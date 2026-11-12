package events

import (
	"context"
	"encoding/json"
	"testing"
)

type fn func(string) Decision

func (f fn) Emit(_ context.Context, n string, _ any) Decision { return f(n) }

func TestMerge(t *testing.T) {
	var d Decision
	s1, s2 := "x", "y"
	d.Merge(Decision{SystemAppend: "a", Args: json.RawMessage(`{"a":1}`)})
	d.Merge(Decision{SystemAppend: "b", Output: &s1, Text: &s2})
	d.Merge(Decision{Block: true, Reason: "no"})
	d.Merge(Decision{Block: true}) // empty reason must not clobber
	if d.SystemAppend != "a\nb" || string(d.Args) != `{"a":1}` || *d.Output != "x" || *d.Text != "y" || !d.Block || d.Reason != "no" {
		t.Fatalf("%+v", d)
	}
}

func TestMergeAddsUpDiscoveredResources(t *testing.T) {
	var d Decision
	d.Merge(Decision{})
	d.Merge(Decision{Resources: &Resources{}})
	if d.Resources != nil {
		t.Fatalf("no paths, no resources: %+v", d.Resources)
	}
	d.Merge(Decision{Resources: &Resources{SkillPaths: []string{"/a"}, ThemePaths: []string{"/t"}}})
	d.Merge(Decision{Resources: &Resources{SkillPaths: []string{"/b"}, PromptPaths: []string{"/p"}}})
	r := d.Resources
	if len(r.SkillPaths) != 2 || r.SkillPaths[1] != "/b" || len(r.PromptPaths) != 1 || len(r.ThemePaths) != 1 || r.Empty() {
		t.Fatalf("%+v", r)
	}
	if !(*Resources)(nil).Empty() {
		t.Error("nil is empty")
	}
}

func TestMultiStopsAtBlock(t *testing.T) {
	calls := 0
	m := Multi{nil, fn(func(string) Decision { calls++; return Decision{Block: true, Reason: "r"} }), fn(func(string) Decision { calls++; return Decision{} })}
	d := m.Emit(context.Background(), ToolCall, nil)
	if !d.Block || d.Reason != "r" || calls != 1 {
		t.Fatalf("%+v calls=%d", d, calls)
	}
}

func TestMultiMergesAndNop(t *testing.T) {
	m := Multi{fn(func(string) Decision { return Decision{SystemAppend: "a"} }), Nop{}, fn(func(string) Decision { return Decision{SystemAppend: "b"} })}
	if d := m.Emit(context.Background(), SystemPrompt, nil); d.SystemAppend != "a\nb" {
		t.Fatalf("%+v", d)
	}
}
