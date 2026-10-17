// Package ghrun resolves the HCP Terraform Run IDs attached to a GitHub pull
// request by shelling out to the `gh` CLI, so that this tool reuses the
// caller's existing `gh auth` session instead of handling GitHub credentials
// itself.
package ghrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// check mirrors the fields requested from `gh pr checks --json name,state,link`.
type check struct {
	Name string `json:"name"`
	Link string `json:"link"`
}

// CommandRunner executes `gh pr checks <prNumber>` and returns its stdout and
// stderr. It exists so tests can substitute a fake without invoking gh.
type CommandRunner func(ctx context.Context, prNumber string) (stdout, stderr []byte)

// GHCommandRunner is the CommandRunner backed by the real gh CLI.
func GHCommandRunner(ctx context.Context, prNumber string) (stdout, stderr []byte) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "checks", prNumber, "--json", "name,state,link")
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	// gh exits non-zero when any check is failing/pending, which is not an
	// error for our purposes, so its exit code is deliberately ignored - only
	// whether stdout produced anything is checked by the caller.
	_ = cmd.Run()
	return stdoutBuf.Bytes(), stderrBuf.Bytes()
}

var (
	runIDPattern    = regexp.MustCompile(`/runs/(run-[0-9A-Za-z]+)`)
	workspacePrefix = regexp.MustCompile(`^Terraform Cloud/[^/]+/`)
)

// CollectRunIDs returns the HCP Terraform Run IDs attached to prNumber via
// its "Terraform Cloud/<app>/<workspace>" GitHub checks, optionally filtered
// to workspaces whose name contains workspaceFilter. The result is sorted by
// workspace name, matching the ordering a human reviewing the PR would see.
func CollectRunIDs(ctx context.Context, run CommandRunner, prNumber, workspaceFilter string) ([]string, error) {
	stdout, stderr := run(ctx, prNumber)
	if len(bytes.TrimSpace(stdout)) == 0 {
		msg := fmt.Sprintf("PR #%s の checks を取得できませんでした。PR 番号 / gh の認証 / 実行中のリポジトリを確認してください", prNumber)
		if s := strings.TrimSpace(string(stderr)); s != "" {
			msg += "\n  gh: " + s
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var checks []check
	if err := json.Unmarshal(stdout, &checks); err != nil {
		return nil, fmt.Errorf("gh pr checks の出力をパースできませんでした: %w", err)
	}

	type item struct {
		workspace string
		runID     string
	}
	var items []item
	for _, c := range checks {
		if !strings.HasPrefix(c.Name, "Terraform Cloud/") {
			continue
		}
		m := runIDPattern.FindStringSubmatch(c.Link)
		if m == nil {
			continue
		}
		workspace := workspacePrefix.ReplaceAllString(c.Name, "")
		if workspaceFilter != "" && !strings.Contains(workspace, workspaceFilter) {
			continue
		}
		items = append(items, item{workspace: workspace, runID: m[1]})
	}

	if len(items) == 0 {
		if workspaceFilter != "" {
			return nil, fmt.Errorf("PR #%s に workspace 名が %q を含む Terraform Cloud の check が見つかりません", prNumber, workspaceFilter)
		}
		return nil, fmt.Errorf("PR #%s に Terraform Cloud の check が見つかりません。まだ Run が作られていない可能性があります", prNumber)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].workspace < items[j].workspace })

	runIDs := make([]string, len(items))
	for i, it := range items {
		runIDs[i] = it.runID
	}
	return runIDs, nil
}
