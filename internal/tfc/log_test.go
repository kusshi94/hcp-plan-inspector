package tfc

import (
	"strings"
	"testing"
)

func TestParseLog_StructuredEvents(t *testing.T) {
	raw := strings.Join([]string{
		`{"@level":"info","@message":"Terraform 1.9.0","type":"version"}`,
		`{"@level":"info","@message":"module.a.aws_instance.foo: Refreshing state...","type":"apply_start"}`,
		`{"type":"planned_change","change":{"action":"update","resource":{"addr":"module.a.aws_instance.foo"}}}`,
		`{"type":"planned_change","change":{"action":"delete","resource":{"addr":"module.a.aws_instance.bar"}}}`,
		`{"type":"change_summary","@message":"Plan: 0 to add, 1 to change, 1 to destroy.","changes":{"add":0,"change":1,"remove":1,"import":0,"operation":"plan"}}`,
		`{"type":"diagnostic","diagnostic":{"severity":"warning","summary":"deprecated attribute"}}`,
	}, "\n")

	events, excerpt, err := ParseLog([]byte(raw))
	if err != nil {
		t.Fatalf("ParseLog() error = %v", err)
	}
	if excerpt != "" {
		t.Errorf("excerpt = %q, want empty when structured events exist", excerpt)
	}
	if len(events) != 4 {
		t.Fatalf("len(events) = %d, want 4, events = %+v", len(events), events)
	}
	if events[0].Type != "planned_change" || events[0].Change.Action != "update" {
		t.Errorf("events[0] = %+v, want planned_change/update", events[0])
	}
	if events[2].Type != "change_summary" || events[2].Changes.Change != 1 {
		t.Errorf("events[2] = %+v, want change_summary with 1 change", events[2])
	}
	if events[3].Diagnostic.Severity != "warning" {
		t.Errorf("events[3].Diagnostic = %+v, want severity warning", events[3].Diagnostic)
	}
}

func TestParseLog_ErroredRunFallsBackToExcerpt(t *testing.T) {
	lines := make([]string, 0, 30)
	for range 30 {
		lines = append(lines, "plain text log line")
	}
	lines = append(lines, "Error: something went wrong")
	raw := strings.Join(lines, "\n")

	events, excerpt, err := ParseLog([]byte(raw))
	if err != nil {
		t.Fatalf("ParseLog() error = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("len(events) = %d, want 0", len(events))
	}
	excerptLines := strings.Split(excerpt, "\n")
	if len(excerptLines) != LogExcerptLines {
		t.Errorf("len(excerptLines) = %d, want %d", len(excerptLines), LogExcerptLines)
	}
	if excerptLines[len(excerptLines)-1] != "Error: something went wrong" {
		t.Errorf("last excerpt line = %q, want the final error line", excerptLines[len(excerptLines)-1])
	}
}

func TestParseLog_IgnoresUnrelatedJSONTypes(t *testing.T) {
	raw := `{"@level":"info","@message":"hello","type":"version"}`
	events, excerpt, err := ParseLog([]byte(raw))
	if err != nil {
		t.Fatalf("ParseLog() error = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("len(events) = %d, want 0 for unrelated event type", len(events))
	}
	// A JSON line that isn't one of our types doesn't count as "plain text"
	// either, so there is nothing to fall back to.
	if excerpt != "" {
		t.Errorf("excerpt = %q, want empty", excerpt)
	}
}
