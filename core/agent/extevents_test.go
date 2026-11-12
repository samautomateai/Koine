package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"koine/core/events"
	"koine/core/llm"
	"koine/core/tools"
)

// recorder is a Dispatcher that notes every event and answers with the decision `answer` gives for it.
type recorder struct {
	mu     sync.Mutex
	names  []string
	loads  map[string][]any
	answer func(name string, p map[string]any) events.Decision
}

func (r *recorder) Emit(_ context.Context, n string, p any) events.Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, n)
	if r.loads == nil {
		r.loads = map[string][]any{}
	}
	r.loads[n] = append(r.loads[n], p)
	if r.answer != nil {
		m, _ := p.(map[string]any)
		return r.answer(n, m)
	}
	return events.Decision{}
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, x := range r.names {
		if x == name {
			n++
		}
	}
	return n
}

func (r *recorder) first(name string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.loads[name]; len(l) > 0 {
		m, _ := l[0].(map[string]any)
		return m
	}
	return nil
}

func TestCompactionGateAndEvent(t *testing.T) {
	cancel := true
	rec := &recorder{answer: func(n string, _ map[string]any) events.Decision {
		return events.Decision{Cancel: n == events.SessionBeforeCompact && cancel}
	}}
	p := &summariser{}
	a := newAgent(p)
	a.Dispatch = rec
	a.KeepRecent = 250
	filled(a, 10)
	n := len(a.History)
	if _, err := a.CompactFor(context.Background(), "focus", ReasonManual, nil); !errors.Is(err, ErrCompactCancelled) {
		t.Fatalf("err %v", err)
	}
	if p.summaries != 0 || len(a.History) != n || rec.count(events.SessionCompact) != 0 {
		t.Fatal("a cancelled compaction must change nothing")
	}
	if g := rec.first(events.SessionBeforeCompact); g["reason"] != ReasonManual || g["customInstructions"] != "focus" || g["willRetry"] != false {
		t.Errorf("gate payload %v", g)
	}

	cancel = false
	if _, err := a.CompactFor(context.Background(), "", ReasonOverflow, nil); err != nil {
		t.Fatal(err)
	}
	ev := rec.first(events.SessionCompact)
	entry, _ := ev["compactionEntry"].(map[string]any)
	if ev == nil || entry["summary"] != "SUMMARY" || ev["reason"] != ReasonOverflow || ev["willRetry"] != true || ev["fromExtension"] != false {
		t.Fatalf("session_compact payload %v", ev)
	}
}

func TestToolExecutionEventsReachExtensions(t *testing.T) {
	rec := &recorder{}
	s := &script{turns: [][]llm.Block{{use("a", "stream", `{"n":"3"}`)}}}
	a := newAgent(s)
	a.Tools.Register(&tools.Func{N: "stream", D: "d", S: json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`),
		F: func(ctx context.Context, args json.RawMessage) (string, error) {
			tools.Update(ctx, "half")
			return "full " + string(args), nil
		}})
	a.Dispatch = rec
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	order := strings.Join(rec.names, ",")
	for _, want := range []string{
		"tool_execution_start,tool_call", "tool_execution_update", "tool_execution_end", "agent_end",
	} {
		if !strings.Contains(order, want) {
			t.Errorf("missing %q in %s", want, order)
		}
	}
	st := rec.first(events.ToolExecutionStart)
	if st["toolCallId"] != "a" || st["toolName"] != "stream" {
		t.Errorf("start %v", st)
	}
	up := rec.first(events.ToolExecutionUpdate)
	if up["partialResult"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "half" {
		t.Errorf("update %v", up)
	}
	end := rec.first(events.ToolExecutionEnd)
	text := end["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if end["isError"] != false || !strings.HasPrefix(text, "full ") {
		t.Errorf("end %v", end)
	}
	if call := rec.first(events.ToolCall); !strings.Contains(string(call["input"].(json.RawMessage)), "3") {
		t.Errorf("tool_call sees the coerced arguments: %v", call)
	}
}

func TestInvalidArgumentsAreAnswerWithoutToolCallOrRun(t *testing.T) {
	rec := &recorder{}
	ran := false
	s := &script{turns: [][]llm.Block{{use("a", "strict", `{"n":"many"}`), use("b", "strict", `{}`)}}}
	a := newAgent(s)
	a.Tools.Register(&tools.Func{N: "strict", D: "d", S: json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`),
		F: func(context.Context, json.RawMessage) (string, error) { ran = true; return "ran", nil }})
	a.Dispatch = rec
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	res := lastResults(s)
	if ran || rec.count(events.ToolCall) != 0 || !res[0].IsError || !strings.Contains(res[0].Content, `Invalid arguments for tool "strict"`) ||
		!strings.Contains(res[0].Content, "- n: expected number") || !strings.Contains(res[1].Content, "- n: missing") {
		t.Fatalf("ran=%v results=%+v", ran, res)
	}
	if rec.count(events.ToolExecutionEnd) != 2 {
		t.Error("an invalid call still ends its execution")
	}
}

