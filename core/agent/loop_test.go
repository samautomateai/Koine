package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"koine/core/events"
	"koine/core/llm"
	"koine/core/tools"
)

// streamer is a script that also streams: each turn's blocks are announced as deltas first.
type streamer struct {
	script
}

func (s *streamer) Stream(ctx context.Context, r llm.Request, fn func(llm.StreamEvent)) (*llm.Response, error) {
	resp, err := s.Complete(ctx, r)
	if err != nil {
		return nil, err
	}
	for _, b := range resp.Content {
		switch b.Type {
		case "thinking":
			fn(llm.StreamEvent{Type: llm.EvThinkingDelta, Delta: b.Thinking})
		case "text":
			fn(llm.StreamEvent{Type: llm.EvTextDelta, Delta: b.Text[:1]})
			fn(llm.StreamEvent{Type: llm.EvTextDelta, Delta: b.Text[1:]})
		case "tool_use":
			fn(llm.StreamEvent{Type: llm.EvToolStart, ID: b.ID, Name: b.Name})
			fn(llm.StreamEvent{Type: llm.EvToolDelta, ID: b.ID, Name: b.Name, Delta: string(b.Input)})
		}
	}
	return resp, nil
}

func TestStreamingProviderEmitsDeltasAndStillEmitsTheFinalBlocks(t *testing.T) {
	s := &streamer{script{turns: [][]llm.Block{
		{{Type: "thinking", Thinking: "hm"}, txt("Hello"), use("1", "echo", `{"a":1}`)},
		{txt("done")},
	}}}
	a := newAgent(s)
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	want := "thinking_delta,text_delta,text_delta,tool_delta,tool_delta,thinking,text,tool_start,tool_end,text_delta,text_delta,text,done"
	if got := types(evs); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
	var tool Event
	for _, e := range evs {
		if e.Type == "tool_delta" && e.Text != "" {
			tool = e
		}
	}
	if tool.CallID != "1" || tool.Tool != "echo" || tool.Text != `{"a":1}` {
		t.Errorf("tool delta: %+v", tool)
	}
	// thinking_delta is hidden along with thinking
	s.i = 0
	a.HideThinking = true
	evs, _ = collect(a, "again")
	if strings.Contains(types(evs), "thinking") {
		t.Errorf("hidden thinking leaked: %s", types(evs))
	}
	// a provider that cannot stream produces no deltas and the same final events
	plain := newAgent(&script{turns: [][]llm.Block{{txt("Hello")}}})
	evs, _ = collect(plain, "go")
	if got := types(evs); got != "text,done" {
		t.Errorf("plain: %s", got)
	}
}

// hang streams one piece of text and then waits for the context, like a model that is still talking when the user interrupts.
type hang struct{ script }

func (h *hang) Stream(ctx context.Context, r llm.Request, fn func(llm.StreamEvent)) (*llm.Response, error) {
	h.reqs = append(h.reqs, r)
	if len(h.reqs) > 1 {
		return &llm.Response{Content: []llm.Block{txt("fresh")}}, nil
	}
	fn(llm.StreamEvent{Type: llm.EvTextDelta, Delta: "part"})
	<-ctx.Done()
	return &llm.Response{Content: []llm.Block{txt("part")}, Usage: llm.Usage{Input: 5}}, ctx.Err()
}

