// Package model assembles the per-run result this tool renders, combining a
// run's structured plan log with (optionally) its JSON plan output for
// attribute-level diffing - the equivalent of the original script's
// build_run_json.
package model

import (
	"fmt"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/kusshi94/hcp-plan-inspector/internal/diff"
	"github.com/kusshi94/hcp-plan-inspector/internal/tfc"
)

// ChangeCounts mirrors a plan's change_summary counts.
type ChangeCounts struct {
	Add       int    `json:"add"`
	Change    int    `json:"change"`
	Remove    int    `json:"remove"`
	Import    int    `json:"import"`
	Operation string `json:"operation,omitempty"`
}

// Change is one planned resource change, with its attribute-level diff when
// available (computed for update/replace actions, and also for create/delete
// when requested).
type Change struct {
	Action  string           `json:"action"`
	Address string           `json:"address"`
	Diff    []diff.Attribute `json:"diff"`
}

// Diagnostic is one plan diagnostic (error or warning).
type Diagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
	Address  string `json:"address,omitempty"`
}

// RunResult is the full rendered result for a single HCP Terraform run.
type RunResult struct {
	RunID       string        `json:"run_id"`
	Workspace   string        `json:"workspace"`
	URL         string        `json:"url"`
	Status      string        `json:"status"`
	Message     string        `json:"message,omitempty"`
	HasChanges  bool          `json:"has_changes"`
	PlanID      string        `json:"plan_id,omitempty"`
	Summary     *string       `json:"summary"`
	Counts      *ChangeCounts `json:"counts"`
	Changes     []Change      `json:"changes"`
	Diagnostics []Diagnostic  `json:"diagnostics"`
	LogExcerpt  *string       `json:"log_excerpt"`
}

// Build assembles a RunResult from a run's info, its parsed log events, its
// raw-log excerpt (only set when there were no structured events), and its
// plan's resource changes when the JSON output could be fetched (nil
// otherwise - update/replace changes then list only their address, with no
// attribute diff). detailAll also computes the attribute diff for
// create/delete actions, which is otherwise skipped because it lists every
// attribute.
func Build(org string, run tfc.RunInfo, events []tfc.LogEvent, excerpt string, plan *tfjson.Plan, detailAll bool) RunResult {
	byAddress := map[string]*tfjson.ResourceChange{}
	for _, rc := range planResourceChanges(plan) {
		byAddress[rc.Address] = rc
	}

	var summary *string
	var counts *ChangeCounts
	for _, ev := range events {
		if ev.Type != "change_summary" {
			continue
		}
		msg := ev.Message
		summary = &msg
		if ev.Changes != nil {
			counts = &ChangeCounts{
				Add:       ev.Changes.Add,
				Change:    ev.Changes.Change,
				Remove:    ev.Changes.Remove,
				Import:    ev.Changes.Import,
				Operation: ev.Changes.Operation,
			}
		}
	}

	changes := []Change{}
	for _, ev := range events {
		if ev.Type != "planned_change" || ev.Change == nil {
			continue
		}
		action := ev.Change.Action
		attrs := []diff.Attribute{}
		if action == "update" || action == "replace" || (detailAll && (action == "create" || action == "delete")) {
			if rc, ok := byAddress[ev.Change.Resource.Addr]; ok && rc.Change != nil {
				attrs = diff.Leaf(rc.Change)
			}
		}
		changes = append(changes, Change{
			Action:  action,
			Address: ev.Change.Resource.Addr,
			Diff:    attrs,
		})
	}

	diagnostics := []Diagnostic{}
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.Type != "diagnostic" || ev.Diagnostic == nil {
			continue
		}
		d := Diagnostic{
			Severity: ev.Diagnostic.Severity,
			Summary:  ev.Diagnostic.Summary,
			Detail:   ev.Diagnostic.Detail,
			Address:  ev.Diagnostic.Address,
		}
		key := d.Severity + "\x00" + d.Summary + "\x00" + d.Address
		if seen[key] {
			continue
		}
		seen[key] = true
		diagnostics = append(diagnostics, d)
	}

	var logExcerpt *string
	if excerpt != "" {
		logExcerpt = &excerpt
	}

	return RunResult{
		RunID:       run.ID,
		Workspace:   run.Workspace,
		URL:         fmt.Sprintf("https://app.terraform.io/app/%s/%s/runs/%s", org, run.Workspace, run.ID),
		Status:      run.Status,
		Message:     run.Message,
		HasChanges:  run.HasChanges,
		PlanID:      run.PlanID,
		Summary:     summary,
		Counts:      counts,
		Changes:     changes,
		Diagnostics: diagnostics,
		LogExcerpt:  logExcerpt,
	}
}

func planResourceChanges(plan *tfjson.Plan) []*tfjson.ResourceChange {
	if plan == nil {
		return nil
	}
	return plan.ResourceChanges
}
