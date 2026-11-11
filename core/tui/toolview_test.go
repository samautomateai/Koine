package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/llm"
)

func call(name, args, result string, done, isErr bool) *ToolCall {
	return &ToolCall{ID: "1", Name: name, Args: json.RawMessage(args), Result: result, Done: done, IsError: isErr}
}

func rendered(tc *ToolCall, expanded bool) []string {
	var out []string
	for _, l := range tc.Render(ToolOptions{Width: 60, Expanded: expanded}) {
		out = append(out, strings.TrimRight(StripANSI(l), " "))
	}
	return out
}

func TestToolSummary(t *testing.T) {
	cases := []struct {
		tc   *ToolCall
		want string
	}{
		{call("bash", `{"command":"ls   -la\n/tmp"}`, "", false, false), "$ ls -la /tmp"},
		{call("read", `{"path":"a.go","offset":20}`, "", false, false), "a.go:20"},
		{call("edit", `{"file_path":"b.go"}`, "", false, false), "b.go"},
		{call("grep", `{"pattern":"TODO","path":"src"}`, "", false, false), "TODO src"},
		{call("custom", `{"b":2,"a":"x"}`, "", false, false), "a=x b=2"},
		{call("bash", `{}`, "", false, false), ""},
	}
	for _, c := range cases {
		if got := c.tc.Summary(); got != c.want {
			t.Errorf("%s: got %q want %q", c.tc.Name, got, c.want)
		}
	}
}

func TestDiff(t *testing.T) {
	d := Diff("a\nb\nc\nd", "a\nB\nc\nd\ne")
	var got []string
	for _, l := range d {
		got = append(got, string(l.Op)+l.Text)
	}
	want := "|" + strings.Join([]string{" a", "-b", "+B", " c", " d", "+e"}, "|")
	if "|"+strings.Join(got, "|") != want {
		t.Errorf("got %q", got)
	}
	if d := Diff("", "x\ny"); len(d) != 2 || d[0].Op != '+' {
		t.Errorf("insert only: %v", d)
	}
	if d := Diff("x", ""); len(d) != 1 || d[0].Op != '-' {
		t.Errorf("delete only: %v", d)
	}
	big := strings.Repeat("l\n", 600)
	if d := Diff(big, big+"x"); len(d) != 601+601 && len(d) == 0 {
		t.Error("huge inputs fall back to remove-all/add-all")
	}
	same := strings.Repeat("same\n", 10)
	trimmed := trimContext(Diff(same+"old\n"+same, same+"new\n"+same), 2)
	if len(trimmed) != 8 || trimmed[0].Op != 0 || trimmed[7].Op != 0 {
		t.Errorf("context is trimmed to 2 lines each side with an ellipsis: %v", trimmed)
	}
}

