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

	// BeforeAgentStart is sent once per prompt, after the input is accepted and before the first model request (Pi's before_agent_start).
	// Handlers may answer with Decision.SystemPrompt and Decision.Message.
	BeforeAgentStart = "before_agent_start"
	// Session lifecycle (REQ-EXT-007). The session_before_* events are gates: a handler that answers Decision.Cancel stops the operation.
	SessionBeforeSwitch  = "session_before_switch"  // {reason: "new"|"resume", targetSessionFile?}
	SessionBeforeFork    = "session_before_fork"    // {entryId, position: "before"|"at"}
	SessionBeforeCompact = "session_before_compact" // {reason: "manual"|"threshold"|"overflow", customInstructions, willRetry}
	SessionCompact       = "session_compact"        // {compactionEntry, fromExtension: false, reason, willRetry}
	SessionBeforeTree    = "session_before_tree"    // {preparation: {targetId, oldLeafId, commonAncestorId, userWantsSummary, ...}}
	SessionTree          = "session_tree"           // {newLeafId, oldLeafId, fromExtension: false}
	SessionShutdown      = "session_shutdown"       // {reason: "quit"|"reload"|"new"|"resume"|"fork", targetSessionFile?}
	// ToolExecutionStart, ToolExecutionUpdate and ToolExecutionEnd are Pi's tool progress events, observed by extensions.
	ToolExecutionStart  = "tool_execution_start"
	ToolExecutionUpdate = "tool_execution_update"
	ToolExecutionEnd    = "tool_execution_end"
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

	// Cancel answers a session_before_* gate: the operation does not happen. Like Block it ends the chain of handlers.
	Cancel bool `json:"cancel,omitempty"`
	// Handled answers the input event: an extension dealt with the prompt, so no agent run starts and nothing is reported as blocked.
	Handled bool `json:"handled,omitempty"`
	// Content replaces the whole result of a tool (tool_result): Pi content blocks, [{type:"text",text}, {type:"image",data,mimeType}].
	// Output stays the text part of it. Details replaces the tool's details.
	Content json.RawMessage `json:"content,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
	// Images replaces the images that come with a prompt (input transform): Pi image blocks.
	Images json.RawMessage `json:"images,omitempty"`
	// SystemPrompt replaces the whole system prompt for the run (before_agent_start). Message is a custom message to add to the
	// conversation before the first model request.
	SystemPrompt *string        `json:"systemPrompt,omitempty"`
	Message      *CustomMessage `json:"message,omitempty"`
}

// CustomMessage is a message an extension adds to the conversation (Pi's custom message): it reaches the model as a user message
// and is stored in the session as a custom_message entry. Display says whether frontends show it.
type CustomMessage struct {
	CustomType string          `json:"customType"`
	Content    string          `json:"content"`
	Display    bool            `json:"display"`
	Details    json.RawMessage `json:"details,omitempty"`
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
	if o.Cancel {
		d.Cancel = true
		if o.Reason != "" {
			d.Reason = o.Reason
		}
	}
	if o.Handled {
		d.Handled, d.Block = true, true
	}
	if len(o.Content) > 0 {
		d.Content = o.Content
	}
	if len(o.Details) > 0 {
		d.Details = o.Details
	}
	if len(o.Images) > 0 {
		d.Images = o.Images
	}
	if o.SystemPrompt != nil {
		d.SystemPrompt = o.SystemPrompt
	}
	if o.Message != nil {
		d.Message = o.Message
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
		if out.Block || out.Cancel {
			return out
		}
	}
	return out
}

// Nop ignores everything.
type Nop struct{}

func (Nop) Emit(context.Context, string, any) Decision { return Decision{} }
