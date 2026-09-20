// Package configdiff compares two architecture config documents.
//
// It produces two representations of the same delta:
//   - Changes: structural RFC 6901-style ops, for rendering a field-by-field
//     "what moved" view in the UI and for storing on the version row.
//   - Unified: a text diff of the pretty-printed JSON, for a plain
//     side-by-side/monospace view.
package configdiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type OpKind string

const (
	OpAdd     OpKind = "add"
	OpRemove  OpKind = "remove"
	OpReplace OpKind = "replace"
)

type Change struct {
	Op   OpKind `json:"op"`
	Path string `json:"path"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

type Stats struct {
	Added    int `json:"added"`
	Removed  int `json:"removed"`
	Replaced int `json:"replaced"`
}

type Result struct {
	From    *int     `json:"from_version,omitempty"`
	To      *int     `json:"to_version,omitempty"`
	Changes []Change `json:"changes"`
	Stats   Stats    `json:"stats"`
	Unified string   `json:"unified,omitempty"`
}

// ErrTooLargeForText guards the O(n*m) LCS below. Config documents are small;
// if one is not, something has gone wrong upstream.
var ErrTooLargeForText = errors.New("document too large for text diff")

const maxTextDiffLines = 2000

// Diff walks both documents and returns changes ordered by path so output is
// stable across calls (important: it gets persisted and compared in tests).
func Diff(from, to json.RawMessage) ([]Change, error) {
	a, err := decode(from)
	if err != nil {
		return nil, fmt.Errorf("decode 'from': %w", err)
	}
	b, err := decode(to)
	if err != nil {
		return nil, fmt.Errorf("decode 'to': %w", err)
	}

	var changes []Change
	walk("", a, b, &changes)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	if changes == nil {
		changes = []Change{}
	}
	return changes, nil
}

func Summarize(changes []Change) Stats {
	var s Stats
	for _, c := range changes {
		switch c.Op {
		case OpAdd:
			s.Added++
		case OpRemove:
			s.Removed++
		case OpReplace:
			s.Replaced++
		}
	}
	return s
}

func decode(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func walk(path string, a, b any, out *[]Change) {
	if a == nil && b == nil {
		return
	}
	if a == nil {
		*out = append(*out, Change{Op: OpAdd, Path: orRoot(path), To: b})
		return
	}
	if b == nil {
		*out = append(*out, Change{Op: OpRemove, Path: orRoot(path), From: a})
		return
	}

	am, aIsMap := a.(map[string]any)
	bm, bIsMap := b.(map[string]any)
	if aIsMap && bIsMap {
		for _, k := range unionKeys(am, bm) {
			av, aOK := am[k]
			bv, bOK := bm[k]
			child := path + "/" + escape(k)
			switch {
			case aOK && !bOK:
				*out = append(*out, Change{Op: OpRemove, Path: child, From: av})
			case !aOK && bOK:
				*out = append(*out, Change{Op: OpAdd, Path: child, To: bv})
			default:
				walk(child, av, bv, out)
			}
		}
		return
	}

	as, aIsSlice := a.([]any)
	bs, bIsSlice := b.([]any)
	if aIsSlice && bIsSlice {
		// Index-wise comparison. Good enough for these documents, where the
		// blocks array is short and blocks carry stable ids; a reorder shows up
		// as replacements rather than a move, which is acceptable.
		n := max(len(as), len(bs))
		for i := 0; i < n; i++ {
			child := fmt.Sprintf("%s/%d", path, i)
			switch {
			case i >= len(bs):
				*out = append(*out, Change{Op: OpRemove, Path: child, From: as[i]})
			case i >= len(as):
				*out = append(*out, Change{Op: OpAdd, Path: child, To: bs[i]})
			default:
				walk(child, as[i], bs[i], out)
			}
		}
		return
	}

	if !reflect.DeepEqual(a, b) {
		*out = append(*out, Change{Op: OpReplace, Path: orRoot(path), From: a, To: b})
	}
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// escape applies RFC 6901 JSON Pointer escaping.
func escape(k string) string {
	k = strings.ReplaceAll(k, "~", "~0")
	return strings.ReplaceAll(k, "/", "~1")
}

func orRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}
