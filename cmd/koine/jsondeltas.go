package main

import (
	"encoding/json"
	"strings"
	"time"

	"koine/core/agent"
	"koine/core/llm"
)

// deltaState follows an assistant message whose text, thinking and tool-call arguments arrive as agent events (text_delta, thinking_delta,
// tool_delta) before the message itself. Each of them becomes the message_update of Pi's schema; the whole message still comes last, as
// message_end, and is the authoritative one. A provider that cannot stream sends no deltas, and the message is then reported as one
// start/delta/end triple per block (see assistant).
type deltaState struct {
	active bool   // message_start has been written for the response in progress
	kind   string // the open content block: text | thinking | toolcall, "" = none
	index  int    // contentIndex of the open block, or of the next one when none is open
	text   strings.Builder
	id     string
	name   string
}

// startLive writes message_start for a response that is about to stream, opening the turn if needed.
func (j *jsonStream) startLive() {
	if j.live.active {
		return
	}
	j.closeTurnIfPending()
	j.openTurn()
	provider, model := j.app.ProviderAndModel()
	j.put(rec("message_start", "message", startMessage(provider, model, time.Now().UnixMilli())))
	j.live = deltaState{active: true}
}

// startMessage is the empty assistant message of message_start.
func startMessage(provider, model string, now int64) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{}, "api": apiOf(provider), "provider": provider, "model": model,
		"usage": usageOf(llm.Message{}), "stopReason": "stop", "timestamp": now}
}

func (j *jsonStream) update(ev record) {
	j.put(rec("message_update", "usage", usageOf(llm.Message{}), "assistantMessageEvent", ev))
}

// delta reports one text_delta, thinking_delta or tool_delta event. A new kind of block (or a new tool call) closes the one before.
func (j *jsonStream) delta(e agent.Event) {
	j.startLive()
	l := &j.live
	kind := map[string]string{"text_delta": "text", "thinking_delta": "thinking", "tool_delta": "toolcall"}[e.Type]
	newCall := kind == "toolcall" && (l.kind != "toolcall" || l.id != e.CallID)
	if l.kind != kind || newCall {
		j.endBlock()
		l.kind, l.id, l.name = kind, e.CallID, e.Tool
		l.text.Reset()
		if kind == "toolcall" {
			j.update(rec("toolcall_start", "contentIndex", l.index, "id", e.CallID, "toolName", e.Tool))
		} else {
			j.update(rec(kind+"_start", "contentIndex", l.index))
		}
	}
	if e.Text == "" {
		return // the announcement of a tool call
	}
	l.text.WriteString(e.Text)
	j.update(rec(kind+"_delta", "contentIndex", l.index, "delta", e.Text))
}

// endBlock closes the open content block, if any.
func (j *jsonStream) endBlock() {
	l := &j.live
	if l.kind == "" {
		return
	}
	if l.kind == "toolcall" {
		args := json.RawMessage(l.text.String())
		if !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		call := map[string]any{"type": "toolCall", "id": l.id, "name": l.name, "arguments": args}
		j.update(rec("toolcall_end", "contentIndex", l.index, "toolCall", call))
	} else {
		j.update(rec(l.kind+"_end", "contentIndex", l.index, "content", l.text.String()))
	}
	l.kind = ""
	l.index++
}

// finishLive closes a streamed response: the open block, and the state for the next one. It reports whether there was one.
func (j *jsonStream) finishLive() bool {
	if !j.live.active {
		return false
	}
	j.endBlock()
	j.live = deltaState{}
	return true
}