func TestAbortDuringTheResponseKeepsAnAbortedMessageThatIsNotSentAgain(t *testing.T) {
	h := &hang{}
	a := newAgent(h)
	var persisted []llm.Message
	a.Persist = func(m llm.Message) { persisted = append(persisted, m) }
	ctx, cancel := context.WithCancel(context.Background())
	var evs []Event
	err := a.Run(ctx, "go", func(e Event) {
		evs = append(evs, e)
		if e.Type == "text_delta" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if got := types(evs); got != "text_delta,usage,text,aborted" {
		t.Errorf("events: %s", got)
	}
	last := a.History[len(a.History)-1]
	if last.Role != "assistant" || last.StopReason != "aborted" || last.Content[0].Text != "part" || len(persisted) != 2 {
		t.Fatalf("history: %+v persisted %d", a.History, len(persisted))
	}
	// the next prompt does not carry the aborted message to the provider
	if _, err := collect(a, "again"); err != nil {
		t.Fatal(err)
	}
	for _, m := range h.reqs[1].Messages {
		if m.Role == "assistant" {
			t.Errorf("aborted message was sent: %+v", m)
		}
	}
	if len(a.History) != 4 || a.History[3].Content[0].Text != "fresh" {
		t.Errorf("history after: %d", len(a.History))
	}
}

func TestAbortDuringToolsAnswersEveryToolCall(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("1", "slow", `{}`), use("2", "echo", `{}`), use("3", "nope", `{}`)}}}
	a := newAgent(s)
	ctx, cancel := context.WithCancel(context.Background())
	a.Tools.Register(&tools.Func{N: "slow", D: "d", S: json.RawMessage(`{}`), F: func(ctx context.Context, _ json.RawMessage) (string, error) {
		cancel()
		<-ctx.Done()
		return "", ctx.Err()
	}})
	a.ToolExecution = ExecSequential // so the echo call has not started when the first one is cancelled
	var evs []Event
	err := a.Run(ctx, "go", func(e Event) { evs = append(evs, e) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	res := a.History[len(a.History)-1].Content
	if len(res) != 3 || res[0].ToolUseID != "1" || res[1].ToolUseID != "2" || res[2].ToolUseID != "3" {
		t.Fatalf("results: %+v", res)
	}
	for _, r := range res {
		if !r.IsError {
			t.Errorf("result should be an error: %+v", r)
		}
	}
	if res[1].Content != "aborted" || types(evs[len(evs)-1:]) != "aborted" {
		t.Errorf("unstarted call: %+v, last event %s", res[1], types(evs[len(evs)-1:]))
	}
	// cancelled before the checks: calls are answered without being announced as run
	a2 := newAgent(&script{turns: [][]llm.Block{{use("1", "echo", `{}`)}}})
	ctx2, cancel2 := context.WithCancel(context.Background())
	a2.Dispatch = disp(func(n string, p any) events.Decision {
		if n == events.TurnEnd {
			cancel2()
		}
		return events.Decision{}
	})
	if err := a2.Run(ctx2, "go", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r := a2.History[len(a2.History)-1].Content[0]; r.Content != "aborted" || !r.IsError {
		t.Errorf("%+v", r)
	}
}

func TestRequestMessagesAnswersOrphanedToolCalls(t *testing.T) {
	a := newAgent(&script{})
	user := func(s string) llm.Message { return userText(s) }
	asst := func(bs ...llm.Block) llm.Message { return llm.Message{Role: "assistant", Content: bs} }
	// 1: the assistant asked for two tools and only one was answered; 2: the last assistant message has no answer at all
	a.History = []llm.Message{user("a"), asst(use("1", "echo", `{}`), use("2", "echo", `{}`)),
		{Role: "user", Content: []llm.Block{{Type: "tool_result", ToolUseID: "1", Content: "ok"}}},
		asst(use("3", "echo", `{}`)), user("later")}
	got := a.requestMessages(func(Event) {})
	if len(got) != 6 {
		t.Fatalf("messages: %+v", got)
	}
	if r := got[2].Content; len(r) != 2 || r[1].ToolUseID != "2" || !r[1].IsError {
		t.Errorf("partial answer: %+v", r)
	}
	if r := got[4]; r.Role != "user" || len(r.Content) != 1 || r.Content[0].ToolUseID != "3" || got[5].Content[0].Text != "later" {
		t.Errorf("missing answer: %+v", got[4:])
	}
	if len(a.History) != 5 {
		t.Error("the history itself must not change")
	}
}

func TestContextEventTransformContextAndPrepareNextTurn(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("1", "echo", `{}`)}, {txt("end")}}}
	a := newAgent(s)
	a.Model = "m1"
	replacement, _ := json.Marshal([]llm.Message{userText("replaced")})
	mode := "ok"
	a.Dispatch = disp(func(n string, p any) events.Decision {
		if n != events.Context {
			return events.Decision{}
		}
		if _, ok := p.(map[string]any)["messages"].([]llm.Message); !ok {
			t.Errorf("payload: %T", p)
		}
		if mode == "bad" {
			return events.Decision{Messages: json.RawMessage(`{"nope"`)}
		}
		return events.Decision{Messages: replacement}
	})
	var seenTurns []int
	a.PrepareNextTurn = func(_ context.Context, ti TurnInfo) TurnPlan {
		seenTurns = append(seenTurns, ti.Turn)
		if ti.Turn == 1 {
			if ti.Model != "m1" || len(ti.Messages) != 3 {
				t.Errorf("turn info: %+v", ti)
			}
			return TurnPlan{Model: "m2", ThinkingLevel: "high"}
		}
		return TurnPlan{}
	}
	a.TransformContext = func(_ context.Context, m []llm.Message) []llm.Message {
		return append(m, userText("extra"))
	}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if m := s.reqs[0].Messages; len(m) != 2 || m[0].Content[0].Text != "replaced" || m[1].Content[0].Text != "extra" {
		t.Errorf("request 0: %+v", m)
	}
	if s.reqs[0].Model != "m1" || s.reqs[1].Model != "m2" || a.Model != "m2" || a.Thinking.Level != "high" || len(seenTurns) != 2 {
		t.Errorf("models %s %s, level %q, turns %v", s.reqs[0].Model, s.reqs[1].Model, a.Thinking.Level, seenTurns)
	}
	if len(a.History) != 4 || a.History[0].Content[0].Text != "go" {
		t.Errorf("history must be untouched: %+v", a.History)
	}
	// a replacement that does not decode is ignored with a notice
	mode = "bad"
	a.TransformContext, a.PrepareNextTurn = nil, nil
	evs, _ := collect(a, "again")
	if !strings.Contains(types(evs), "notice") || len(s.reqs[len(s.reqs)-1].Messages) < 5 {
		t.Errorf("bad replacement: %s", types(evs))
	}
}

