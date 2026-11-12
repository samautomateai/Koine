package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"koine/core/mcp/mcptest"
)

// TestMain lets the test binary act as the stdio MCP server the tests start.
func TestMain(m *testing.M) {
	if os.Getenv(mcptest.EnvStdio) != "" {
		mcptest.Main()
	}
	os.Exit(m.Run())
}

func testStdio(t *testing.T, mode string, stderr *lockedBuf) Transport {
	t.Helper()
	env := append(os.Environ(), "GORACE=atexit_sleep_ms=0", mcptest.EnvStdio+"=1", mcptest.EnvMode+"="+mode)
	cfg := StdioConfig{Command: os.Args[0], Env: env, StopGrace: 300 * time.Millisecond}
	if stderr != nil {
		cfg.Stderr = stderr
	}
	return NewStdio(cfg)
}

// shellInitReply is a shell command that answers the first request (initialize) of a hand-made server.
const shellInitReply = `echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}'`

type lockedBuf struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

func connect(t *testing.T, tr Transport) *Client {
	t.Helper()
	c := NewClient(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestStdioInitializeListAndCall(t *testing.T) {
	c := connect(t, testStdio(t, "", nil))
	if c.Info.ServerInfo.Name != "mcptest" || !strings.HasPrefix(c.Info.Instructions, "Test server") || c.Info.Capabilities.Tools == nil {
		t.Fatalf("init = %+v", c.Info)
	}
	ctx := context.Background()
	tl, err := c.ListTools(ctx) // two pages
	if err != nil || len(tl) != 9 {
		t.Fatalf("tools = %d, %v", len(tl), err)
	}
	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`)) // the server pings us during the call; the client must answer it
	if err != nil || res.IsError || res.Content[0].Text != "echo: hi" {
		t.Fatalf("echo = %+v, %v", res, err)
	}
	if _, err := c.CallTool(ctx, "nope", nil); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool err = %v", err)
	}
	var rpc *RPCError
	if _, err := c.CallTool(ctx, "nope", json.RawMessage("null")); !errors.As(err, &rpc) || rpc.Code != -32602 {
		t.Fatalf("want RPCError -32602, got %v", err)
	}
	rs, err := c.ListResources(ctx)
	if err != nil || len(rs) != 3 {
		t.Fatalf("resources = %v, %v", rs, err)
	}
	ts, err := c.ListResourceTemplates(ctx)
	if err != nil || len(ts) != 1 || ts[0].URITemplate == "" {
		t.Fatalf("templates = %v, %v", ts, err)
	}
	body, err := c.ReadResource(ctx, "file:///notes.txt")
	if err != nil || body[0].Text != "hello notes" {
		t.Fatalf("read = %v, %v", body, err)
	}
	if _, err := c.ReadResource(ctx, "file:///missing"); err == nil {
		t.Fatal("missing resource should fail")
	}
}

func TestClientTimeoutAndProgress(t *testing.T) {
	c := connect(t, testStdio(t, "", nil))
	c.Timeout = 150 * time.Millisecond
	ctx := context.Background()
	// "progress" takes about 240ms with a notification every 60ms: longer than the timeout, so only the resets let it finish.
	res, err := c.CallTool(ctx, "progress", nil)
	if err != nil || res.Content[0].Text != "progress done" {
		t.Fatalf("progress = %+v, %v", res, err)
	}
	_, err = c.CallTool(ctx, "hang", nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("hang err = %v, want a timeout", err)
	}
	// the connection is still usable after a timeout
	if _, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"x"}`)); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.CallTool(cctx, "echo", nil); err == nil {
		t.Fatal("cancelled context should fail")
	}
}

func TestStdioServerCrash(t *testing.T) {
	log := &lockedBuf{}
	tr := testStdio(t, "crash", log)
	c := connect(t, tr)
	deadline := time.Now().Add(5 * time.Second)
	for tr.Alive() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if tr.Alive() {
		t.Fatal("transport should notice the exit")
	}
	if _, err := c.CallTool(context.Background(), "echo", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	if err := c.T.Send(context.Background(), []byte("{}")); !errors.Is(err, ErrClosed) {
		t.Fatalf("send err = %v", err)
	}
}

func TestStdioCloseKillsStubbornProcess(t *testing.T) {
	c := connect(t, testStdio(t, "silent", nil))
	done := make(chan struct{})
	go func() { _ = c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	if c.T.Alive() {
		t.Fatal("process should be gone")
	}
	if err := (&stdioTransport{}).Close(); err != nil { // never started
		t.Fatal(err)
	}
}

func TestStdioStartFailure(t *testing.T) {
	tr := NewStdio(StdioConfig{Command: "/nonexistent/koine-mcp-server"})
	if err := NewClient(tr).Initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("err = %v", err)
	}
	if tr.Alive() {
		t.Fatal("never started")
	}
	if _, err := tr.Do(context.Background(), 1, []byte("{}")); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestStdioLogsStderrAndStrayStdout(t *testing.T) {
	log := &lockedBuf{}
	// a shell server that prints a banner to stdout and stderr, then answers initialize
	script := `echo banner; echo oops >&2; read line; ` + shellInitReply + `; read line; sleep 5`
	tr := NewStdio(StdioConfig{Command: "sh", Args: []string{"-c", script}, Env: os.Environ(), Stderr: log, StopGrace: 200 * time.Millisecond})
	c := NewClient(tr)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if s := log.String(); !strings.Contains(s, "oops") || !strings.Contains(s, "banner") {
		t.Fatalf("log = %q", s)
	}
}

func TestHandleServerMessages(t *testing.T) {
	var sent [][]byte
	var mu sync.Mutex
	ft := &fakeT{send: func(b []byte) { mu.Lock(); sent = append(sent, b); mu.Unlock() }}
	c := NewClient(ft)
	changed := make(chan struct{}, 1)
	c.OnToolsChanged = func() { changed <- struct{}{} }
	c.handle(&Message{Method: "notifications/tools/list_changed"})
	<-changed
	c.handle(&Message{Method: "notifications/progress", Params: json.RawMessage(`{"progressToken":"nobody"}`)})
	c.handle(&Message{Method: "notifications/progress", Params: json.RawMessage(`not json`)})
	c.handle(&Message{ID: json.RawMessage(`7`), Method: "roots/list"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || !strings.Contains(string(sent[0]), "-32601") {
		t.Fatalf("sent = %q", sent)
	}
}

// fakeT is a Transport that records what is sent.
type fakeT struct {
	send func([]byte)
	h    func(*Message)
}

func (f *fakeT) Start(context.Context) error { return nil }
func (f *fakeT) Do(context.Context, int64, []byte) (*Message, error) {
	return nil, errors.New("not used")
}
func (f *fakeT) Send(_ context.Context, b []byte) error { f.send(b); return nil }
func (f *fakeT) SetHandler(h func(*Message))            { f.h = h }
func (f *fakeT) SetProtocolVersion(string)              {}
func (f *fakeT) Alive() bool                            { return true }
func (f *fakeT) Close() error                           { return nil }
