package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"koine/core/ext"
)

// rpcUI is the extension UI sub-protocol (https://pi.dev/docs/latest/rpc-extension-ui). A dialog an extension opens (select, confirm, input,
// editor) becomes an extension_ui_request on stdout and waits for the extension_ui_response with the same id. Notifications, status
// entries, widgets, titles and editor text are requests that expect no answer. setWorkingMessage and the other methods Pi lists as no-ops
// in RPC mode are not sent.
type rpcUI struct {
	s       *rpcServer
	mu      sync.Mutex
	seq     int
	waiting map[string]chan ext.UIResponse
}

func (u *rpcUI) init(s *rpcServer) { u.s, u.waiting = s, map[string]chan ext.UIResponse{} }

// request writes an extension_ui_request: type, id, method and then the request's own fields, sorted by name.
func (u *rpcUI) request(id string, r ext.UIRequest) {
	raw, _ := json.Marshal(r)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "method")
	out := record{{"type", "extension_ui_request"}, {"id", id}, {"method", r.Method}}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, kv{k, fields[k]})
	}
	u.s.put(out)
}

// dialog sends a dialog request and waits for the client's answer, the end of ctx (the dialog timed out or the extension gave up) or the
// end of the server.
func (u *rpcUI) dialog(ctx context.Context, r ext.UIRequest) ext.UIResponse {
	u.mu.Lock()
	u.seq++
	id := fmt.Sprintf("ui-%d", u.seq)
	ch := make(chan ext.UIResponse, 1)
	u.waiting[id] = ch
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		delete(u.waiting, id)
		u.mu.Unlock()
	}()
	u.request(id, r)
	select {
	case resp := <-ch:
		return resp
	case <-ctx.Done():
		return ext.UIResponse{Cancelled: true}
	case <-u.s.ctx.Done():
		return ext.UIResponse{Cancelled: true}
	}
}

// surface sends a request that expects no answer.
func (u *rpcUI) surface(r ext.UIRequest) {
	switch r.Method {
	case "notify", "setStatus", "setWidget", "setTitle", "set_editor_text":
		u.mu.Lock()
		u.seq++
		id := fmt.Sprintf("ui-%d", u.seq)
		u.mu.Unlock()
		u.request(id, r)
	}
}

// answer delivers an extension_ui_response to the dialog waiting for it. A response nobody waits for (late, or a stray id) is dropped.
func (u *rpcUI) answer(line []byte) {
	var in struct {
		ID        string  `json:"id"`
		Value     *string `json:"value"`
		Confirmed *bool   `json:"confirmed"`
		Cancelled bool    `json:"cancelled"`
	}
	if json.Unmarshal(line, &in) != nil {
		return
	}
	u.mu.Lock()
	ch := u.waiting[in.ID]
	u.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- ext.UIResponse{Value: in.Value, Confirmed: in.Confirmed, Cancelled: in.Cancelled}:
	default:
	}
}