func TestRunDeliversQueuedMessagesAsTheLoopGoes(t *testing.T) {
	// the model asks for a tool, a steering message is typed while it runs, and a follow-up waits for the end
	s := &script{turns: [][]llm.Block{{use("1", "echo", `{}`)}, {txt("one")}, {txt("two")}, {txt("three")}}}
	a := newAgent(s)
	a.Tools.Register(&tools.Func{N: "type", D: "d", S: json.RawMessage(`{}`), F: func(context.Context, json.RawMessage) (string, error) {
		a.Steer("steer 1")
		a.Steer("steer 2")
		a.FollowUp("follow 1")
		a.FollowUp("follow 2")
		return "typed", nil
	}})
	s.turns[0] = []llm.Block{use("1", "type", `{}`)}
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	// one at a time: steer 1 after the tools, steer 2 after the next assistant turn, then the follow-ups one by one
	var order []string
	for _, e := range evs {
		if e.Type == "user_message" {
			order = append(order, e.Text)
		}
	}
	if got := strings.Join(order, ","); got != "steer 1,steer 2,follow 1,follow 2" {
		t.Fatalf("delivery order %s (events %s)", got, types(evs))
	}
	if len(s.reqs) != 5 {
		t.Fatalf("requests: %d", len(s.reqs))
	}
	// the steering message follows the tool results and is in the next request
	m := s.reqs[1].Messages
	if m[len(m)-2].Content[0].Type != "tool_result" || m[len(m)-1].Content[0].Text != "steer 1" {
		t.Errorf("request 1: %+v", m)
	}
	if st, fu := a.Queued(); len(st) != 0 || len(fu) != 0 {
		t.Errorf("queues left: %v %v", st, fu)
	}
	last := lastQueueUpdate(evs)
	if last == nil || len(last.Data["steering"].([]string)) != 0 || len(last.Data["followUp"].([]string)) != 0 {
		t.Errorf("final queue update: %+v", last)
	}
}

