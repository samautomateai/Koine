package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTimeout is how long a request may go without an answer or a progress notification (the config's timeout is in seconds).
const DefaultTimeout = 60 * time.Second

// ErrTimeout is the cause of a request that ran out of time.
var ErrTimeout = errors.New("mcp: request timed out")

// Client speaks MCP over a Transport: the initialize handshake and the requests this agent needs.
type Client struct {
	T       Transport
	Timeout time.Duration // per request; progress notifications for the request start it again. Zero means DefaultTimeout.
	// OnToolsChanged is called when the server says its tool list changed. It runs on its own goroutine.
	OnToolsChanged func()

	Info InitResult // what the server said in initialize

	next atomic.Int64
	mu   sync.Mutex
	prog map[string]func() // progress token -> restart the request's timer
}

// NewClient wraps a transport.
func NewClient(t Transport) *Client {
	c := &Client{T: t, prog: map[string]func(){}}
	t.SetHandler(c.handle)
	return c
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// handle deals with what a server sends on its own: progress, list changes and requests (ping; everything else is refused).
func (c *Client) handle(m *Message) {
	switch {
	case m.Method == "notifications/progress":
		var p struct {
			Token json.RawMessage `json:"progressToken"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		key := string(p.Token)
		if u, err := strconv.Unquote(key); err == nil {
			key = u
		}
		c.mu.Lock()
		reset := c.prog[key]
		c.mu.Unlock()
		if reset != nil {
			reset()
		}
	case m.Method == "notifications/tools/list_changed":
		if c.OnToolsChanged != nil {
			go c.OnToolsChanged()
		}
	case len(m.ID) > 0 && m.Method != "":
		reply := Message{JSONRPC: "2.0", ID: m.ID}
		if m.Method == "ping" {
			reply.Result = json.RawMessage("{}")
		} else {
			reply.Error = &RPCError{Code: -32601, Message: "method not supported by this client: " + m.Method}
		}
		go func() {
			b, _ := json.Marshal(reply)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = c.T.Send(ctx, b)
		}()
	}
}

// Initialize starts the transport and performs the handshake.
func (c *Client) Initialize(ctx context.Context) error {
	if err := c.T.Start(ctx); err != nil {
		return err
	}
	var res InitResult
	err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "koine", "version": "1"},
	}, &res)
	if err != nil {
		return err
	}
	c.Info = res
	if res.ProtocolVersion != "" {
		c.T.SetProtocolVersion(res.ProtocolVersion)
	}
	return c.notify(ctx, "notifications/initialized", nil)
}

func (c *Client) notify(ctx context.Context, method string, params map[string]any) error {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return c.T.Send(ctx, b)
}

// call sends a request and decodes its result into out (which may be nil). The timeout restarts on every progress notification.
func (c *Client) call(ctx context.Context, method string, params map[string]any, out any) error {
	id := c.next.Add(1)
	token := strconv.FormatInt(id, 10)
	if params == nil {
		params = map[string]any{}
	}
	if method != "initialize" {
		params["_meta"] = map[string]any{"progressToken": token}
	}
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	cctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(c.timeout(), func() { cancel(ErrTimeout) })
	defer timer.Stop()
	c.mu.Lock()
	c.prog[token] = func() { timer.Reset(c.timeout()) }
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.prog, token)
		c.mu.Unlock()
	}()

	resp, err := c.T.Do(cctx, id, b)
	if err != nil {
		if errors.Is(context.Cause(cctx), ErrTimeout) {
			bg, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_ = c.notify(bg, "notifications/cancelled", map[string]any{"requestId": id, "reason": "timeout"})
			return fmt.Errorf("%s: %w after %s", method, ErrTimeout, c.timeout())
		}
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

// paged follows nextCursor until the list is complete.
func (c *Client) paged(ctx context.Context, method, key string, each func(raw json.RawMessage) error) error {
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res map[string]json.RawMessage
		if err := c.call(ctx, method, params, &res); err != nil {
			return err
		}
		if err := each(res[key]); err != nil {
			return err
		}
		cursor = ""
		_ = json.Unmarshal(res["nextCursor"], &cursor)
		if cursor == "" {
			return nil
		}
	}
	return nil
}

// ListTools returns every tool the server offers.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var out []Tool
	err := c.paged(ctx, "tools/list", "tools", func(raw json.RawMessage) error {
		var page []Tool
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// CallTool runs a tool. A tool that fails by its own account comes back with IsError set, not as an error.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 && string(args) != "null" {
		params["arguments"] = args
	}
	var res CallResult
	if err := c.call(ctx, "tools/call", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ListResources returns the server's resources.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	var out []Resource
	err := c.paged(ctx, "resources/list", "resources", func(raw json.RawMessage) error {
		var page []Resource
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// ListResourceTemplates returns the server's resource templates.
func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	var out []ResourceTemplate
	err := c.paged(ctx, "resources/templates/list", "resourceTemplates", func(raw json.RawMessage) error {
		var page []ResourceTemplate
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// ReadResource returns the contents of one resource.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]Resource, error) {
	var res struct {
		Contents []Resource `json:"contents"`
	}
	if err := c.call(ctx, "resources/read", map[string]any{"uri": uri}, &res); err != nil {
		return nil, err
	}
	return res.Contents, nil
}

// Close shuts the connection down.
func (c *Client) Close() error { return c.T.Close() }
