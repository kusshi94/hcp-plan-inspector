package diff

import (
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func attrByPath(attrs []Attribute, path string) (Attribute, bool) {
	for _, a := range attrs {
		if a.Path == path {
			return a, true
		}
	}
	return Attribute{}, false
}

func TestLeaf_SimpleUpdate(t *testing.T) {
	change := &tfjson.Change{
		Before: map[string]any{"name": "old", "count": float64(1)},
		After:  map[string]any{"name": "new", "count": float64(1)},
	}

	attrs := Leaf(change)

	got, ok := attrByPath(attrs, "name")
	if !ok {
		t.Fatalf("expected diff for %q, attrs = %+v", "name", attrs)
	}
	if got.Before != `"old"` || got.After != `"new"` {
		t.Errorf("name diff = %+v, want before=\"old\" after=\"new\"", got)
	}

	if _, ok := attrByPath(attrs, "count"); ok {
		t.Errorf("unchanged attribute %q should not appear, attrs = %+v", "count", attrs)
	}
}

func TestLeaf_SensitiveMaskedBothSides(t *testing.T) {
	change := &tfjson.Change{
		Before:          map[string]any{"password": "old-secret"},
		After:           map[string]any{"password": "new-secret"},
		BeforeSensitive: map[string]any{"password": true},
		// only before is marked sensitive; after must still be masked.
	}

	attrs := Leaf(change)

	got, ok := attrByPath(attrs, "password")
	if !ok {
		t.Fatalf("expected diff for password, attrs = %+v", attrs)
	}
	if got.Before != "(sensitive)" || got.After != "(sensitive)" {
		t.Errorf("password diff = %+v, want both sides masked", got)
	}
}

func TestLeaf_WholeValueSensitive(t *testing.T) {
	change := &tfjson.Change{
		Before:         map[string]any{"secret": map[string]any{"a": "x"}},
		After:          map[string]any{"secret": map[string]any{"a": "y"}},
		AfterSensitive: map[string]any{"secret": true},
	}

	attrs := Leaf(change)
	got, ok := attrByPath(attrs, "secret.a")
	if !ok {
		t.Fatalf("expected diff for secret.a, attrs = %+v", attrs)
	}
	if got.Before != "(sensitive)" || got.After != "(sensitive)" {
		t.Errorf("secret.a diff = %+v, want masked via parent marking", got)
	}
}

func TestLeaf_KnownAfterApply(t *testing.T) {
	change := &tfjson.Change{
		Before:       map[string]any{"version": "37"},
		After:        map[string]any{"version": "38"},
		AfterUnknown: map[string]any{"version": true},
	}

	attrs := Leaf(change)
	got, ok := attrByPath(attrs, "version")
	if !ok {
		t.Fatalf("expected diff for version, attrs = %+v", attrs)
	}
	if got.After != "(known after apply)" {
		t.Errorf("version.After = %q, want (known after apply)", got.After)
	}
}

func TestLeaf_UnknownStillReportedEvenIfValuesEqual(t *testing.T) {
	// terraform sometimes reports the same placeholder for before/after while
	// marking the value unknown; it must still show up as a change.
	change := &tfjson.Change{
		Before:       map[string]any{"id": nil},
		After:        map[string]any{"id": nil},
		AfterUnknown: map[string]any{"id": true},
	}

	attrs := Leaf(change)
	if _, ok := attrByPath(attrs, "id"); !ok {
		t.Fatalf("expected unknown attribute to be reported even though before == after, attrs = %+v", attrs)
	}
}

func TestLeaf_ForcesReplacement(t *testing.T) {
	change := &tfjson.Change{
		Before:       map[string]any{"ami": "ami-old"},
		After:        map[string]any{"ami": "ami-new"},
		ReplacePaths: []any{[]any{"ami"}},
	}

	attrs := Leaf(change)
	got, ok := attrByPath(attrs, "ami")
	if !ok {
		t.Fatalf("expected diff for ami, attrs = %+v", attrs)
	}
	if !got.ForcesReplacement {
		t.Errorf("ami.ForcesReplacement = false, want true")
	}
}

func TestLeaf_LongValueNotTruncated(t *testing.T) {
	long := strings.Repeat("a", 200)
	change := &tfjson.Change{
		Before: map[string]any{"blob": "short"},
		After:  map[string]any{"blob": long},
	}

	attrs := Leaf(change)
	got, ok := attrByPath(attrs, "blob")
	if !ok {
		t.Fatalf("expected diff for blob, attrs = %+v", attrs)
	}
	if want := `"` + long + `"`; got.After != want {
		t.Errorf("blob.After = %q, want full value %q", got.After, want)
	}
}

func TestLeaf_NilBeforeTreatedAsEmpty(t *testing.T) {
	// create-like shape: Before is nil (no prior state).
	change := &tfjson.Change{
		Before: nil,
		After:  map[string]any{"name": "new"},
	}

	attrs := Leaf(change)
	got, ok := attrByPath(attrs, "name")
	if !ok {
		t.Fatalf("expected diff for name, attrs = %+v", attrs)
	}
	if got.Before != "null" {
		t.Errorf("name.Before = %q, want \"null\" (jq tojson of absent value)", got.Before)
	}
}

func TestLeaf_NestedArrayIndexPath(t *testing.T) {
	change := &tfjson.Change{
		Before: map[string]any{"tags": []any{"a", "b"}},
		After:  map[string]any{"tags": []any{"a", "c"}},
	}

	attrs := Leaf(change)
	if _, ok := attrByPath(attrs, "tags.1"); !ok {
		t.Fatalf("expected diff for tags.1, attrs = %+v", attrs)
	}
	if _, ok := attrByPath(attrs, "tags.0"); ok {
		t.Errorf("tags.0 unchanged, should not appear, attrs = %+v", attrs)
	}
}

func TestLeaf_KeyRemovedEntirely(t *testing.T) {
	change := &tfjson.Change{
		Before: map[string]any{"old_field": "x"},
		After:  map[string]any{},
	}

	attrs := Leaf(change)
	got, ok := attrByPath(attrs, "old_field")
	if !ok {
		t.Fatalf("expected diff for old_field, attrs = %+v", attrs)
	}
	if got.After != "null" {
		t.Errorf("old_field.After = %q, want \"null\"", got.After)
	}
}