func lastQueueUpdate(evs []Event) *Event {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == "queue_update" {
			return &evs[i]
		}
	}
	return nil
}

func TestQueueModesAll(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("1", "echo", `{}`)}, {txt("one")}, {txt("two")}}}
	a := newAgent(s)
	a.SteeringMode, a.FollowUpMode = ModeAll, ModeAll
	a.Tools.Register(&tools.Func{N: "type", D: "d", S: json.RawMessage(`{}`), F: func(context.Context, json.RawMessage) (string, error) {
		a.Steer("s1")
		a.Steer("s2")
		a.FollowUp("f1")
		a.FollowUp("f2")
		return "typed", nil
	}})
	s.turns[0] = []llm.Block{use("1", "type", `{}`)}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if len(s.reqs) != 3 {
		t.Fatalf("requests: %d", len(s.reqs))
	}
	m := s.reqs[1].Messages // go, tool_use, tool_result, s1, s2
	if len(m) != 5 || m[3].Content[0].Text != "s1" || m[4].Content[0].Text != "s2" {
		t.Errorf("steering all at once: %+v", m)
	}
	m = s.reqs[2].Messages // ..., one, f1, f2
	if len(m) != 8 || m[6].Content[0].Text != "f1" || m[7].Content[0].Text != "f2" {
		t.Errorf("follow-ups all at once: %d %+v", len(m), m[len(m)-2:])
	}
}

func TestFollowUpsWaitForSteeringAndQueuedTextIsExpanded(t *testing.T) {
	s := &script{turns: [][]llm.Block{{txt("one")}, {txt("two")}, {txt("three")}}}
	a := newAgent(s)
	// typed while idle: both go out around the first request, steering ahead of the follow-up
	a.FollowUp("later")
	a.Steer("now")
	steer, follow := a.Queued()
	if len(steer) != 1 || len(follow) != 1 {
		t.Fatalf("queued: %v %v", steer, follow)
	}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if m := s.reqs[0].Messages; len(m) != 2 || m[1].Content[0].Text != "now" {
		t.Errorf("first request: %+v", m)
	}
	if m := s.reqs[1].Messages; m[len(m)-1].Content[0].Text != "later" {
		t.Errorf("second request: %+v", m)
	}
	// ClearQueues hands the text back
	a.Steer("s")
	a.FollowUp("f")
	st, fu := a.ClearQueues()
	if len(st) != 1 || len(fu) != 1 {
		t.Errorf("cleared: %v %v", st, fu)
	}
	if st, fu = a.Queued(); len(st)+len(fu) != 0 {
		t.Error("queues not empty")
	}
}

func TestSteerReportsQueueChangesOfTheRunInProgress(t *testing.T) {
	a := newAgent(&script{})
	var mu sync.Mutex
	var seen []Event
	started := make(chan struct{})
	release := make(chan struct{})
	a.Tools.Register(&tools.Func{N: "wait", D: "d", S: json.RawMessage(`{}`), F: func(context.Context, json.RawMessage) (string, error) {
		close(started)
		<-release
		return "ok", nil
	}})
	a.Provider = &script{turns: [][]llm.Block{{use("1", "wait", `{}`)}}}
	done := make(chan error, 1)
	go func() {
		done <- a.Run(context.Background(), "go", func(e Event) { mu.Lock(); seen = append(seen, e); mu.Unlock() })
	}()
	<-started
	a.Steer("hey")
	a.FollowUp("later")
	st, fu := a.ClearQueues()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	var q []Event
	for _, e := range seen {
		if e.Type == "queue_update" {
			q = append(q, e)
		}
	}
	if len(q) != 3 || q[0].Data["steering"].([]string)[0] != "hey" || q[1].Data["followUp"].([]string)[0] != "later" ||
		len(q[2].Data["steering"].([]string)) != 0 || len(st) != 1 || len(fu) != 1 {
		t.Errorf("queue updates: %+v", q)
	}
}
