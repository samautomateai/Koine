package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"koine/core/compact"
	"koine/core/events"
	"koine/core/llm"
)

// TurnInfo is what PrepareNextTurn is told about the request that is about to be made.
type TurnInfo struct {
	Turn     int // 0 for the first request of a Run
	Model    string
	Thinking string        // the thinking level the request would use ("" = none)
	Messages []llm.Message // the conversation as it would be sent; do not modify
}

// TurnPlan is what PrepareNextTurn may change. Empty fields change nothing. A new model or level stays in force until changed again, as
// if the user had chosen it with /model or /thinking.
type TurnPlan struct {
	Model         string
	ThinkingLevel string
}

func userText(s string) llm.Message {
	return llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: s}}}
}

// Run executes one user prompt to completion, streaming events through emit. Messages queued with Steer or FollowUp are delivered as the
// run goes. When ctx is cancelled the turn in progress is closed off consistently (see abortTurn) and ctx's error is returned.
func (a *Agent) Run(ctx context.Context, prompt string, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}
	var mu sync.Mutex // tools run in parallel, and Steer reports from other goroutines: events reach emit one at a time
	inner := emit
	emit = func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		inner(e)
	}
	defer a.setActive(emit)()
	if a.Warm != nil {
		a.drainWarm() // refreshes sent while the agent was idle
		a.Warm.Begin()
		defer func() { a.drainWarm(); a.Warm.End() }()
	}
	if s, f := a.Queued(); len(s)+len(f) > 0 {
		emit(queueEvent(s, f)) // queued before the run began: the stream has not seen them yet
	}

	d := a.dispatch(ctx, events.Input, map[string]any{"text": prompt})
	if d.Block {
		emit(Event{Type: "notice", Text: "input blocked: " + d.Reason})
		return nil
	}
	if d.Text != nil {
		prompt = *d.Text
	}
	var user llm.Message
	if prompt = a.ExpandInput(prompt); prompt != "" {
		user = userText(prompt)
	}
	user.Role = "user"
	user.Content = append(user.Content, a.pendingImages...)
	a.pendingImages = nil
	a.add(user)
	defer a.dispatch(context.WithoutCancel(ctx), events.AgentEnd, map[string]any{})

	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}
	overflowed := false
	a.deliver(a.take(true, emit), emit) // steering typed before the run started goes out with the prompt
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			emit(Event{Type: "aborted"})
			return err
		}
		finished, err := a.turn(ctx, turn, &overflowed, emit)
		if err != nil {
			return err
		}
		if finished {
			emit(Event{Type: "done"})
			return nil
		}
	}
	emit(Event{Type: "notice", Text: fmt.Sprintf("stopped after %d turns", maxTurns)})
	emit(Event{Type: "done"})
	return nil
}

// turn makes one model request and runs the tool calls it asks for. It reports whether the run is over: the model asked for nothing
// more and no queued message is waiting, or every tool result asked to terminate.
func (a *Agent) turn(ctx context.Context, turn int, overflowed *bool, emit func(Event)) (finished bool, err error) {
	a.dispatch(ctx, events.TurnStart, map[string]any{"turn": turn})
	a.prepareTurn(ctx, turn)
	sys := a.systemPrompt(ctx)
	if a.needsCompaction(sys) {
		a.autoCompact(ctx, emit, ReasonThreshold)
	}
	thinking, maxTokens := a.thinkingFor()
	level := ""
	if thinking != nil {
		level = thinking.Level
	}
	req := llm.Request{Model: a.Model, System: sys, Messages: a.contextMessages(ctx, emit), MaxTokens: maxTokens, Thinking: thinking,
		Cache: llm.CacheHints{Key: a.CacheKey, Long: a.CacheLong}}
	for _, n := range a.Tools.Names() {
		t, _ := a.Tools.Get(n)
		req.Tools = append(req.Tools, llm.ToolDef{Name: t.Name(), Description: t.Description(), InputSchema: t.Schema()})
	}
	resp, err := a.complete(ctx, req, emit)
	if err != nil && ctx.Err() == nil && compact.IsOverflow(err) && !*overflowed && a.autoCompact(ctx, emit, ReasonOverflow) {
		*overflowed = true // one recovery per prompt
		req.Messages = a.contextMessages(ctx, emit)
		resp, err = a.complete(ctx, req, emit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return false, a.abortTurn(ctx, resp, level, emit)
		}
		emit(Event{Type: "error", Text: err.Error()})
		return false, err
	}
	msg := a.record(resp, emit)
	msg.ThinkingLevel = level
	a.add(msg)
	if a.Warm != nil {
		a.Warm.Observe(req, resp.Usage)
		a.drainWarm()
	}
	var uses []llm.Block
	for _, b := range resp.Content {
		switch b.Type {
		case "thinking":
			a.showThinking(b, emit)
		case "text":
			if b.Text != "" {
				emit(Event{Type: "text", Text: b.Text})
			}
		case "tool_use":
			uses = append(uses, b)
		}
	}
	a.dispatch(ctx, events.TurnEnd, map[string]any{"turn": turn})
	if len(uses) == 0 {
		// nothing more to do: a steering message or a follow-up starts another turn
		return !a.deliver(a.take(true, emit), emit) && !a.deliver(a.take(false, emit), emit), nil
	}
	results, terminate := a.execTools(ctx, uses, emit)
	a.add(llm.Message{Role: "user", Content: results})
	if ctx.Err() != nil {
		emit(Event{Type: "aborted"})
		return false, ctx.Err()
	}
	if terminate {
		return true, nil
	}
	a.deliver(a.take(true, emit), emit)
	return false, nil
}