func TestEditRendersADiffFromItsArguments(t *testing.T) {
	tc := call("edit", `{"path":"x.go","old":"alpha\nbeta","new":"alpha\ngamma"}`, "edited 1 replacement(s)", true, false)
	got := strings.Join(rendered(tc, false), "\n")
	for _, want := range []string{"edit x.go", "  alpha", "- beta", "+ gamma"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	th := Builtin("dark")
	th.TrueColor = true
	styled := strings.Join(tc.Render(ToolOptions{Width: 60, Theme: th}), "\n")
	if !strings.Contains(styled, th.Fg("toolDiffAdded", "+ gamma")) || !strings.Contains(styled, th.Fg("toolDiffRemoved", "- beta")) ||
		!strings.Contains(styled, "\x1b[48;2;36;49;39m") {
		t.Errorf("diff colours and the success background: %q", styled)
	}
	multi := call("edit", `{"path":"x","edits":[{"oldText":"a","newText":"b"},{"oldText":"c","newText":"d"}]}`, "", true, false)
	if got := strings.Join(rendered(multi, false), "\n"); !strings.Contains(got, "- a") || !strings.Contains(got, "+ d") || !strings.Contains(got, "…") {
		t.Errorf("several edits: %s", got)
	}
	camel := call("edit", `{"path":"x","old_string":"p","new_string":""}`, "", true, false)
	if got := strings.Join(rendered(camel, false), "\n"); !strings.Contains(got, "- p") {
		t.Errorf("deleting text: %s", got)
	}
	// a tool result that already is a unified diff wins over the arguments
	withDiff := call("edit", `{"path":"x","old":"a","new":"b"}`, "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b", true, false)
	if got := strings.Join(rendered(withDiff, false), "\n"); !strings.Contains(got, "@@ -1 +1 @@") || !strings.Contains(got, "--- a/x") {
		t.Errorf("unified diff output: %s", got)
	}
}

func TestToolOutputCollapsesAndExpands(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	out := strings.Join(lines, "\n")
	bash := call("bash", `{"command":"seq 30"}`, out, true, false)
	c := strings.Join(rendered(bash, false), "\n")
	if !strings.Contains(c, "22 earlier lines (ctrl+o to expand)") || !strings.Contains(c, "line 30") || strings.Contains(c, "line 5\n") {
		t.Errorf("bash keeps the tail:\n%s", c)
	}
	if e := rendered(bash, true); len(e) != 31 {
		t.Errorf("expanded shows everything: %d lines", len(e))
	}
	read := call("read", `{"path":"f"}`, out, true, false)
	c = strings.Join(rendered(read, false), "\n")
	if !strings.Contains(c, "line 8") || strings.Contains(c, "line 9\n") || !strings.Contains(c, "22 more lines (ctrl+o to expand)") {
		t.Errorf("read keeps the head:\n%s", c)
	}
	pending := call("bash", `{"command":"sleep 9"}`, "", false, false)
	if got := rendered(pending, false); len(got) != 1 {
		t.Errorf("a running call is just its title: %v", got)
	}
	failed := call("bash", `{"command":"false"}`, "boom: it failed", true, true)
	th := Builtin("dark")
	th.TrueColor = true
	styled := strings.Join(failed.Render(ToolOptions{Width: 60, Theme: th}), "")
	if !strings.Contains(styled, "boom") || !strings.Contains(styled, "\x1b[48;2;58;37;40m") || !strings.Contains(styled, th.Fg("error", "boom: it failed")) {
		t.Errorf("errors use the error background and colour: %q", styled)
	}
	write := call("write", `{"path":"n.txt","content":"one\ntwo"}`, "wrote 2 lines", true, false)
	if got := strings.Join(rendered(write, false), "\n"); !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Errorf("write shows the content: %s", got)
	}
	long := call("bash", `{"command":"x"}`, strings.Repeat("y", 200), true, false)
	for _, l := range rendered(long, true) {
		if Width(l) > 60 {
			t.Errorf("lines wrap to the width: %d", Width(l))
		}
	}
}

