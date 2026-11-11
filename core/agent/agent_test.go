package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agents"
	"koine/core/events"
	"koine/core/llm"
	"koine/core/skills"
	"koine/core/tools"
)

type script struct {
	turns [][]llm.Block
	i     int
	reqs  []llm.Request
	err   error
}

func (s *script) Complete(_ context.Context, r llm.Request) (*llm.Response, error) {
	s.reqs = append(s.reqs, r)
	if s.err != nil {
		return nil, s.err
	}
	if s.i >= len(s.turns) {
		return &llm.Response{Content: []llm.Block{{Type: "text", Text: "end"}}}, nil
	}
	s.i++
	return &llm.Response{Content: s.turns[s.i-1]}, nil
}

func txt(s string) llm.Block { return llm.Block{Type: "text", Text: s} }
func use(id, n, in string) llm.Block {
	return llm.Block{Type: "tool_use", ID: id, Name: n, Input: json.RawMessage(in)}
}

func newAgent(p llm.Provider) *Agent {
	r := tools.NewRegistry()
	r.Register(&tools.Func{N: "echo", D: "d", S: json.RawMessage(`{"type":"object"}`),
		F: func(_ context.Context, a json.RawMessage) (string, error) { return "echo:" + string(a), nil }})
	r.Register(&tools.Func{N: "boom", D: "d", S: json.RawMessage(`{"type":"object"}`),
		F: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("kaboom") }})
	return &Agent{Provider: p, Model: "m", Cwd: "/w", Tools: r}
}

type disp func(name string, p any) events.Decision

func (d disp) Emit(_ context.Context, n string, p any) events.Decision { return d(n, p) }

func collect(a *Agent, in string) ([]Event, error) {
	var evs []Event
	err := a.Run(context.Background(), in, func(e Event) { evs = append(evs, e) })
	return evs, err
}

func lastResults(s *script) []llm.Block {
	m := s.reqs[len(s.reqs)-1].Messages
	return m[len(m)-1].Content
}

func TestLoopRunsToolsAndStops(t *testing.T) {
	s := &script{turns: [][]llm.Block{{txt("thinking"), use("1", "echo", `{"a":1}`), use("2", "boom", `{}`), use("3", "nope", `{}`)}}}
	a := newAgent(s)
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	if got := strings.Join(types, ","); got != "text,tool_start,tool_start,tool_start,tool_end,tool_end,tool_end,text,done" {
		t.Fatalf("events: %s", got)
	}
	// tool results go back to the model as one user message
	res := s.reqs[1].Messages[2].Content
	bad := res[1].Content != "kaboom" || !strings.Contains(res[2].Content, "unknown tool")
	if len(res) != 3 || res[0].Content != `echo:{"a":1}` || res[0].IsError || !res[1].IsError || bad {
		t.Fatalf("%+v", res)
	}
	if len(s.reqs[0].Tools) != 2 {
		t.Fatalf("tools advertised: %v", s.reqs[0].Tools)
	}
	if len(a.History) != 4 {
		t.Fatalf("history len %d", len(a.History))
	}
	a.Reset()
	if a.History != nil {
		t.Fatal("reset")
	}
}

func TestSystemPromptContents(t *testing.T) {
	s := &script{}
	a := newAgent(s)
	a.ContextFiles = []string{"<context>CTX</context>"}
	a.Skills = []skills.Skill{{Name: "sk", Description: "ds", Location: "/l"}}
	a.Defs = []agents.Def{{Name: "rev", Description: "reviews"}}
	a.Dispatch = disp(func(n string, p any) events.Decision {
		if n == events.SystemPrompt {
			return events.Decision{SystemAppend: "EXTRA"}
		}
		return events.Decision{}
	})
	if _, err := collect(a, "hi"); err != nil {
		t.Fatal(err)
	}
	sys := s.reqs[0].System
	for _, want := range []string{"koine", "Working directory: /w", "CTX", "<name>sk</name>", "- rev: reviews", "EXTRA"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	a.Base = "CUSTOM BASE"
	a.depth = 1 // sub-agents don't advertise further sub-agents
	s.reqs = nil
	_, _ = collect(a, "hi")
	if sys = s.reqs[0].System; !strings.HasPrefix(sys, "CUSTOM BASE") || strings.Contains(sys, "rev:") {
		t.Errorf("%q", sys)
	}
}

func TestToolCallHooks(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("1", "echo", `{"x":1}`), use("2", "echo", `{"x":2}`), use("3", "echo", `{"x":3}`)}}}
	a := newAgent(s)
	rewritten := "REWRITTEN"
	a.Dispatch = disp(func(n string, p any) events.Decision {
		m, _ := p.(map[string]any)
		switch n {
		case events.ToolCall:
			switch string(m["input"].(json.RawMessage)) {
			case `{"x":1}`:
				return events.Decision{Block: true, Reason: "denied"}
			case `{"x":2}`:
				return events.Decision{Args: json.RawMessage(`{"x":"changed"}`)}
			}
		case events.ToolResult:
			if strings.Contains(m["output"].(string), "changed") {
				return events.Decision{Output: &rewritten}
			}
		}
		return events.Decision{}
	})
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	r := lastResults(s)
	if !r[0].IsError || r[0].Content != "Blocked: denied" {
		t.Errorf("block: %+v", r[0])
	}
	if r[1].Content != "REWRITTEN" {
		t.Errorf("rewrite: %+v", r[1])
	}
	if r[2].Content != `echo:{"x":3}` {
		t.Errorf("passthrough: %+v", r[2])
	}
	if got := orDefault("", "d"); got != "d" || orDefault("x", "d") != "x" {
		t.Error("orDefault")
	}
}

