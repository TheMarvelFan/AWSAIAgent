package configdiff

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiffDetectsAddRemoveReplace(t *testing.T) {
	from := json.RawMessage(`{"region":"ap-south-1","blocks":[{"id":"api","parameters":{"cpu":256}}],"gone":true}`)
	to := json.RawMessage(`{"region":"us-east-1","blocks":[{"id":"api","parameters":{"cpu":512,"memory":1024}}]}`)

	changes, err := Diff(from, to)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	want := map[string]OpKind{
		"/region":                     OpReplace,
		"/blocks/0/parameters/cpu":    OpReplace,
		"/blocks/0/parameters/memory": OpAdd,
		"/gone":                       OpRemove,
	}
	if len(changes) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(changes), len(want), changes)
	}
	for _, c := range changes {
		op, ok := want[c.Path]
		if !ok {
			t.Errorf("unexpected path %q", c.Path)
			continue
		}
		if op != c.Op {
			t.Errorf("path %q: got op %q, want %q", c.Path, c.Op, op)
		}
	}

	stats := Summarize(changes)
	if stats.Added != 1 || stats.Removed != 1 || stats.Replaced != 2 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestDiffIgnoresKeyOrder(t *testing.T) {
	from := json.RawMessage(`{"a":1,"b":2}`)
	to := json.RawMessage(`{"b":2,"a":1}`)
	changes, err := Diff(from, to)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %+v", changes)
	}
}

func TestDiffFromEmptyIsAddAtRoot(t *testing.T) {
	changes, err := Diff(nil, json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 1 || changes[0].Op != OpAdd || changes[0].Path != "/" {
		t.Fatalf("unexpected changes: %+v", changes)
	}
}

func TestUnifiedMarksChangedLines(t *testing.T) {
	out, err := Unified(json.RawMessage(`{"region":"ap-south-1"}`), json.RawMessage(`{"region":"us-east-1"}`), 2)
	if err != nil {
		t.Fatalf("Unified: %v", err)
	}
	if !strings.Contains(out, `-  "region": "ap-south-1"`) {
		t.Errorf("missing removal line:\n%s", out)
	}
	if !strings.Contains(out, `+  "region": "us-east-1"`) {
		t.Errorf("missing addition line:\n%s", out)
	}
}
