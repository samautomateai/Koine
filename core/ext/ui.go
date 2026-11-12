package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"koine/core/events"
)

// UIRequest is something an extension asks of the frontend (ctx.ui). The field names are those of Pi's RPC extension UI sub-protocol
// (https://pi.dev/docs/latest/rpc-extension-ui), so the RPC frontend can pass a request on as it is.
//
// Dialogs (select, confirm, input, editor) wait for a UIResponse. The rest (notify, setStatus, setWidget, setTitle, setWorkingMessage,
// set_editor_text) are fire-and-forget.
type UIRequest struct {
	Method      string   `json:"method"`
	Title       string   `json:"title,omitempty"`
	Message     string   `json:"message,omitempty"` // confirm: the question; notify and setWorkingMessage: the text
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Prefill     string   `json:"prefill,omitempty"`
	Timeout     int      `json:"timeout,omitempty"` // dialogs: milliseconds until the dialog resolves on its own
	NotifyType  string   `json:"notifyType,omitempty"`
	StatusKey   string   `json:"statusKey,omitempty"`
	StatusText  *string  `json:"statusText,omitempty"` // nil clears the status
	WidgetKey   string   `json:"widgetKey,omitempty"`
	WidgetLines []string `json:"widgetLines,omitempty"` // empty clears the widget
	// WidgetPlacement is "aboveEditor" (default) or "belowEditor".
	WidgetPlacement string `json:"widgetPlacement,omitempty"`
	Text            string `json:"text,omitempty"` // set_editor_text
	// Extension names the extension that asked. It is filled in by the host.
	Extension string `json:"-"`
}

// IsDialog reports whether the request waits for an answer.
func (r UIRequest) IsDialog() bool {
	switch r.Method {
	case "select", "confirm", "input", "editor":
		return true
	}
	return false
}

// UIResponse answers a dialog: Value for select, input and editor, Confirmed for confirm, Cancelled when the user dismissed it or
// the time ran out.
type UIResponse struct {
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Cancelled bool    `json:"cancelled,omitempty"`
}

var (
	cancelled        = UIResponse{Cancelled: true}
	errNoNestedTools = errors.New("this host cannot run tools for an extension")
)

func errMethod(m string) error { return fmt.Errorf("method not found: %s", m) }

// ExtError is an error an extension's handler raised. The extension carries on; frontends may show it (Pi's extension_error).
type ExtError struct{ Extension, Event, Error string }

// SendOptions are the options of pi.sendMessage.
type SendOptions struct {
	DeliverAs   string // "steer" (default), "followUp" or "nextTurn"
	TriggerTurn bool
}

// askUI asks the frontend a dialog and waits for the answer, the timeout or the end of ctx, whichever comes first. A frontend that cannot
// close a dialog on its own (a terminal prompt) is left to finish it, and its late answer is dropped.
func (h *Host) askUI(ctx context.Context, req UIRequest) UIResponse {
	if h.Dialog == nil {
		return cancelled
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Millisecond)
		defer cancel()
	}
	done := make(chan UIResponse, 1)
	go func() { done <- h.Dialog(ctx, req) }()
	select {
	case r := <-done:
		return r
	case <-ctx.Done():
		return cancelled
	}
}

// serve answers a request an extension made of the host. It runs on its own goroutine: a dialog may wait for the user.
func (p *Proc) serve(m msg) {
	h := p.host
	reply := func(v any, err error) {
		if err != nil {
			p.write(msg{ID: m.ID, Error: &rpcErr{Code: -32000, Message: err.Error()}})
			return
		}
		b, _ := json.Marshal(v)
		p.write(msg{ID: m.ID, Result: b})
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		select {
		case <-p.dead:
			stop()
		case <-ctx.Done():
		}
	}()
	switch m.Method {
	case "ui.dialog":
		var req UIRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			reply(nil, err)
			return
		}
		req.Extension = p.Name
		reply(h.askUI(ctx, req), nil)
	case "tools.execute":
		var req struct {
			Name string
			Args json.RawMessage
		}
		if err := json.Unmarshal(m.Params, &req); err != nil {
			reply(nil, err)
			return
		}
		if h.RunTool == nil {
			reply(nil, errNoNestedTools)
			return
		}
		out, isErr := h.RunTool(ctx, req.Name, req.Args)
		reply(map[string]any{"content": out, "isError": isErr}, nil)
	default:
		reply(nil, errMethod(m.Method))
	}
}

// surface hands a fire-and-forget request to the frontend.
func (h *Host) surface(p *Proc, params json.RawMessage) {
	var req UIRequest
	if json.Unmarshal(params, &req) != nil || req.Method == "" || h.Surface == nil {
		return
	}
	req.Extension = p.Name
	h.Surface(req)
}

// chain carries what one extension changed in an event into the payload the next one is given, so handlers of different extensions
// compose the way the handlers of one extension do. Payloads that are not the maps the agent builds are passed on as they are.
func chain(name string, payload any, d events.Decision) any {
	pl, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	next := make(map[string]any, len(pl))
	for k, v := range pl {
		next[k] = v
	}
	switch name {
	case events.ToolCall:
		if len(d.Args) > 0 {
			next["input"] = d.Args
		}
	case events.ToolResult:
		if d.Output != nil {
			next["output"] = *d.Output
		}
		if d.IsError != nil {
			next["isError"] = *d.IsError
		}
		if len(d.Content) > 0 {
			next["content"] = d.Content
		}
		if len(d.Details) > 0 {
			next["details"] = d.Details
		}
	case events.Input:
		if d.Text != nil {
			next["text"] = *d.Text
		}
		if len(d.Images) > 0 {
			next["images"] = d.Images
		}
	case events.BeforeAgentStart:
		if d.SystemPrompt != nil {
			next["systemPrompt"] = *d.SystemPrompt
		}
	}
	return next
}
