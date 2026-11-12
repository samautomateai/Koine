package app

import (
	"strings"

	"koine/core/agent"
)

// ExpandTemplate turns a prompt-template command line ("/review src/") into the prompt it stands for. Any other line comes back as it
// was, with ok false.
func (a *App) ExpandTemplate(line string) (string, bool) {
	name, args, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "/"), " ")
	if !strings.HasPrefix(strings.TrimSpace(line), "/") {
		return line, false
	}
	for _, p := range a.Prompts {
		if p.Name == name {
			return p.Expand(strings.TrimSpace(args)), true
		}
	}
	return line, false
}

// IsExtensionCommand reports whether line is a slash command an extension registered (it runs even while the agent is busy).
func (a *App) IsExtensionCommand(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false
	}
	name, _, _ := strings.Cut(line[1:], " ")
	for _, c := range a.Host.Commands() {
		if c.Invocation == name {
			return true
		}
	}
	return false
}

// SetInputSource says where prompts come from, for the input event: "interactive" (default), "rpc" or "extension".
func (a *App) SetInputSource(source string) { a.Agent.InputSource = source }

// SetInputHook is called when a prompt's input event is settled; handled says that no run follows (see agent.Agent.OnInput).
func (a *App) SetInputHook(f func(handled bool)) { a.Agent.OnInput = f }

// SetEventSink installs the receiver of events raised while no input is being handled: an extension error, a custom message or a
// notification that arrives while the agent is idle.
func (a *App) SetEventSink(f func(agent.Event)) { a.sink = f }
