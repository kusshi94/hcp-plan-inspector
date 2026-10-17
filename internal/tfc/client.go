// Package tfc wraps the parts of the HCP Terraform (Terraform Cloud) API
// this tool needs: reading a run and its workspace, downloading a plan's
// structured log, and fetching a plan's JSON output (terraform show -json
// equivalent) for attribute-level diffing. Every call is a GET - this
// package never applies, discards, or cancels anything.
package tfc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	tfe "github.com/hashicorp/go-tfe"
	tfjson "github.com/hashicorp/terraform-json"
)

// LogExcerptLines is how many lines of raw log to keep when a plan has no
// structured log events at all (e.g. it errored before Terraform could even
// start emitting them).
const LogExcerptLines = 25

// Client is a thin, read-only wrapper around the go-tfe client.
type Client struct {
	tfe *tfe.Client
}

// NewClient builds a Client authenticated with token against the default
// HCP Terraform address (app.terraform.io).
func NewClient(token string) (*Client, error) {
	c, err := tfe.NewClient(&tfe.Config{Token: token})
	if err != nil {
		return nil, fmt.Errorf("HCP Terraform クライアントの初期化に失敗しました: %w", err)
	}
	return &Client{tfe: c}, nil
}

// RunInfo is the subset of a Terraform Cloud Run this tool renders.
type RunInfo struct {
	ID         string
	Workspace  string
	Status     string
	Message    string
	HasChanges bool
	PlanID     string
}

// FetchRun reads a run and its workspace name in a single request.
func (c *Client) FetchRun(ctx context.Context, runID string) (*RunInfo, error) {
	run, err := c.tfe.Runs.ReadWithOptions(ctx, runID, &tfe.RunReadOptions{
		Include: []tfe.RunIncludeOpt{tfe.RunWorkspace},
	})
	if err != nil {
		return nil, translateAPIError(err, fmt.Sprintf("Run %s の取得", runID))
	}

	workspace := "unknown"
	if run.Workspace != nil && run.Workspace.Name != "" {
		workspace = run.Workspace.Name
	}
	planID := ""
	if run.Plan != nil {
		planID = run.Plan.ID
	}

	return &RunInfo{
		ID:         run.ID,
		Workspace:  workspace,
		Status:     string(run.Status),
		Message:    run.Message,
		HasChanges: run.HasChanges,
		PlanID:     planID,
	}, nil
}

// LogEvent is one line of a plan's structured log
// (https://developer.hashicorp.com/terraform/internals/machine-readable-ui).
// Only the event types this tool acts on are modeled; everything else is
// ignored.
type LogEvent struct {
	Type       string         `json:"type"`
	Message    string         `json:"@message"`
	Change     *PlannedChange `json:"change,omitempty"`
	Changes    *ChangeCounts  `json:"changes,omitempty"`
	Diagnostic *Diagnostic    `json:"diagnostic,omitempty"`
}

// PlannedChange is the payload of a "planned_change" log event.
type PlannedChange struct {
	Action   string `json:"action"`
	Resource struct {
		Addr string `json:"addr"`
	} `json:"resource"`
}

// ChangeCounts is the payload of a "change_summary" log event's "changes" field.
type ChangeCounts struct {
	Add       int    `json:"add"`
	Change    int    `json:"change"`
	Remove    int    `json:"remove"`
	Import    int    `json:"import"`
	Operation string `json:"operation"`
}

// Diagnostic is the payload of a "diagnostic" log event.
type Diagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
	Address  string `json:"address,omitempty"`
}

var structuredEventTypes = map[string]bool{
	"planned_change": true,
	"change_summary": true,
	"diagnostic":     true,
}

// FetchPlanLogRaw returns plan's log as a stream of raw bytes, for callers
// that want to pass the untouched structured log through (the --raw output
// mode) rather than parsing it.
func (c *Client) FetchPlanLogRaw(ctx context.Context, planID string) (io.Reader, error) {
	r, err := c.tfe.Plans.Logs(ctx, planID)
	if err != nil {
		return nil, translateAPIError(err, fmt.Sprintf("Plan %s のログ取得", planID))
	}
	return r, nil
}

