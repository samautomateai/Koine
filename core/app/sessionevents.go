package app

import (
	"context"
	"time"

	"koine/core/events"
)

// Session lifecycle events for extensions (REQ-EXT-007). The session_before_* ones are gates: an extension that answers cancel stops the
// operation, which the caller reports as "cancelled by an extension".

// emit sends an event to hooks and extensions, or does nothing before they are set up.
func (a *App) emitEvent(ctx context.Context, name string, payload map[string]any) events.Decision {
	if a.Agent == nil || a.Agent.Dispatch == nil {
		return events.Decision{}
	}
	return a.Agent.Dispatch.Emit(ctx, name, payload)
}

// gate sends a session_before_* event and reports whether an extension cancelled the operation.
func (a *App) gate(ctx context.Context, name string, payload map[string]any) bool {
	return a.emitEvent(ctx, name, payload).Cancel
}

// sessionFile is the file of the current session ("" without one).
func (a *App) sessionFile() string {
	if a.Session == nil {
		return ""
	}
	return a.Session.Path
}

// leaving tells extensions that the session is about to be torn down: reason is quit, reload, new, resume or fork, and target the
// session file that replaces it, if any.
func (a *App) leaving(ctx context.Context, reason, target string) {
	p := map[string]any{"reason": reason}
	if target != "" {
		p["targetSessionFile"] = target
	}
	a.emitEvent(ctx, events.SessionShutdown, p)
}

// arrived tells extensions that a session started after a switch: reason is new, resume or fork, previous the file it came from.
func (a *App) arrived(ctx context.Context, reason, previous string) {
	p := map[string]any{"cwd": a.Cwd, "reason": reason}
	if previous != "" {
		p["previousSessionFile"] = previous
	}
	a.emitEvent(ctx, events.SessionStart, p)
}

// shutdownContext bounds how long extensions get to react to the end of the process.
func shutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}