// abortTurn closes off a model request that was cancelled. The text and thinking received so far are kept as an assistant message marked
// "aborted", which is saved but not sent to providers again. Tool calls that had not been fully received are dropped, so there is no
// tool_use without a tool_result.
func (a *Agent) abortTurn(ctx context.Context, resp *llm.Response, level string, emit func(Event)) error {
	if resp == nil {
		resp = &llm.Response{}
	}
	msg := a.record(resp, emit)
	msg.ThinkingLevel, msg.StopReason = level, "aborted"
	a.add(msg)
	for _, b := range resp.Content {
		switch b.Type {
		case "thinking":
			a.showThinking(b, emit)
		case "text":
			emit(Event{Type: "text", Text: b.Text})
		}
	}
	emit(Event{Type: "aborted"})
	return ctx.Err()
}

// prepareTurn runs the PrepareNextTurn hook.
func (a *Agent) prepareTurn(ctx context.Context, turn int) {
	if a.PrepareNextTurn == nil {
		return
	}
	p := a.PrepareNextTurn(ctx, TurnInfo{Turn: turn, Model: a.Model, Thinking: a.ThinkingLevel(), Messages: a.requestMessages(func(Event) {})})
	if p.Model != "" {
		a.Model = p.Model
	}
	if p.ThinkingLevel != "" {
		a.Thinking.Level = p.ThinkingLevel
	}
}

// contextMessages is the conversation for one request: the history without aborted messages and with every tool call answered, then
// whatever the context event and TransformContext make of it.
func (a *Agent) contextMessages(ctx context.Context, emit func(Event)) []llm.Message {
	msgs := a.requestMessages(emit)
	if a.Dispatch != nil {
		d := a.dispatch(ctx, events.Context, map[string]any{"messages": msgs})
		if len(d.Messages) > 0 {
			var out []llm.Message
			if err := json.Unmarshal(d.Messages, &out); err != nil {
				emit(Event{Type: "notice", Text: "context event ignored: " + err.Error()})
			} else {
				msgs = out
			}
		}
	}
	if a.TransformContext != nil {
		msgs = a.TransformContext(ctx, msgs)
	}
	return msgs
}

// requestMessages is the history as it is sent to a model. Aborted assistant messages are left out. A tool call whose result is missing
// (a session cut short by a crash) gets a synthetic error result, because providers reject a tool_use without its tool_result.
func (a *Agent) requestMessages(emit func(Event)) []llm.Message {
	hist := a.forModel(emit) // images the model cannot read or the settings forbid become a note
	out := make([]llm.Message, 0, len(hist))
	for _, m := range hist {
		if m.StopReason != "aborted" {
			out = append(out, m)
		}
	}
	for i := 0; i < len(out); i++ {
		if out[i].Role != "assistant" {
			continue
		}
		var missing []llm.Block
		answered := map[string]bool{}
		if i+1 < len(out) {
			for _, b := range out[i+1].Content {
				if b.Type == "tool_result" {
					answered[b.ToolUseID] = true
				}
			}
		}
		for _, b := range out[i].Content {
			if b.Type == "tool_use" && !answered[b.ID] {
				missing = append(missing, llm.Block{Type: "tool_result", ToolUseID: b.ID, Content: "Aborted: no result was recorded", IsError: true})
			}
		}
		if len(missing) == 0 {
			continue
		}
		if i+1 < len(out) && len(answered) > 0 {
			next := out[i+1]
			next.Content = append(append([]llm.Block(nil), next.Content...), missing...)
			out[i+1] = next
		} else {
			out = append(out[:i+1], append([]llm.Message{{Role: "user", Content: missing}}, out[i+1:]...)...)
		}
	}
	return out
}
