// Package events defines the neutral event/decision vocabulary that every
// extension mechanism (JS sidecar, command hooks, out-of-process extensions in
// any language) maps onto.
package events

import (
	"context"
	"encoding/json"
)

// Canonical event names.
const (
	SessionStart = "session_start"
	Input        = "input"         // user prompt submitted; may block or rewrite text
	SystemPrompt = "system_prompt" // may append to the system prompt
	TurnStart    = "turn_start"
	TurnEnd      = "turn_end"
	ToolCall     = "tool_call"   // before a tool runs; may block or rewrite args
	ToolResult   = "tool_result" // after a tool runs; may rewrite output
	AgentEnd     = "agent_end"
)

// Decision is what a handler may answer. The zero value means "no opinion".
type Decision struct {
	Block        bool            `json:"block,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Args         json.RawMessage `json:"args,omitempty"`   // replaces tool input
	Output       *string         `json:"output,omitempty"` // replaces tool output
	Text         *string         `json:"text,omitempty"`   // replaces user input
	SystemAppend string          `json:"systemAppend,omitempty"`
}

func (d *Decision) Merge(o Decision) {
	if o.Block {
		d.Block = true
		if o.Reason != "" {
			d.Reason = o.Reason
		}
	}
	if len(o.Args) > 0 {
		d.Args = o.Args
	}
	if o.Output != nil {
		d.Output = o.Output
	}
	if o.Text != nil {
		d.Text = o.Text
	}
	if o.SystemAppend != "" {
		if d.SystemAppend != "" {
			d.SystemAppend += "\n"
		}
		d.SystemAppend += o.SystemAppend
	}
}

// Dispatcher receives events. payload is any JSON-marshalable value.
type Dispatcher interface {
	Emit(ctx context.Context, name string, payload any) Decision
}

// Multi fans an event out to several dispatchers in order, stopping at the
// first block.
type Multi []Dispatcher

func (m Multi) Emit(ctx context.Context, name string, payload any) Decision {
	var out Decision
	for _, d := range m {
		if d == nil {
			continue
		}
		out.Merge(d.Emit(ctx, name, payload))
		if out.Block {
			return out
		}
	}
	return out
}

// Nop ignores everything.
type Nop struct{}

func (Nop) Emit(context.Context, string, any) Decision { return Decision{} }