func TestBlocksRenderAndHistory(t *testing.T) {
	th := Builtin("dark")
	c := RenderCtx{Width: 40, Theme: th, OutputPad: 1}
	user := RenderBlock(Block{Kind: "user", Text: "hello there"}, c)
	if len(user) != 1 || !strings.HasPrefix(user[0], " ") || !strings.Contains(user[0], "\x1b[48") {
		t.Errorf("user: %q", user)
	}
	if got := StripANSI(RenderBlock(Block{Kind: "error", Text: "bad"}, c)[0]); got != " error: bad" {
		t.Errorf("error: %q", got)
	}
	if got := RenderBlock(Block{Kind: "thinking", Text: "pondering"}, RenderCtx{Width: 40, HideThinking: true}); StripANSI(got[0]) != "Thinking…" {
		t.Errorf("hidden thinking: %q", got)
	}
	if got := RenderBlock(Block{Kind: "thinking", Text: "pondering"}, RenderCtx{Width: 40}); StripANSI(got[0]) != "pondering" {
		t.Errorf("thinking: %q", got)
	}
	if got := RenderBlock(Block{Kind: "tool"}, c); len(got) != 0 {
		t.Error("a tool block without a call")
	}
	if got := RenderBlock(Block{Kind: "notice", Text: "n"}, RenderCtx{Width: 40}); StripANSI(got[0]) != "n" {
		t.Error("notice")
	}
	if got := RenderBlock(Block{Kind: "assistant", Text: "# T\n\ntext"}, RenderCtx{Width: 40}); len(got) != 3 {
		t.Errorf("assistant is markdown: %q", got)
	}
	all := RenderBlocks([]Block{{Kind: "notice", Text: "a"}, {Kind: "notice", Text: "b"}}, RenderCtx{Width: 40})
	if len(all) != 4 || all[0] != "" || all[2] != "" {
		t.Errorf("blocks are separated by blank lines: %q", all)
	}

	h := []llm.Message{
		{Role: "user", Content: []llm.Block{{Type: "text", Text: "do it"}}},
		{Role: "assistant", Content: []llm.Block{{Type: "thinking", Thinking: "hmm"}, {Type: "text", Text: "ok"},
			{Type: "tool_use", ID: "t1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
		{Role: "user", Content: []llm.Block{{Type: "tool_result", ToolUseID: "t1", Content: "file.txt"}}},
		{Role: "assistant", Content: []llm.Block{{Type: "text", Text: "done"}}},
	}
	bs := BlocksFromHistory(h)
	kinds := ""
	for _, b := range bs {
		kinds += b.Kind[:1]
	}
	if kinds != "utata" {
		t.Errorf("kinds %q", kinds)
	}
	if bs[3].Tool.Result != "file.txt" || !bs[3].Tool.Done {
		t.Errorf("tool results are attached to their calls: %+v", bs[3].Tool)
	}
	if p := UserPrompts(h); len(p) != 1 || p[0] != "do it" {
		t.Errorf("prompts: %v", p)
	}
}

func TestUIEventsBuildBlocks(t *testing.T) {
	ui := newTestUI(t, nil)
	for _, e := range []agent.Event{
		{Type: "tool_start", Tool: "bash", CallID: "a", Args: json.RawMessage(`{"command":"ls"}`)},
		{Type: "tool_start", Tool: "read", CallID: "b", Args: json.RawMessage(`{"path":"f"}`)},
		{Type: "tool_end", Tool: "read", CallID: "b", Text: "contents"},
		{Type: "tool_end", Tool: "bash", CallID: "a", Text: "out", IsError: true},
		{Type: "tool_end", Tool: "ghost", Text: "orphan"},
		{Type: "text_delta", Text: "hel"}, {Type: "text_delta", Text: "lo"}, {Type: "text", Text: "hello"},
		{Type: "thinking_delta", Text: "t"}, {Type: "thinking", Text: "thought"}, {Type: "thinking", Text: "again"},
		{Type: "notice", Text: "note"}, {Type: "error", Text: "bad"}, {Type: "done"}, {Type: "text_delta", Text: "x"},
	} {
		ui.Event(e)
	}
	var kinds []string
	for _, b := range ui.blocks[len(ui.blocks)-9:] {
		kinds = append(kinds, b.Kind)
	}
	if got := strings.Join(kinds, " "); got != "tool tool tool assistant thinking thinking notice error assistant" {
		t.Errorf("kinds: %s", got)
	}
	first := ui.blocks[len(ui.blocks)-9]
	if first.Tool.Name != "bash" || !first.Tool.IsError || first.Tool.Result != "out" || !first.Final {
		t.Errorf("tool_end matches by call id: %+v", first.Tool)
	}
	if ui.blocks[len(ui.blocks)-6].Text != "hello" {
		t.Errorf("the final text replaces the streamed text: %q", ui.blocks[len(ui.blocks)-6].Text)
	}
	if last := ui.blocks[len(ui.blocks)-1]; last.Final {
		t.Error("a delta after the end opens a new unfinished block")
	}
	ui.Event(agent.Event{Type: "usage", Usage: &agent.UsageInfo{
		Usage: llm.Usage{Input: 1500, Output: 20}, ContextTokens: 1520, ContextWindow: 200000, ContextPct: 0.76}})
	if ui.foot.Input != 1500 || !ui.foot.CtxKnown {
		t.Errorf("usage feeds the footer: %+v", ui.foot)
	}
}
