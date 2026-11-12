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

func TestMergeCarriesTheExtensionAPIFields(t *testing.T) {
	var d Decision
	sys, yes := "S", true
	msg := &CustomMessage{CustomType: "t", Content: "c", Display: true}
	d.Merge(Decision{Content: json.RawMessage(`[1]`), Details: json.RawMessage(`{"a":1}`), Images: json.RawMessage(`[2]`), IsError: &yes, Terminate: true})
	d.Merge(Decision{SystemPrompt: &sys, Message: msg, Messages: json.RawMessage(`[3]`)})
	if string(d.Content) != "[1]" || string(d.Details) != `{"a":1}` || string(d.Images) != "[2]" || !*d.IsError || !d.Terminate ||
		*d.SystemPrompt != "S" || d.Message != msg || string(d.Messages) != "[3]" {
		t.Fatalf("%+v", d)
	}
	d.Merge(Decision{Content: json.RawMessage(`[9]`)})
	if string(d.Content) != "[9]" {
		t.Error("a later handler's content replaces the earlier one")
	}

	var c Decision
	c.Merge(Decision{Cancel: true, Reason: "why"})
	c.Merge(Decision{Cancel: true}) // an empty reason must not clobber
	if !c.Cancel || c.Block || c.Reason != "why" {
		t.Fatalf("%+v", c)
	}
	var h Decision
	h.Merge(Decision{Handled: true})
	if !h.Handled || !h.Block {
		t.Fatalf("handled input also stops the chain: %+v", h)
	}
}

func TestMultiStopsAtCancel(t *testing.T) {
	calls := 0
	m := Multi{fn(func(string) Decision { calls++; return Decision{Cancel: true} }), fn(func(string) Decision { calls++; return Decision{} })}
	if d := m.Emit(context.Background(), SessionBeforeSwitch, nil); !d.Cancel || calls != 1 {
		t.Fatalf("%+v calls=%d", d, calls)
	}
}
