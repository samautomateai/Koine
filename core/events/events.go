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
	Context      = "context"     // before each model request; may replace the messages (Koine's message shape, not Pi's)
	AgentEnd     = "agent_end"
	// ResourcesDiscover is sent after session_start (reason "startup") and after a reload (reason "reload"); handlers answer with
	// Decision.Resources, paths of skills, prompt templates and themes to load.
	ResourcesDiscover = "resources_discover"
)

// Resources are the paths an extension contributes in answer to ResourcesDiscover (Pi's skillPaths, promptPaths and themePaths).
type Resources struct {
	SkillPaths  []string `json:"skillPaths,omitempty"`
	PromptPaths []string `json:"promptPaths,omitempty"`
	ThemePaths  []string `json:"themePaths,omitempty"`
}

// Empty reports whether no path was contributed.
func (r *Resources) Empty() bool {
	return r == nil || len(r.SkillPaths)+len(r.PromptPaths)+len(r.ThemePaths) == 0
}

// Decision is what a handler may answer. The zero value means "no opinion".
type Decision struct {
	Block   bool            `json:"block,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`    // replaces tool input
	Output  *string         `json:"output,omitempty"`  // replaces tool output
	Text    *string         `json:"text,omitempty"`    // replaces user input
	IsError *bool           `json:"isError,omitempty"` // tool_result: overrides whether the result is an error
	// Terminate asks the agent to stop after this batch of tool calls, unless a sibling result disagrees. It is read on a blocked
	// tool_call and on tool_result.
	Terminate bool `json:"terminate,omitempty"`
	// Messages replaces the conversation for one request (context event): a JSON array of messages as llm.Message encodes them.
	Messages     json.RawMessage `json:"messages,omitempty"`
	SystemAppend string          `json:"systemAppend,omitempty"`
	Resources    *Resources      `json:"resources,omitempty"` // answer to ResourcesDiscover; paths from several handlers add up
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
	if o.IsError != nil {
		d.IsError = o.IsError
	}
	if o.Terminate {
		d.Terminate = true
	}
	if len(o.Messages) > 0 {
		d.Messages = o.Messages
	}
	if !o.Resources.Empty() {
		if d.Resources == nil {
			d.Resources = &Resources{}
		}
		d.Resources.SkillPaths = append(d.Resources.SkillPaths, o.Resources.SkillPaths...)
		d.Resources.PromptPaths = append(d.Resources.PromptPaths, o.Resources.PromptPaths...)
		d.Resources.ThemePaths = append(d.Resources.ThemePaths, o.Resources.ThemePaths...)
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
