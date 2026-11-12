package app

import (
	"encoding/json"

	"koine/core/agent"
	"koine/core/session"
)

// event reports an event to whoever runs the current input (HandleInput's emit). It does nothing outside HandleInput, for example while the
// app starts. Extension callbacks reach it too: the host handles an extension's notification before it answers the call the agent is
// waiting on, so the agent's goroutine is idle then and emit is never called from two goroutines at once.
func (a *App) event(e agent.Event) {
	if a.emit != nil {
		a.emit(e)
	}
}

// setSession makes s the current session and reports each entry written to it as an entry_appended event.
func (a *App) setSession(s *session.Session) {
	a.Session = s
	a.Agent.CacheKey = ""
	if s != nil {
		s.OnAppend = a.entryAppended
		a.Agent.CacheKey = s.ID
	}
}

func (a *App) entryAppended(e session.Entry) {
	a.event(agent.Event{Type: agent.EvEntryAppended, Data: agent.Data("entry", e.JSON())})
}

// keptEntryID is the id of the first session entry the latest compaction kept: OnCompact wrote the compaction entry just before.
func (a *App) keptEntryID() string {
	if a.Session == nil {
		return ""
	}
	if e, ok := a.Session.Entry(a.Session.Leaf()); ok && e.Type == "compaction" {
		return e.FirstKeptEntryID
	}
	return ""
}

// thinkingChanged records an explicit change of the thinking level (entry_appended) and then reports it.
func (a *App) thinkingChanged(level string) {
	if a.Session != nil {
		if err := a.Session.RecordThinkingLevel(level); err != nil {
			a.Warnings = append(a.Warnings, "thinking level not saved to the session: "+err.Error())
		}
	}
	a.event(agent.Event{Type: agent.EvThinkingChanged, Data: agent.Data("level", level)})
}

// nameChanged reports a new session name, after SetName wrote its entry; an empty name means it was cleared and is left out, as Pi does.
func (a *App) nameChanged(name string) {
	d := agent.Data()
	if name != "" {
		d["name"] = name
	}
	a.event(agent.Event{Type: agent.EvSessionInfoChanged, Data: d})
}

// wireHost connects the extension host to the agent and the session. It runs whenever the host is created.
func (a *App) wireHost() {
	a.Host.OnUserMessage = func(text, deliverAs string) {
		if deliverAs == "steer" {
			a.Agent.Steer(text)
		} else {
			a.Agent.FollowUp(text)
		}
	}
	a.Host.OnAppendEntry = func(customType string, data json.RawMessage) {
		if a.Session == nil {
			return
		}
		if err := a.Session.AppendCustom(customType, data); err != nil {
			a.Warnings = append(a.Warnings, "extension entry not saved to the session: "+err.Error())
		}
	}
}
