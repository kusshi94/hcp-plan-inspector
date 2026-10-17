// Package render turns []model.RunResult into the tool's three output
// formats: colorized human-readable text (with a final destroy/error
// summary that is easy to skim), and machine-readable JSON. Raw log output
// is handled by the caller directly, since it bypasses the model entirely.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kusshi94/hcp-plan-inspector/internal/model"
)

// MaxDiffLinesPerResource caps how many attribute diff lines are shown per
// resource in text output, unless TextOptions.Full is set.
const MaxDiffLinesPerResource = 20

// MaxValueChars caps how many characters of a single before/after value are
// shown in text output before it is cut with an ellipsis, unless
// TextOptions.Full is set. JSON output is never truncated.
const MaxValueChars = 80

// TextOptions controls the human-oriented text output.
type TextOptions struct {
	// Color enables ANSI colors; it should reflect both an interactive
	// stdout and the absence of NO_COLOR.
	Color bool
	// Full disables both the per-resource diff line cap and the per-value
	// character cap.
	Full bool
}

type palette struct {
	red, yellow, bold, reset string
}

func newPalette(colorEnabled bool) palette {
	if !colorEnabled {
		return palette{}
	}
	return palette{
		red:    "\033[31m",
		yellow: "\033[33m",
		bold:   "\033[1m",
		reset:  "\033[0m",
	}
}

// Text writes results in the human-oriented format, followed by an overall
// destroy/replace and diagnostics summary.
func Text(w io.Writer, results []model.RunResult, opts TextOptions) {
	t := &textWriter{w: w, p: newPalette(opts.Color), full: opts.Full}
	for _, r := range results {
		t.writeRun(r)
		fmt.Fprintln(w)
	}
	writeTotals(w, results)
	if t.truncated {
		fmt.Fprintln(w, "  ※ 一部の差分・値を省略しています (--full で全件表示)")
	}
}

type textWriter struct {
	w    io.Writer
	p    palette
	full bool
	// truncated records whether anything was omitted, so the footer can
	// point at --full.
	truncated bool
}

func (t *textWriter) writeRun(r model.RunResult) {
	w, p := t.w, t.p
	fmt.Fprintf(w, "%s=== %s  (%s) ===%s\n", p.bold, r.Workspace, r.RunID, p.reset)

	status := "  status      : " + r.Status
	if !r.HasChanges && r.Summary != nil {
		status += "  [no changes]"
	}
	fmt.Fprintln(w, status)

	if r.Summary != nil {
		fmt.Fprintf(w, "  summary     : %s\n", *r.Summary)
	}

	if len(r.Changes) > 0 {
		fmt.Fprintln(w, "  changes     :")
		for _, ch := range r.Changes {
			t.writeChange(ch)
		}
	}

	if len(r.Diagnostics) > 0 {
		fmt.Fprintln(w, "  diagnostics :")
		for _, d := range r.Diagnostics {
			writeDiagnostic(w, p, d)
		}
	}

	if r.LogExcerpt != nil {
		fmt.Fprintln(w, "  log (末尾)  :")
		for line := range strings.SplitSeq(*r.LogExcerpt, "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}

	fmt.Fprintf(w, "  url         : %s\n", r.URL)
}

func (t *textWriter) writeChange(ch model.Change) {
	w, p := t.w, t.p
	destructive := ch.Action == "delete" || ch.Action == "replace"
	prefix := "       " // 7 spaces, aligned with the destructive marker below
	if destructive {
		prefix = "    !! " // 4 spaces + "!! " = 7 chars
	}
	line := fmt.Sprintf("%s%-7s %s", prefix, ch.Action, ch.Address)
	if destructive {
		fmt.Fprintf(w, "%s%s%s\n", p.red, line, p.reset)
	} else {
		fmt.Fprintln(w, line)
	}

	shown := ch.Diff
	var omitted int
	if !t.full && len(shown) > MaxDiffLinesPerResource {
		omitted = len(shown) - MaxDiffLinesPerResource
		shown = shown[:MaxDiffLinesPerResource]
		t.truncated = true
	}
	for _, a := range shown {
		l := fmt.Sprintf("               %s: %s -> %s", a.Path, t.value(a.Before), t.value(a.After))
		if a.ForcesReplacement {
			l += "  (forces replacement)"
		}
		fmt.Fprintln(w, l)
	}
	if omitted > 0 {
		fmt.Fprintf(w, "               ... 他 %d 件 (--full で全件)\n", omitted)
	}
}

// value truncates s to MaxValueChars runes unless full output was requested.
func (t *textWriter) value(s string) string {
	if t.full {
		return s
	}
	runes := []rune(s)
	if len(runes) <= MaxValueChars {
		return s
	}
	t.truncated = true
	return string(runes[:MaxValueChars]) + "…"
}

func writeDiagnostic(w io.Writer, p palette, d model.Diagnostic) {
	line := fmt.Sprintf("    [%s] %s", d.Severity, d.Summary)
	if d.Address != "" {
		line += fmt.Sprintf(" (%s)", d.Address)
	}
	switch d.Severity {
	case "error":
		fmt.Fprintf(w, "%s%s%s\n", p.red, line, p.reset)
	case "warning":
		fmt.Fprintf(w, "%s%s%s\n", p.yellow, line, p.reset)
	default:
		fmt.Fprintln(w, line)
	}
}

func writeTotals(w io.Writer, results []model.RunResult) {
	var destructive, errs, warnings int
	var erroredRuns, awaitingApply []string

	for _, r := range results {
		for _, ch := range r.Changes {
			if ch.Action == "delete" || ch.Action == "replace" {
				destructive++
			}
		}
		for _, d := range r.Diagnostics {
			switch d.Severity {
			case "error":
				errs++
			case "warning":
				warnings++
			}
		}
		switch r.Status {
		case "errored":
			erroredRuns = append(erroredRuns, r.Workspace)
		case "planned", "policy_checked":
			awaitingApply = append(awaitingApply, r.Workspace)
		}
	}

	fmt.Fprintln(w, "--- 全体 ---")
	fmt.Fprintf(w, "  destroy / replace されるリソース: %d\n", destructive)
	fmt.Fprintf(w, "  diagnostics: error %d / warning %d\n", errs, warnings)
	if len(erroredRuns) > 0 {
		fmt.Fprintf(w, "  errored な run: %s\n", strings.Join(erroredRuns, ", "))
	}
	if len(awaitingApply) > 0 {
		fmt.Fprintf(w, "  apply 待ち (Confirm Apply が必要): %s\n", strings.Join(awaitingApply, ", "))
	}
}

// JSON writes results as indented, machine-readable JSON.
func JSON(w io.Writer, results []model.RunResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(results)
}