func TestToolResultContentAndDetailsReplaceTheResult(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("a", "echo", `{}`)}}}
	a := newAgent(s)
	a.Dispatch = disp(func(n string, _ any) events.Decision {
		if n != events.ToolResult {
			return events.Decision{}
		}
		return events.Decision{
			Content: json.RawMessage(`[{"type":"text","text":"one"},{"type":"image","data":"QQ==","mimeType":"image/png"},{"type":"text","text":"two"}]`),
			Details: json.RawMessage(`{"k":1}`),
		}
	})
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	r := lastResults(s)[0]
	if r.Content != "one\ntwo" || len(r.Images) != 1 || r.Images[0].MimeType() != "image/png" {
		t.Errorf("result %+v", r)
	}
	for _, e := range evs {
		if e.Type == "tool_end" && string(e.Data["details"].(json.RawMessage)) != `{"k":1}` {
			t.Errorf("details %v", e.Data)
		}
	}
	if _, _, ok := contentFromPi(json.RawMessage(`not json`)); ok {
		t.Error("garbage content must be ignored")
	}
}

func TestInputHandledBlockedAndTransformed(t *testing.T) {
	text := "rewritten"
	cases := []struct {
		name    string
		d       events.Decision
		runs    bool
		handled *bool
		notice  string
		prompt  string
	}{
		{"handled", events.Decision{Handled: true}, false, ptr(true), "", ""},
		{"blocked", events.Decision{Block: true, Reason: "nope"}, false, ptr(true), "input blocked: nope", ""},
		{"transformed", events.Decision{Text: &text, Images: json.RawMessage(`[{"type":"image","data":"QQ==","mimeType":"image/png"},{"type":"x"}]`)}, true,
			ptr(false), "", "rewritten"},
		{"untouched", events.Decision{}, true, ptr(false), "", "orig"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &script{}
			a := newAgent(s)
			a.InputSource = SourceRPC
			var seen map[string]any
			a.Dispatch = disp(func(n string, p any) events.Decision {
				if n == events.Input {
					seen = p.(map[string]any)
					return c.d
				}
				return events.Decision{}
			})
			var got []bool
			a.OnInput = func(h bool) { got = append(got, h) }
			evs, err := collect(a, "orig")
			if err != nil {
				t.Fatal(err)
			}
			if seen["source"] != "rpc" || seen["text"] != "orig" {
				t.Errorf("payload %v", seen)
			}
			if len(got) != 1 || got[0] != *c.handled || (len(s.reqs) > 0) != c.runs {
				t.Errorf("OnInput %v, requests %d", got, len(s.reqs))
			}
			if c.runs && c.name == "transformed" {
				last := s.reqs[0].Messages[0]
				if last.Content[0].Text != c.prompt || len(last.Content) != 2 {
					t.Errorf("sent %+v", last)
				}
			}
			if c.notice != "" && (len(evs) == 0 || evs[0].Text != c.notice) {
				t.Errorf("events %v", evs)
			}
		})
	}
	a := newAgent(&script{})
	if p := a.inputPayload("x"); p["source"] != SourceInteractive {
		t.Errorf("default source %v", p)
	}
}