// FetchPlanLog downloads plan's log and extracts the planned_change /
// change_summary / diagnostic events used to render its result. If the plan
// has no structured log at all (e.g. it errored before Terraform emitted
// any), the last LogExcerptLines non-JSON lines are returned as excerpt
// instead, mirroring the original script's fallback for errored runs.
func (c *Client) FetchPlanLog(ctx context.Context, planID string) (events []LogEvent, excerpt string, err error) {
	r, err := c.tfe.Plans.Logs(ctx, planID)
	if err != nil {
		return nil, "", translateAPIError(err, fmt.Sprintf("Plan %s のログ取得", planID))
	}

	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("plan ログの取得に失敗しました (%s): %w", planID, err)
	}

	return ParseLog(raw)
}

// ParseLog splits a plan's raw log into structured events and, if none are
// found, a plain-text excerpt of its tail.
func ParseLog(raw []byte) (events []LogEvent, excerpt string, err error) {
	var nonJSONLines []string

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t\r")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "{") {
			nonJSONLines = append(nonJSONLines, line)
			continue
		}

		var typed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &typed); err != nil {
			continue
		}
		if !structuredEventTypes[typed.Type] {
			continue
		}

		var ev LogEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("plan ログの読み込みに失敗しました: %w", err)
	}

	if len(events) == 0 && len(nonJSONLines) > 0 {
		start := 0
		if len(nonJSONLines) > LogExcerptLines {
			start = len(nonJSONLines) - LogExcerptLines
		}
		excerpt = strings.Join(nonJSONLines[start:], "\n")
	}

	return events, excerpt, nil
}

// JSONOutputResult is the outcome of fetching a plan's JSON output. Because
// this endpoint requires admin access to the workspace, a failure to fetch
// it is not treated as fatal by this package: Plan is nil and Warning
// explains why, and the caller decides how (or whether) to surface it.
type JSONOutputResult struct {
	Plan    *tfjson.Plan
	Warning string
}

// FetchPlanJSONOutput fetches the terraform-show-json equivalent of a plan,
// used to compute per-attribute update/replace diffs. A failure here
// (typically a 403 because the caller lacks admin access to the workspace)
// is reported via JSONOutputResult.Warning rather than an error, since the
// resource-level plan summary is still usable without it.
func (c *Client) FetchPlanJSONOutput(ctx context.Context, planID string) JSONOutputResult {
	raw, err := c.tfe.Plans.ReadJSONOutput(ctx, planID)
	if err != nil {
		warning := fmt.Sprintf("plan の詳細差分を取得できませんでした (%s)", err)
		if errors.Is(err, tfe.ErrUnauthorized) {
			warning = "plan の詳細差分を取得できませんでした (401)。この API は workspace への admin 権限が必要です。--no-detail で警告を抑制できます"
		} else if isForbidden(err) {
			warning = "plan の詳細差分を取得できませんでした (403)。この API は workspace への admin 権限が必要です。--no-detail で警告を抑制できます"
		}
		return JSONOutputResult{Warning: warning}
	}

	var plan tfjson.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return JSONOutputResult{Warning: fmt.Sprintf("plan の詳細差分のパースに失敗しました (%s): %s", planID, err)}
	}
	return JSONOutputResult{Plan: &plan}
}

// isForbidden reports whether err looks like it came from an HTTP 403. go-tfe
// has no dedicated sentinel for 403 (unlike 401/404), so this falls back to
// matching the JSON:API error payload text it decodes into.
func isForbidden(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "forbidden") || strings.Contains(msg, "403")
}

func translateAPIError(err error, action string) error {
	switch {
	case errors.Is(err, tfe.ErrUnauthorized):
		return fmt.Errorf("401 Unauthorized: トークンが無効か期限切れです。`terraform login` で取り直してください")
	case errors.Is(err, tfe.ErrResourceNotFound):
		return fmt.Errorf("404: %s に失敗しました。存在しないか、その workspace の read 権限がありません", action)
	default:
		return fmt.Errorf("%s に失敗しました: %w", action, err)
	}
}
