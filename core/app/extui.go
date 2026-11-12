package app

import (
	"context"
	"sort"
	"strings"
	"sync"

	"koine/core/agent"
	"koine/core/ext"
)

// extUI is the text-level UI state extensions set through ctx.ui: status entries, widgets, the terminal title and the working message.
// Frontends draw it from Surfaces; RPC clients receive each change as a request instead (OnSurface).
type extUI struct {
	mu       sync.Mutex
	statuses map[string]string
	widgets  map[string]widget
	title    string
	working  string
}

type widget struct {
	lines []string
	below bool
}

// Surfaces is a snapshot of the extension-set UI state for a frontend to draw.
type Surfaces struct {
	Statuses []string // "text" of each status, in key order
	Above    []string // widget lines above the editor, widgets in key order
	Below    []string // widget lines below the editor
	Title    string   // terminal title; empty = leave alone
	Working  string   // replaces the "Working…" message while the agent runs; empty = the default
}

// Surfaces returns the current extension-set UI state.
func (a *App) Surfaces() Surfaces {
	u := &a.extui
	u.mu.Lock()
	defer u.mu.Unlock()
	var s Surfaces
	keys := make([]string, 0, len(u.statuses))
	for k := range u.statuses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.Statuses = append(s.Statuses, u.statuses[k])
	}
	keys = keys[:0]
	for k := range u.widgets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if w := u.widgets[k]; w.below {
			s.Below = append(s.Below, w.lines...)
		} else {
			s.Above = append(s.Above, w.lines...)
		}
	}
	s.Title, s.Working = u.title, u.working
	return s
}

// surface applies a fire-and-forget request from an extension.
func (a *App) surface(r ext.UIRequest) {
	u := &a.extui
	changed := true
	u.mu.Lock()
	switch r.Method {
	case "setStatus":
		if u.statuses == nil {
			u.statuses = map[string]string{}
		}
		if r.StatusText == nil || *r.StatusText == "" {
			delete(u.statuses, r.StatusKey)
		} else {
			u.statuses[r.StatusKey] = *r.StatusText
		}
	case "setWidget":
		if u.widgets == nil {
			u.widgets = map[string]widget{}
		}
		if len(r.WidgetLines) == 0 {
			delete(u.widgets, r.WidgetKey)
		} else {
			u.widgets[r.WidgetKey] = widget{lines: r.WidgetLines, below: r.WidgetPlacement == "belowEditor"}
		}
	case "setTitle":
		u.title = r.Title
	case "setWorkingMessage":
		u.working = r.Message
	default:
		changed = false
	}
	u.mu.Unlock()
	switch r.Method {
	case "notify":
		a.notifyUser(r)
	case "set_editor_text":
		if a.PrefillEditor != nil {
			a.PrefillEditor(r.Text)
		}
	}
	if a.OnSurface != nil {
		a.OnSurface(r)
	}
	if changed && a.UIChanged != nil {
		a.UIChanged()
	}
}

// notifyUser shows ctx.ui.notify text: in the transcript during a run, otherwise with the startup messages. Frontends that pass
// requests on as they are (RPC) set OnSurface and show nothing here.
func (a *App) notifyUser(r ext.UIRequest) {
	if a.OnSurface != nil {
		return
	}
	text := r.Message
	switch r.NotifyType {
	case "error":
		text = "error: " + text
	case "warning":
		text = "warning: " + text
	}
	if a.emit != nil {
		a.emit(agent.Event{Type: "notice", Text: text})
		return
	}
	a.Warnings = append(a.Warnings, r.Extension+": "+r.Message)
}

// dialog answers select, confirm, input and editor requests with the frontend's own prompts (Choose and Prompt). A frontend that
// cannot ask (print and json runs) never sets hasUI, so extensions do not get here; one without Choose or Prompt cancels.
func (a *App) dialog(ctx context.Context, r ext.UIRequest) ext.UIResponse {
	if a.OnDialog != nil {
		return a.OnDialog(ctx, r)
	}
	cancelled := ext.UIResponse{Cancelled: true}
	title := r.Title
	switch r.Method {
	case "select":
		if a.Choose == nil || len(r.Options) == 0 {
			return cancelled
		}
		if i := a.Choose(title, r.Options); i >= 0 && i < len(r.Options) {
			return ext.UIResponse{Value: &r.Options[i]}
		}
	case "confirm":
		if a.Choose == nil {
			return cancelled
		}
		if r.Message != "" {
			title += "\n" + r.Message
		}
		switch a.Choose(title, []string{"Yes", "No"}) {
		case 0:
			yes := true
			return ext.UIResponse{Confirmed: &yes}
		case 1:
			no := false
			return ext.UIResponse{Confirmed: &no}
		}
	case "input", "editor":
		if a.Prompt == nil {
			return cancelled
		}
		q := title
		if r.Placeholder != "" {
			q += " (" + r.Placeholder + ")"
		}
		if v, err := a.Prompt(strings.TrimSpace(q), false); err == nil {
			return ext.UIResponse{Value: &v}
		}
	}
	return cancelled
}