func ptr[T any](v T) *T { return &v }

func TestBeforeAgentStartReplacesThePromptForOneRunAndAddsAMessage(t *testing.T) {
	sys := "EXTENSION PROMPT"
	rec := &recorder{answer: func(n string, _ map[string]any) events.Decision {
		if n == events.BeforeAgentStart {
			return events.Decision{SystemPrompt: &sys, Message: &events.CustomMessage{CustomType: "note", Content: "extra", Display: true}}
		}
		return events.Decision{}
	}}
	s := &script{turns: [][]llm.Block{{use("1", "echo", `{}`)}}}
	a := newAgent(s)
	a.Dispatch = rec
	var custom []events.CustomMessage
	a.OnCustom = func(m events.CustomMessage) { custom = append(custom, m) }
	if _, err := collect(a, "hello"); err != nil {
		t.Fatal(err)
	}
	if rec.count(events.BeforeAgentStart) != 1 {
		t.Errorf("once per prompt, not per turn: %d", rec.count(events.BeforeAgentStart))
	}
	if len(s.reqs) != 2 || s.reqs[0].System != sys || s.reqs[1].System != sys {
		t.Errorf("systems: %q", s.reqs[0].System)
	}
	if len(custom) != 1 || custom[0].Content != "extra" {
		t.Errorf("custom %v", custom)
	}
	opts := rec.first(events.BeforeAgentStart)["systemPromptOptions"].(map[string]any)
	if opts["cwd"] != "/w" || opts["selectedTools"] == nil {
		t.Errorf("options %v", opts)
	}
	if _, err := collect(a, "again"); err != nil {
		t.Fatal(err)
	}
	if len(s.reqs) != 3 || s.reqs[2].System != sys { // the hook runs for each prompt
		t.Errorf("requests %d", len(s.reqs))
	}
	if a.sysOverride != nil {
		t.Error("the override must end with the run")
	}
	if n, tx, ok := cutLine("- name: some text"); !ok || n != "name" || tx != "some text" {
		t.Error("cutLine")
	}
}

func TestContinueAnswersAMessageAnExtensionAdded(t *testing.T) {
	s := &script{}
	a := newAgent(s)
	rec := &recorder{}
	a.Dispatch = rec
	a.AddCustom(events.CustomMessage{CustomType: "n", Content: "please look", Display: true})
	var stored int
	a.OnCustom = func(events.CustomMessage) { stored++ }
	a.AddCustom(events.CustomMessage{CustomType: "n", Content: "second"})
	evs := []Event{}
	if err := a.Continue(context.Background(), func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatal(err)
	}
	if stored != 1 || len(s.reqs) != 1 || len(s.reqs[0].Messages) != 2 || s.reqs[0].Messages[0].Content[0].Text != "please look" {
		t.Errorf("stored=%d requests=%+v", stored, s.reqs)
	}
	if evs[len(evs)-1].Type != "done" || rec.count(events.AgentEnd) != 1 || rec.count(events.Input) != 0 {
		t.Errorf("events %v, hooks %v", evs, rec.names)
	}
	if err := a.Continue(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestImagesFromPiKeepsOnlyWellFormedBlocks(t *testing.T) {
	got := imagesFromPi(json.RawMessage(`[{"type":"image","data":"QQ==","mimeType":"image/png"},{"type":"image","data":""},{"type":"text"}]`))
	if len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if imagesFromPi(json.RawMessage(`{`)) != nil {
		t.Error("garbage")
	}
	if len(piImages([]llm.Block{llm.ImageBlock("image/png", "QQ=="), txt("x")})) != 1 {
		t.Error("piImages")
	}
}