func TestInputHook(t *testing.T) {
	s := &script{}
	a := newAgent(s)
	newText := "rewritten input"
	mode := "rewrite"
	a.Dispatch = disp(func(n string, p any) events.Decision {
		if n != events.Input {
			return events.Decision{}
		}
		if mode == "block" {
			return events.Decision{Block: true, Reason: "nope"}
		}
		return events.Decision{Text: &newText}
	})
	_, _ = collect(a, "orig")
	if got := s.reqs[0].Messages[0].Content[0].Text; got != newText {
		t.Fatalf("%q", got)
	}
	mode = "block"
	s.reqs = nil
	evs, err := collect(a, "orig")
	if err != nil || len(s.reqs) != 0 || len(evs) != 1 || !strings.Contains(evs[0].Text, "input blocked: nope") {
		t.Fatalf("%v %v %v", evs, err, len(s.reqs))
	}
}

func TestMaxTurnsProviderErrorAndCancel(t *testing.T) {
	loop := &script{}
	for i := 0; i < 10; i++ {
		loop.turns = append(loop.turns, []llm.Block{use("x", "echo", `{}`)})
	}
	a := newAgent(loop)
	a.MaxTurns = 3
	evs, err := collect(a, "go")
	if err != nil || len(loop.reqs) != 3 || evs[len(evs)-2].Type != "notice" || !strings.Contains(evs[len(evs)-2].Text, "3 turns") {
		t.Fatalf("%v %d %v", err, len(loop.reqs), evs[len(evs)-2:])
	}

	bad := newAgent(&script{err: errors.New("api down")})
	evs, err = collect(bad, "go")
	if err == nil || evs[len(evs)-1].Type != "error" {
		t.Fatalf("%v %v", err, evs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newAgent(&script{}).Run(ctx, "go", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestExpandInput(t *testing.T) {
	a := newAgent(&script{})
	a.Skills = []skills.Skill{{Name: "pdf", Description: "d", Location: "/s/SKILL.md", Dir: "/s", Body: "PDF BODY"}}
	if got := a.ExpandInput("/skill:pdf make it"); !strings.Contains(got, "PDF BODY") || !strings.HasSuffix(got, "make it") {
		t.Fatalf("%q", got)
	}
	if got := a.ExpandInput("/skill:pdf"); strings.Contains(got, "\n\n\n") || !strings.Contains(got, "PDF BODY") {
		t.Fatalf("%q", got)
	}
	for _, in := range []string{"/skill:unknown x", "plain", "/other"} {
		if a.ExpandInput(in) != in {
			t.Errorf("%q must pass through", in)
		}
	}
}

func TestTaskToolSubAgents(t *testing.T) {
	// parent -> task(reviewer) -> sub-agent answers "sub answer" -> parent finishes
	s := &script{turns: [][]llm.Block{
		{use("t", "task", `{"agent":"reviewer","prompt":"check it"}`)},
		{txt("sub answer")},
	}}
	a := newAgent(s)
	a.Defs = []agents.Def{{Name: "reviewer", Description: "r", Prompt: "YOU REVIEW", Model: "haiku", Deny: []string{"boom"}}}
	a.Tools.Register(a.TaskTool())
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	sub := s.reqs[1]
	if sub.Model != "haiku" || !strings.HasPrefix(sub.System, "YOU REVIEW") || sub.Messages[0].Content[0].Text != "check it" {
		t.Fatalf("sub request: model=%s sys=%q", sub.Model, sub.System[:20])
	}
	for _, td := range sub.Tools {
		if td.Name == "boom" || td.Name == "task" {
			t.Errorf("sub-agent must not get %s", td.Name)
		}
	}
	if r := s.reqs[2].Messages[2].Content[0]; r.Content != "sub answer" || r.IsError {
		t.Fatalf("parent got %+v", r)
	}

	tt := a.TaskTool()
	call := func(args string) error { _, err := tt.Run(context.Background(), json.RawMessage(args)); return err }
	if call(`{"agent":"missing","prompt":"x"}`) == nil || call(`bad`) == nil {
		t.Fatal("unknown agent / bad json must error")
	}
	a.depth = 2
	if err := call(`{"agent":"reviewer","prompt":"x"}`); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("%v", err)
	}
}

func TestLoadContextFiles(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("agents rules"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("  \n"), 0o644) // blank: skipped
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("user rules"), 0o644)
	got := LoadContextFiles(cwd, home)
	if len(got) != 2 || !strings.Contains(got[0], "user rules") || !strings.Contains(got[1], "agents rules") {
		t.Fatalf("%v", got)
	}
	if len(LoadContextFiles(t.TempDir(), "")) != 0 {
		t.Fatal("empty")
	}
}

func TestNilEmitAndDispatch(t *testing.T) {
	a := newAgent(&script{})
	if err := a.Run(context.Background(), "x", nil); err != nil { // nil emit and nil Dispatch are fine
		t.Fatal(err)
	}
}
