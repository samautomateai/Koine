package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"koine/core/agent"
	"koine/core/events"
	"koine/core/ext"
	"koine/core/srcinfo"
	"koine/core/tools"
)

// wireUI connects what extensions can ask of the user interface, the conversation and the tool registry to the app. It runs whenever the
// host is created, before the extensions start, because ctx.mode and ctx.hasUI are told to them in the handshake.
func (a *App) wireUI() {
	h := a.Host
	h.Mode = a.opts.Mode
	if h.Mode == "" {
		h.Mode = "print"
	}
	h.HasUI = h.Mode == "tui" || h.Mode == "rpc"
	h.Dialog = a.dialog
	h.Surface = a.surface
	h.RunTool = a.runNested
	h.OnSendMessage = a.sendMessage
	h.OnError = a.extError
	h.OnShutdown = func() {
		if a.OnShutdown != nil {
			a.OnShutdown()
		}
	}
}

// extError reports an error an extension's handler raised (Pi's extension_error). The extension carries on.
func (a *App) extError(e ext.ExtError) {
	a.event(agent.Event{Type: "extension_error", Text: fmt.Sprintf("extension %s: %s: %s", e.Extension, e.Event, e.Error),
		Data: agent.Data("extensionPath", e.Extension, "event", e.Event, "error", e.Error)})
}

// sendMessage handles pi.sendMessage. Between runs the message goes into the conversation and the session at once, and starts a model
// turn if the extension asked for one. During a run it is delivered like any queued message: steering by default, follow-up on request;
// "nextTurn" holds it until the next prompt. A message queued during a run reaches the model as an ordinary user message.
func (a *App) sendMessage(m events.CustomMessage, o ext.SendOptions) {
	switch {
	case o.DeliverAs == "nextTurn":
		a.nextTurn = append(a.nextTurn, m)
	case a.Agent.Running():
		if o.DeliverAs == "followUp" {
			a.Agent.FollowUp(m.Content)
		} else {
			a.Agent.Steer(m.Content)
		}
	default:
		a.Agent.AddCustom(m)
		a.trigger = a.trigger || o.TriggerTurn
	}
}

// flushNextTurn adds the messages held for the next prompt to the conversation.
func (a *App) flushNextTurn() {
	held := a.nextTurn
	a.nextTurn = nil
	for _, m := range held {
		a.Agent.AddCustom(m)
	}
}

// persistCustom stores a custom message in the session and tells the frontend.
func (a *App) persistCustom(m events.CustomMessage) {
	if a.Session != nil {
		if err := a.Session.AppendCustomMessage(m.CustomType, m.Content, m.Display, m.Details); err != nil {
			a.Warnings = append(a.Warnings, "custom message not saved to the session: "+err.Error())
		}
	}
	a.event(agent.Event{Type: "custom_message", Text: m.Content,
		Data: agent.Data("customType", m.CustomType, "content", m.Content, "display", m.Display)})
}

// runNested runs a tool for an extension (ctx.executeTool): its arguments are checked and the tool_call hook may block it. Failures come
// back as error results, never as errors. Nested results do not enter the transcript.
func (a *App) runNested(ctx context.Context, name string, args json.RawMessage) (string, bool) {
	t, ok := a.Tools.Get(name)
	if !ok {
		return fmt.Sprintf("unknown tool %q", name), true
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	args, err := tools.Validate(t.Schema(), args)
	if err != nil {
		return fmt.Sprintf("Invalid arguments for tool %q:\n%v", name, err), true
	}
	if d := a.emitEvent(ctx, events.ToolCall, map[string]any{"toolName": name, "callId": "nested", "input": args}); d.Block {
		return "Blocked: " + d.Reason, true
	}
	out, err := t.Run(ctx, args)
	if err != nil && out == "" {
		out = err.Error()
	}
	return out, err != nil
}

// CommandInfo is a slash command as Pi's getCommands lists it.
type CommandInfo struct {
	Name        string       `json:"name"` // what to type after the slash; skills are "skill:<name>"
	Description string       `json:"description,omitempty"`
	Source      string       `json:"source"` // "extension", "prompt" or "skill"
	SourceInfo  srcinfo.Info `json:"sourceInfo"`
}

// CommandList lists the commands that run through a prompt: those of extensions, then prompt templates, then skills. The built-in commands
// of the interactive UI (/model, /tree ...) are not part of it, as in Pi.
func (a *App) CommandList() []CommandInfo {
	out := []CommandInfo{}
	for _, c := range a.Host.Commands() {
		out = append(out, CommandInfo{Name: c.Invocation, Description: c.Description, Source: "extension", SourceInfo: c.Source})
	}
	for _, p := range a.Prompts {
		out = append(out, CommandInfo{Name: p.Name, Description: p.Description, Source: "prompt", SourceInfo: p.Source})
	}
	for _, s := range a.Agent.Skills {
		out = append(out, CommandInfo{Name: "skill:" + s.Name, Description: s.Description, Source: "skill", SourceInfo: s.Source})
	}
	return out
}

// pushHostState gives the extensions the lists pi.getCommands() and ctx.tools answer from.
func (a *App) pushHostState() {
	type toolInfo struct {
		Name        string       `json:"name"`
		Description string       `json:"description"`
		SourceInfo  srcinfo.Info `json:"sourceInfo"`
	}
	names := a.Tools.Names()
	sort.Strings(names)
	ts := []toolInfo{}
	for _, n := range names {
		if t, ok := a.Tools.Get(n); ok {
			src, _ := a.Tools.SourceOf(n)
			ts = append(ts, toolInfo{n, t.Description(), src})
		}
	}
	a.Host.Broadcast("host_state", map[string]any{"commands": a.CommandList(), "tools": ts})
}

// CompleteCommand suggests arguments for an extension command typed so far ("/name arg…"). It returns nothing for other commands.
func (a *App) CompleteCommand(ctx context.Context, name, prefix string) []ext.CompletionItem {
	return a.Host.CompleteCommand(ctx, name, prefix)
}
