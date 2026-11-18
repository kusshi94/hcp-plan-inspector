// Command hcp-plan-inspector fetches HCP Terraform Plan results using the
// terraform CLI's stored credentials and prints them - read-only, so that
// "what will this PR actually change" doesn't require opening each
// workspace in HCP Terraform by hand.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/kusshi94/hcp-plan-inspector/internal/ghrun"
	"github.com/kusshi94/hcp-plan-inspector/internal/model"
	"github.com/kusshi94/hcp-plan-inspector/internal/render"
	"github.com/kusshi94/hcp-plan-inspector/internal/tfc"
	"github.com/kusshi94/hcp-plan-inspector/internal/token"
)

// version is set via -ldflags at release build time (see .goreleaser.yaml).
var version = "dev"

const orgEnvVar = "TFC_ORG"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

type options struct {
	runIDs    []string
	workspace string
	org       string
	jsonOut   bool
	rawOut    bool
	noDetail  bool
	full      bool
}

func newRootCmd() *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:     "hcp-plan-inspector [PR番号]",
		Version: version,
		Short:   "HCP Terraform 上の Plan 内容を read-only で取得して表示する",
		Long: strings.TrimSpace(`
terraform CLI の認証情報 (` + "`terraform login`" + ` で保存されるトークン) を使って、
HCP Terraform API から Plan の内容を取得して表示します。

行うのは Run / Plan の取得とログのダウンロード (いずれも GET) のみです。
Apply / Discard / Cancel など状態を変更する API は呼びません。Apply は
HCP Terraform 上で人間が Confirm Apply して行ってください。

update / replace のリソースは、変更される属性を
"属性: 変更前 -> 変更後" の形で表示します (create / delete は量が多いため一覧のみ。
--full を付けると create / delete も属性を表示します)。
sensitive とマークされた値は "(sensitive)" に伏せて表示します。
長い値や多数の差分は省略されるので、全件見るには --full を付けてください
(--json は常に省略しません。--json でも create / delete の属性を見たい場合は --full を併用してください)。

トークンは以下の順で探します。見つからない場合は ` + "`terraform login`" + ` を案内して終了します。
  1. 環境変数 TF_TOKEN_app_terraform_io
  2. 環境変数 TFC_CREDENTIALS_FILE が指すファイル
  3. ~/.terraform.d/credentials.tfrc.json (` + "`terraform login`" + ` の保存先)

organization は --org か環境変数 TFC_ORG で指定してください。
`),
		Example: strings.Trim(`
  hcp-plan-inspector --org example-org 123                          # PR の全 workspace の plan
  hcp-plan-inspector --org example-org 123 -w production            # production を含む workspace だけ
  hcp-plan-inspector --org example-org -r run-example00000001       # Run ID 直指定
  hcp-plan-inspector --org example-org 123 --json | jq '...'        # 後段で加工する / AI に読ませる
`, "\n"),
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var prNumber string
			if len(args) == 1 {
				prNumber = args[0]
				if _, err := strconv.Atoi(prNumber); err != nil {
					return fmt.Errorf("PR 番号は数値で指定してください: %s", prNumber)
				}
			}
			if prNumber == "" && len(opts.runIDs) == 0 {
				return fmt.Errorf("PR 番号か Run ID (-r) を指定してください")
			}

			if opts.org == "" {
				opts.org = os.Getenv(orgEnvVar)
			}
			if opts.org == "" {
				return fmt.Errorf("--org または環境変数 %s で organization を指定してください", orgEnvVar)
			}

			return runInspect(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), prNumber, opts)
		},
	}

	cmd.Flags().StringArrayVarP(&opts.runIDs, "run", "r", nil, "Run ID を直接指定する (複数指定可)")
	cmd.Flags().StringVarP(&opts.workspace, "workspace", "w", "", "workspace 名で絞り込む (部分一致)")
	cmd.Flags().StringVar(&opts.org, "org", "", "organization を指定する (既定: 環境変数 TFC_ORG)")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "機械可読な JSON で出力する")
	cmd.Flags().BoolVar(&opts.rawOut, "raw", false, "構造化ログの生データを出力する")
	cmd.Flags().BoolVar(&opts.noDetail, "no-detail", false, "属性差分を取得しない (速くなる)")
	cmd.Flags().BoolVar(&opts.full, "full", false, "差分の行数・値の長さを省略せずに表示し、create / delete の属性も表示する")

	return cmd
}

func runInspect(ctx context.Context, stdout, stderr io.Writer, prNumber string, opts *options) error {
	tok, err := token.Resolve()
	if err != nil {
		return err
	}

	client, err := tfc.NewClient(tok)
	if err != nil {
		return err
	}

	runIDs := opts.runIDs
	if prNumber != "" {
		ids, err := ghrun.CollectRunIDs(ctx, ghrun.GHCommandRunner, prNumber, opts.workspace)
		if err != nil {
			return err
		}
		runIDs = ids
	}
	for _, id := range runIDs {
		if !strings.HasPrefix(id, "run-") {
			return fmt.Errorf("Run ID は run- で始まる必要があります: %s", id)
		}
	}

	if opts.rawOut {
		return runRaw(ctx, stdout, client, runIDs)
	}

	results := make([]model.RunResult, 0, len(runIDs))
	for _, id := range runIDs {
		result, err := inspectRun(ctx, stderr, client, opts.org, id, !opts.noDetail, opts.full)
		if err != nil {
			return err
		}
		results = append(results, result)
	}

	if opts.jsonOut {
		return render.JSON(stdout, results)
	}

	render.Text(stdout, results, render.TextOptions{Color: colorEnabled(stdout), Full: opts.full})
	return nil
}

func runRaw(ctx context.Context, stdout io.Writer, client *tfc.Client, runIDs []string) error {
	for _, id := range runIDs {
		run, err := client.FetchRun(ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "=== %s  (%s) ===\n", run.Workspace, run.ID)
		if run.PlanID != "" {
			logReader, err := client.FetchPlanLogRaw(ctx, run.PlanID)
			if err != nil {
				return err
			}
			if _, err := io.Copy(stdout, logReader); err != nil {
				return fmt.Errorf("plan ログの取得に失敗しました (%s): %w", run.PlanID, err)
			}
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func inspectRun(ctx context.Context, stderr io.Writer, client *tfc.Client, org, runID string, showDetail, detailAll bool) (model.RunResult, error) {
	run, err := client.FetchRun(ctx, runID)
	if err != nil {
		return model.RunResult{}, err
	}

	var events []tfc.LogEvent
	var excerpt string
	var plan *tfjson.Plan

	if run.PlanID != "" {
		events, excerpt, err = client.FetchPlanLog(ctx, run.PlanID)
		if err != nil {
			return model.RunResult{}, err
		}

		if showDetail && hasDetailTarget(events, detailAll) {
			jsonOutput := client.FetchPlanJSONOutput(ctx, run.PlanID)
			if jsonOutput.Warning != "" {
				fmt.Fprintln(stderr, "警告:", jsonOutput.Warning)
			}
			plan = jsonOutput.Plan
		}
	}

	return model.Build(org, *run, events, excerpt, plan, detailAll), nil
}

// hasDetailTarget reports whether any planned change needs the JSON plan
// output for its attribute diff, so runs without one skip the extra fetch.
func hasDetailTarget(events []tfc.LogEvent, detailAll bool) bool {
	for _, ev := range events {
		if ev.Type != "planned_change" || ev.Change == nil {
			continue
		}
		switch ev.Change.Action {
		case "update", "replace":
			return true
		case "create", "delete":
			if detailAll {
				return true
			}
		}
	}
	return false
}

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
