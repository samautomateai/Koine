// Package mcptest is a small in-process MCP server for tests: it speaks JSON-RPC over stdio (a test binary re-executes itself as the
// server, see Main) and over streamable HTTP (JSON or SSE answers). It has no dependency on package mcp so that package's own tests can
// use it.
package mcptest

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// EnvStdio, when set in a test binary's environment, makes TestMain call Main so the binary acts as an MCP server.
const EnvStdio = "KOINE_MCPTEST_STDIO"

// EnvMode picks a variant of the stdio server: "" (normal), "crash" (exits after answering initialize), "silent" (never answers tools/call).
const EnvMode = "KOINE_MCPTEST_MODE"

// Server holds the canned tools and resources.
type Server struct {
	Instructions string
	// NoResources and NoTools leave the capability out of initialize.
	NoResources, NoTools bool

	mu      sync.Mutex
	Calls   []string // "tool name" of every tools/call received, in order
	Lists   int      // number of tools/list requests
	extra   []map[string]any
	Initted int
}

// New returns a server with the default tools: echo, img, structured, big, fail, progress, hang, annotated.
func New() *Server { return &Server{Instructions: "Test server for the echo tools.\nSecond line."} }

// AddTool makes tools/list include another tool.
func (s *Server) AddTool(t map[string]any) {
	s.mu.Lock()
	s.extra = append(s.extra, t)
	s.mu.Unlock()
}

func tool(name, desc string, props string) map[string]any {
	var schema map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","properties":`+props+`}`), &schema)
	return map[string]any{"name": name, "description": desc, "inputSchema": schema}
}

