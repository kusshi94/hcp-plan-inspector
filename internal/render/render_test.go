package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kusshi94/hcp-plan-inspector/internal/diff"
	"github.com/kusshi94/hcp-plan-inspector/internal/model"
)

func sampleSummary() *string {
	s := "Plan: 0 to add, 8 to change, 0 to destroy."
	return &s
}

func TestText_NoColor(t *testing.T) {
	results := []model.RunResult{
		{
			RunID:      "run-abc",
			Workspace:  "example-app_production",
			URL:        "https://app.terraform.io/app/example-org/example-app_production/runs/run-abc",
			Status:     "planned_and_finished",
			HasChanges: true,
			Summary:    sampleSummary(),
			Changes: []model.Change{
				{
					Action:  "update",
					Address: "aws_launch_template.foo",
					Diff: []diff.Attribute{
						{Path: "latest_version", Before: "37", After: "(known after apply)"},
					},
				},
				{Action: "delete", Address: "aws_instance.old"},
			},
		},
	}

	var buf bytes.Buffer
	Text(&buf, results, TextOptions{})
	out := buf.String()

	for _, want := range []string{
		"=== example-app_production  (run-abc) ===",
		"  status      : planned_and_finished",
		"  summary     : Plan: 0 to add, 8 to change, 0 to destroy.",
		"  changes     :",
		"       update  aws_launch_template.foo",
		"               latest_version: 37 -> (known after apply)",
		"    !! delete  aws_instance.old",
		"  url         : https://app.terraform.io/app/example-org/example-app_production/runs/run-abc",
		"--- 全体 ---",
		"  destroy / replace されるリソース: 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}

	if strings.Contains(out, "\033[") {
		t.Errorf("output contains ANSI escapes despite colorEnabled=false:\n%s", out)
	}
}

func TestText_ColorWrapsDestructiveLine(t *testing.T) {
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{{Action: "replace", Address: "aws_instance.x"}}},
	}
	var buf bytes.Buffer
	Text(&buf, results, TextOptions{Color: true})
	out := buf.String()
	if !strings.Contains(out, "\033[31m") {
		t.Errorf("expected red escape around destructive line, got:\n%s", out)
	}
}

func TestText_TruncatesDiffAtMaxLines(t *testing.T) {
	attrs := make([]diff.Attribute, MaxDiffLinesPerResource+5)
	for i := range attrs {
		attrs[i] = diff.Attribute{Path: "field", Before: "a", After: "b"}
	}
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{{Action: "update", Address: "aws_instance.x", Diff: attrs}}},
	}
	var buf bytes.Buffer
	Text(&buf, results, TextOptions{})
	out := buf.String()
	if !strings.Contains(out, "... 他 5 件 (--full で全件)") {
		t.Errorf("expected truncation notice for 5 omitted lines, got:\n%s", out)
	}
}

func TestText_FullShowsAllLines(t *testing.T) {
	attrs := make([]diff.Attribute, MaxDiffLinesPerResource+5)
	for i := range attrs {
		attrs[i] = diff.Attribute{Path: "field", Before: "a", After: "b"}
	}
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{{Action: "update", Address: "aws_instance.x", Diff: attrs}}},
	}
	var buf bytes.Buffer
	Text(&buf, results, TextOptions{Full: true})
	out := buf.String()
	if got := strings.Count(out, "field: a -> b"); got != len(attrs) {
		t.Errorf("shown diff lines = %d, want %d\n%s", got, len(attrs), out)
	}
	if strings.Contains(out, "--full") {
		t.Errorf("expected no truncation notice with Full, got:\n%s", out)
	}
}

func TestText_TruncatesLongValue(t *testing.T) {
	long := `"` + strings.Repeat("a", 200) + `"`
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{{
			Action: "update", Address: "aws_s3_bucket_policy.x",
			Diff: []diff.Attribute{{Path: "policy", Before: `"short"`, After: long}},
		}}},
	}

	var buf bytes.Buffer
	Text(&buf, results, TextOptions{})
	out := buf.String()
	want := `policy: "short" -> ` + string([]rune(long)[:MaxValueChars]) + "…\n"
	if !strings.Contains(out, want) {
		t.Errorf("expected value truncated to %d runes, got:\n%s", MaxValueChars, out)
	}
	if !strings.Contains(out, "--full で全件表示") {
		t.Errorf("expected footer pointing at --full, got:\n%s", out)
	}

	buf.Reset()
	Text(&buf, results, TextOptions{Full: true})
	out = buf.String()
	if !strings.Contains(out, `policy: "short" -> `+long+"\n") {
		t.Errorf("expected full value with Full, got:\n%s", out)
	}
	if strings.Contains(out, "--full") {
		t.Errorf("expected no truncation notice with Full, got:\n%s", out)
	}
}

func TestText_NoNoticeWhenNothingTruncated(t *testing.T) {
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{{
			Action: "update", Address: "aws_instance.x",
			Diff: []diff.Attribute{{Path: "ami", Before: `"ami-old"`, After: `"ami-new"`}},
		}}},
	}
	var buf bytes.Buffer
	Text(&buf, results, TextOptions{})
	if out := buf.String(); strings.Contains(out, "--full") {
		t.Errorf("unexpected truncation notice:\n%s", out)
	}
}

func TestJSON_KeepsLongValues(t *testing.T) {
	long := `"` + strings.Repeat("a", 200) + `"`
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{{
			Action: "update", Address: "aws_s3_bucket_policy.x",
			Diff: []diff.Attribute{{Path: "policy", Before: `"short"`, After: long}},
		}}, Diagnostics: []model.Diagnostic{}},
	}
	var buf bytes.Buffer
	if err := JSON(&buf, results); err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	var decoded []model.RunResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := decoded[0].Changes[0].Diff[0].After; got != long {
		t.Errorf("After = %q, want full value", got)
	}
}

func TestJSON_RoundTrips(t *testing.T) {
	results := []model.RunResult{
		{RunID: "run-abc", Workspace: "ws", Changes: []model.Change{}, Diagnostics: []model.Diagnostic{}},
	}
	var buf bytes.Buffer
	if err := JSON(&buf, results); err != nil {
		t.Fatalf("JSON() error = %v", err)
	}

	var decoded []model.RunResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output = %s", err, buf.String())
	}
	if len(decoded) != 1 || decoded[0].RunID != "run-abc" {
		t.Errorf("decoded = %+v, want run-abc", decoded)
	}
	if !strings.Contains(buf.String(), `"changes": []`) {
		t.Errorf("expected empty changes to serialize as [], got:\n%s", buf.String())
	}
}
