// Package diff computes leaf-level attribute differences for a Terraform
// plan's update/replace resource changes, mirroring the jq `leaf_diff`
// function of the original shell script this tool was ported from: it
// walks every scalar leaf path present in either the before or after value,
// masks anything marked sensitive on either side, and reports
// unknown-after-apply values instead of a stale "known" value.
package diff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	tfjson "github.com/hashicorp/terraform-json"
)

// Attribute is a single before -> after leaf attribute difference.
type Attribute struct {
	Path              string `json:"path"`
	Before            string `json:"before"`
	After             string `json:"after"`
	ForcesReplacement bool   `json:"forces_replacement"`
}

// Leaf computes the leaf-level attribute differences for change. It is only
// meaningful for "update" and "replace" actions: "create"/"delete" touch
// every attribute, so the caller should skip calling this for those actions
// and just list the resource address instead.
func Leaf(change *tfjson.Change) []Attribute {
	before := orEmptyObject(change.Before)
	after := orEmptyObject(change.After)
	beforeSensitive := orEmptyObject(change.BeforeSensitive)
	afterSensitive := orEmptyObject(change.AfterSensitive)
	unknown := orEmptyObject(change.AfterUnknown)
	replacePaths := toPathList(change.ReplacePaths)

	paths := unionLeafPaths(before, after)

	attrs := make([]Attribute, 0, len(paths))
	for _, path := range paths {
		b := getPath(before, path)
		a := getPath(after, path)
		isUnknown := marked(unknown, path)
		if jsonEqual(b, a) && !isUnknown {
			continue
		}

		isSensitive := marked(beforeSensitive, path) || marked(afterSensitive, path)

		var beforeStr, afterStr string
		if isSensitive {
			beforeStr = "(sensitive)"
		} else {
			beforeStr = show(b)
		}
		switch {
		case isUnknown:
			afterStr = "(known after apply)"
		case isSensitive:
			afterStr = "(sensitive)"
		default:
			afterStr = show(a)
		}

		attrs = append(attrs, Attribute{
			Path:              joinPath(path),
			Before:            beforeStr,
			After:             afterStr,
			ForcesReplacement: containsPath(replacePaths, path),
		})
	}

	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Path < attrs[j].Path })
	return attrs
}

// orEmptyObject mirrors jq's `// {}`: a JSON null (or absent field, which
// decodes to a nil any the same way) is treated as an empty object
// contributing no leaf paths.
func orEmptyObject(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

// unionLeafPaths returns the sorted, deduplicated set of paths in before and
// after whose value is a scalar (string, number, bool, or null) - i.e. the
// equivalent of jq's `paths(scalars)`.
func unionLeafPaths(before, after any) [][]any {
	seen := map[string][]any{}
	collectLeafPaths(before, nil, seen)
	collectLeafPaths(after, nil, seen)

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([][]any, len(keys))
	for i, k := range keys {
		out[i] = seen[k]
	}
	return out
}

func collectLeafPaths(v any, prefix []any, out map[string][]any) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			collectLeafPaths(vv, append(clonePath(prefix), k), out)
		}
	case []any:
		for i, vv := range t {
			collectLeafPaths(vv, append(clonePath(prefix), i), out)
		}
	default:
		// scalar (string, float64, bool) or nil: a leaf.
		p := clonePath(prefix)
		out[joinPath(p)] = p
	}
}

func clonePath(prefix []any) []any {
	out := make([]any, len(prefix))
	copy(out, prefix)
	return out
}

// getPath navigates v by path, returning nil if any segment is missing or of
// the wrong shape - the equivalent of jq's `try getpath($path) catch null`.
func getPath(v any, path []any) any {
	cur := v
	for _, elem := range path {
		switch key := elem.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[key]
		case int:
			s, ok := cur.([]any)
			if !ok || key < 0 || key >= len(s) {
				return nil
			}
			cur = s[key]
		default:
			return nil
		}
	}
	return cur
}

// marked reports whether tree is `true` at path or at any prefix of path,
// mirroring the original script's rule that a sensitive/unknown marking on a
// parent covers everything beneath it.
func marked(tree any, path []any) bool {
	if b, ok := tree.(bool); ok && b {
		return true
	}
	cur := tree
	for _, elem := range path {
		switch key := elem.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return false
			}
			cur = m[key]
		case int:
			s, ok := cur.([]any)
			if !ok || key < 0 || key >= len(s) {
				return false
			}
			cur = s[key]
		default:
			return false
		}
		if b, ok := cur.(bool); ok && b {
			return true
		}
	}
	return false
}

// show renders value as compact JSON. It never truncates: shortening for
// display is the text renderer's job, so JSON output keeps full values.
func show(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func joinPath(path []any) string {
	s := ""
	for i, elem := range path {
		if i > 0 {
			s += "."
		}
		s += pathElemToString(elem)
	}
	return s
}

func pathElemToString(elem any) string {
	switch v := elem.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toPathList normalizes tfjson.Change.ReplacePaths ([]any of
// []any) into the same [][]any shape used elsewhere, with
// float64 array indices (as decoded from JSON) normalized to int.
func toPathList(raw []any) [][]any {
	out := make([][]any, 0, len(raw))
	for _, p := range raw {
		segs, ok := p.([]any)
		if !ok {
			continue
		}
		norm := make([]any, len(segs))
		for i, s := range segs {
			if f, ok := s.(float64); ok {
				norm[i] = int(f)
			} else {
				norm[i] = s
			}
		}
		out = append(out, norm)
	}
	return out
}

func containsPath(paths [][]any, target []any) bool {
	for _, p := range paths {
		if pathEqual(p, target) {
			return true
		}
	}
	return false
}

func pathEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func jsonEqual(a, b any) bool {
	da, errA := json.Marshal(a)
	db, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
	return string(da) == string(db)
}