func (s *Server) toolList() []map[string]any {
	ro := true
	ann := tool("annotated", "A read-only tool", `{}`)
	ann["annotations"] = map[string]any{"readOnlyHint": ro, "title": "Annotated"}
	list := []map[string]any{
		tool("echo", "Echo the text back", `{"text":{"type":"string"}}`),
		tool("img", "Return an image", `{}`),
		tool("structured", "Return only structured content", `{}`),
		tool("big", "Return a very long text", `{}`),
		tool("fail", "Always reports an error", `{}`),
		tool("progress", "Takes a while and reports progress", `{}`),
		tool("hang", "Never answers", `{}`),
		ann,
		tool("with.dot", "A tool whose name needs sanitising", `{}`),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(list, s.extra...)
}

// Reply is what Handle returns for a request: a result, or an error code and message.
type Reply struct {
	Result any
	Code   int
	Msg    string
}

// Notifier sends a notification or a server request to the client while a request is being handled.
type Notifier func(method string, params any)

// Handle answers one request. notify may be used for progress notifications before the reply.
func (s *Server) Handle(method string, params json.RawMessage, notify Notifier) Reply {
	switch method {
	case "initialize":
		s.mu.Lock()
		s.Initted++
		s.mu.Unlock()
		caps := map[string]any{}
		if !s.NoTools {
			caps["tools"] = map[string]any{"listChanged": true}
		}
		if !s.NoResources {
			caps["resources"] = map[string]any{}
		}
		return Reply{Result: map[string]any{"protocolVersion": "2025-06-18", "capabilities": caps,
			"serverInfo": map[string]any{"name": "mcptest", "version": "1"}, "instructions": s.Instructions}}
	case "ping":
		return Reply{Result: map[string]any{}}
	case "tools/list":
		s.mu.Lock()
		s.Lists++
		s.mu.Unlock()
		var p struct{ Cursor string }
		_ = json.Unmarshal(params, &p)
		all := s.toolList()
		if p.Cursor == "" { // two pages, to exercise nextCursor
			return Reply{Result: map[string]any{"tools": all[:3], "nextCursor": "p2"}}
		}
		return Reply{Result: map[string]any{"tools": all[3:]}}
	case "tools/call":
		return s.call(params, notify)
	case "resources/list":
		return Reply{Result: map[string]any{"resources": []map[string]any{
			{"uri": "file:///notes.txt", "name": "notes", "mimeType": "text/plain", "description": "Some notes\nsecond line"},
			{"uri": "file:///pic.png", "name": "pic", "mimeType": "image/png"},
			{"uri": "ui://widget", "name": "widget"}}}}
	case "resources/templates/list":
		return Reply{Result: map[string]any{"resourceTemplates": []map[string]any{{"uriTemplate": "file:///{name}.txt", "name": "any note"}}}}
	case "resources/read":
		var p struct{ URI string }
		_ = json.Unmarshal(params, &p)
		switch p.URI {
		case "file:///notes.txt":
			return Reply{Result: map[string]any{"contents": []map[string]any{{"uri": p.URI, "mimeType": "text/plain", "text": "hello notes"}}}}
		case "file:///pic.png":
			return Reply{Result: map[string]any{"contents": []map[string]any{{"uri": p.URI, "mimeType": "image/png", "blob": PNG}}}}
		case "file:///data.bin":
			blob := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
			return Reply{Result: map[string]any{"contents": []map[string]any{{"uri": p.URI, "mimeType": "application/octet-stream", "blob": blob}}}}
		}
		return Reply{Code: -32002, Msg: "resource not found: " + p.URI}
	}
	return Reply{Code: -32601, Msg: "method not found: " + method}
}

// PNG is a 1x1 PNG, base64 encoded.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func text(t string) map[string]any { return map[string]any{"type": "text", "text": t} }

func (s *Server) call(params json.RawMessage, notify Notifier) Reply {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			Token any `json:"progressToken"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(params, &p)
	s.mu.Lock()
	s.Calls = append(s.Calls, p.Name)
	s.mu.Unlock()
	switch p.Name {
	case "echo":
		var a struct{ Text string }
		_ = json.Unmarshal(p.Arguments, &a)
		notify("ping-me", nil) // a server request the client must answer, to prove the handler works while a call is pending
		return Reply{Result: map[string]any{"content": []any{text("echo: " + a.Text)}}}
	case "img":
		return Reply{Result: map[string]any{"content": []any{text("here is an image"), map[string]any{"type": "image", "data": PNG, "mimeType": "image/png"}}}}
	case "structured":
		return Reply{Result: map[string]any{"content": []any{}, "structuredContent": map[string]any{"answer": 42}}}
	case "big":
		return Reply{Result: map[string]any{"content": []any{text(strings.Repeat("0123456789", 5000))}}}
	case "fail":
		return Reply{Result: map[string]any{"content": []any{text("it went wrong")}, "isError": true}}
	case "progress":
		for i := 0; i < 4; i++ {
			time.Sleep(60 * time.Millisecond)
			notify("notifications/progress", map[string]any{"progressToken": p.Meta.Token, "progress": i + 1})
		}
		return Reply{Result: map[string]any{"content": []any{text("progress done")}}}
	case "hang":
		select {} // the client's timeout is what ends this call
	case "annotated", "with.dot":
		return Reply{Result: map[string]any{"content": []any{text("ok " + p.Name)}}}
	}
	return Reply{Code: -32602, Msg: "unknown tool " + p.Name}
}

type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r Reply) message(id json.RawMessage) rpc {
	if r.Code != 0 {
		return rpc{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: r.Code, Message: r.Msg}}
	}
	return rpc{JSONRPC: "2.0", ID: id, Result: r.Result}
}

// Serve runs the stdio protocol until r ends. mode is one of the EnvMode values.
func (s *Server) Serve(r io.Reader, w io.Writer, mode string) error {
	var wmu sync.Mutex
	send := func(m rpc) {
		b, _ := json.Marshal(m)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = w.Write(append(b, '\n'))
	}
	sc := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := sc.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var m rpc
			if json.Unmarshal(line, &m) == nil && m.Method != "" && len(m.ID) > 0 {
				go func() {
					notify := func(method string, params any) {
						n := rpc{JSONRPC: "2.0", Method: method}
						if method == "ping-me" {
							n.ID = json.RawMessage(`"srv-1"`)
							n.Method = "ping"
						} else if b, err := json.Marshal(params); err == nil {
							n.Params = b
						}
						send(n)
					}
					if mode == "silent" && m.Method == "tools/call" {
						return
					}
					send(s.Handle(m.Method, m.Params, notify).message(m.ID))
					if mode == "crash" && m.Method == "initialize" {
						time.Sleep(50 * time.Millisecond)
						os.Exit(3)
					}
				}()
			}
		}
		if err != nil {
			return err
		}
	}
}

// Main is what a test binary calls from TestMain when EnvStdio is set: it serves the default server on stdin and stdout and exits.
func Main() {
	_ = New().Serve(os.Stdin, os.Stdout, os.Getenv(EnvMode))
	os.Exit(0)
}

// HTTPOptions tune the HTTP handler.
type HTTPOptions struct {
	SSE         bool   // answer requests with an event stream (progress notifications go in the same stream)
	Token       string // when set, requests need "Authorization: Bearer <Token>" or get 401 with a resource_metadata challenge
	MetadataURL string // resource_metadata value of the 401 challenge
	// FailFirst makes the first N requests answer 503, to exercise retries.
	FailFirst int
	// ExpireSession makes the handler forget its session id after this many requests (then 404), once.
	ExpireSession int
}

// HTTPHandler is the streamable HTTP endpoint.
func (s *Server) HTTPHandler(o HTTPOptions) http.Handler {
	var mu sync.Mutex
	failed, requests, sessions := 0, 0, 0
	session := ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if failed < o.FailFirst {
			failed++
			mu.Unlock()
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		mu.Unlock()
		if o.Token != "" && r.Header.Get("Authorization") != "Bearer "+o.Token {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, o.MetadataURL))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m rpc
		if json.Unmarshal(body, &m) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests++
		if m.Method != "initialize" && o.ExpireSession > 0 && requests > o.ExpireSession && sessions == 1 {
			session = ""
		}
		if m.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != session {
			mu.Unlock()
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		if m.Method == "initialize" {
			sessions++
			session = fmt.Sprintf("sess-%d", sessions)
			w.Header().Set("Mcp-Session-Id", session)
		}
		mu.Unlock()
		if len(m.ID) == 0 { // notification
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if !o.SSE {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(s.Handle(m.Method, m.Params, func(string, any) {}).message(m.ID))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		event := func(v rpc) {
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, ": comment\nevent: message\ndata: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}
		notify := func(method string, params any) {
			if method == "ping-me" {
				return
			}
			n := rpc{JSONRPC: "2.0", Method: method}
			n.Params, _ = json.Marshal(params)
			event(n)
		}
		event(rpc{JSONRPC: "2.0", Method: "notifications/message", Params: json.RawMessage(`{"level":"info"}`)})
		event(s.Handle(m.Method, m.Params, notify).message(m.ID))
	})
}
