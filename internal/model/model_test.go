package model

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/kusshi94/hcp-plan-inspector/internal/tfc"
)

func TestBuild_URLAndBasicFields(t *testing.T) {
	run := tfc.RunInfo{ID: "run-abc123", Workspace: "example-app_production", Status: "planned_and_finished", HasChanges: true, PlanID: "plan-xyz"}
	result := Build("example-org", run, nil, "", nil)

	wantURL := "https://app.terraform.io/app/example-org/example-app_production/runs/run-abc123"
	if result.URL != wantURL {
		t.Errorf("URL = %q, want %q", result.URL, wantURL)
	}
	if result.Changes == nil || len(result.Changes) != 0 {
		t.Errorf("Changes = %v, want non-nil empty slice", result.Changes)
	}
	if result.Diagnostics == nil || len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want non-nil empty slice", result.Diagnostics)
	}
}

func TestBuild_SummaryAndCounts(t *testing.T) {
	events := []tfc.LogEvent{
		{Type: "change_summary", Message: "Plan: 0 to add, 8 to change, 0 to destroy.", Changes: &tfc.ChangeCounts{Change: 8, Operation: "plan"}},
	}
	result := Build("example-org", tfc.RunInfo{}, events, "", nil)

	if result.Summary == nil || *result.Summary != "Plan: 0 to add, 8 to change, 0 to destroy." {
		t.Fatalf("Summary = %v, want the change_summary message", result.Summary)
	}
	if result.Counts == nil || result.Counts.Change != 8 {
		t.Fatalf("Counts = %+v, want Change=8", result.Counts)
	}
}

func TestBuild_UpdateGetsAttributeDiffFromPlan(t *testing.T) {
	events := []tfc.LogEvent{
		{Type: "planned_change", Change: &tfc.PlannedChange{Action: "update", Resource: struct {
			Addr string `json:"addr"`
		}{Addr: "aws_instance.foo"}}},
	}
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "aws_instance.foo",
				Change: &tfjson.Change{
					Before: map[string]any{"ami": "ami-old"},
					After:  map[string]any{"ami": "ami-new"},
				},
			},
		},
	}

	result := Build("example-org", tfc.RunInfo{}, events, "", plan)

	if len(result.Changes) != 1 {
		t.Fatalf("len(Changes) = %d, want 1", len(result.Changes))
	}
	ch := result.Changes[0]
	if ch.Action != "update" || ch.Address != "aws_instance.foo" {
		t.Fatalf("Changes[0] = %+v, want update/aws_instance.foo", ch)
	}
	if len(ch.Diff) != 1 || ch.Diff[0].Path != "ami" {
		t.Fatalf("Changes[0].Diff = %+v, want a diff for \"ami\"", ch.Diff)
	}
}

func TestBuild_CreateActionHasNoDiffEvenWithPlanAvailable(t *testing.T) {
	events := []tfc.LogEvent{
		{Type: "planned_change", Change: &tfc.PlannedChange{Action: "create", Resource: struct {
			Addr string `json:"addr"`
		}{Addr: "aws_instance.new"}}},
	}
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{Address: "aws_instance.new", Change: &tfjson.Change{After: map[string]any{"ami": "ami-1"}}},
		},
	}

	result := Build("example-org", tfc.RunInfo{}, events, "", plan)
	if len(result.Changes) != 1 || len(result.Changes[0].Diff) != 0 {
		t.Fatalf("Changes = %+v, want create action with empty diff", result.Changes)
	}
}

func TestBuild_DiagnosticsDeduplicated(t *testing.T) {
	events := []tfc.LogEvent{
		{Type: "diagnostic", Diagnostic: &tfc.Diagnostic{Severity: "warning", Summary: "deprecated"}},
		{Type: "diagnostic", Diagnostic: &tfc.Diagnostic{Severity: "warning", Summary: "deprecated"}},
	}
	result := Build("example-org", tfc.RunInfo{}, events, "", nil)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(Diagnostics) = %d, want 1 (deduplicated)", len(result.Diagnostics))
	}
}

func TestBuild_LogExcerptOnlySetWhenNonEmpty(t *testing.T) {
	result := Build("example-org", tfc.RunInfo{}, nil, "", nil)
	if result.LogExcerpt != nil {
		t.Errorf("LogExcerpt = %v, want nil for empty excerpt", result.LogExcerpt)
	}

	result = Build("example-org", tfc.RunInfo{}, nil, "boom", nil)
	if result.LogExcerpt == nil || *result.LogExcerpt != "boom" {
		t.Errorf("LogExcerpt = %v, want \"boom\"", result.LogExcerpt)
	}
}
