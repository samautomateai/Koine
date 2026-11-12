package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"koine/core/mcp/mcptest"
	"koine/core/tools"
)

func stdioEntry(name string, mut func(*ServerConfig)) Entry {
	c := ServerConfig{Type: "stdio", Command: os.Args[0], Env: map[string]string{mcptest.EnvStdio: "1", "GORACE": "atexit_sleep_ms=0"}}
	if mut != nil {
		mut(&c)
	}
	return Entry{Name: name, Config: c, Source: SourceUser}
}

func newManager(t *testing.T) (*Manager, *tools.Registry) {
	t.Helper()
	reg := tools.NewRegistry()
	m := NewManager(reg, t.TempDir(), t.TempDir())
	t.Cleanup(m.Close)
	return m, reg
}

func startAll(t *testing.T, m *Manager, entries ...Entry) {
	t.Helper()
	m.Start(context.Background(), entries, 15*time.Second)
	m.Wait()
}

func run(t *testing.T, reg *tools.Registry, name, args string) (string, error) {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %s is not offered; have %v", name, reg.Names())
	}
	return tool.Run(context.Background(), json.RawMessage(args))
}

func TestManagerDirectTools(t *testing.T) {
	m, reg := newManager(t)
	startAll(t, m, stdioEntry("test-srv", nil))
	want := []string{"mcp__test_srv__echo", "mcp__test_srv__with_dot", "mcp__test_srv__annotated"}
	for _, n := range want {
		if _, ok := reg.Get(n); !ok {
			t.Fatalf("missing %s in %v", n, reg.Names())
		}
	}
	out, err := run(t, reg, "mcp__test_srv__echo", `{"text":"yo"}`)
	if err != nil || out != "echo: yo" {
		t.Fatalf("echo = %q, %v", out, err)
	}
	st := m.Statuses()
	if len(st) != 1 || st[0].State != StateConnected || st[0].Tools != 9 || st[0].Exposure != ExposureDirect || !st[0].Resources || st[0].Source != "user" {
		t.Fatalf("status = %+v", st)
	}
	// prompt metadata and annotations
	tool, _ := reg.Get("mcp__test_srv__annotated")
	p := tool.(tools.Prompted)
	if p.PromptSnippet() != "A read-only tool" || len(p.PromptGuidelines()) != 1 || !strings.Contains(p.PromptGuidelines()[0], "mcp__test_srv__*") ||
		!strings.Contains(p.PromptGuidelines()[0], "Test server for the echo tools.") {
		t.Fatalf("prompt = %q %q", p.PromptSnippet(), p.PromptGuidelines())
	}
	a := tool.(Annotated)
	if a.MCPServer() != "test-srv" || a.MCPAnnotations() == nil || a.MCPAnnotations().ReadOnlyHint == nil || !*a.MCPAnnotations().ReadOnlyHint {
		t.Fatalf("annotations = %+v", a.MCPAnnotations())
	}
	lines, rules := reg.PromptParts()
	if len(lines) < 5 || len(rules) != 2 {
		t.Fatalf("prompt parts: %d lines, %q", len(lines), rules)
	}
}

func TestToolResultsErrorsImagesAndLimits(t *testing.T) {
	m, reg := newManager(t)
	startAll(t, m, stdioEntry("s", nil))
	if _, err := run(t, reg, "mcp__s__fail", `{}`); err == nil {
		t.Fatal("an isError result must be an error")
	} else if out, _ := run(t, reg, "mcp__s__fail", `{}`); out != "it went wrong" {
		t.Fatalf("error text = %q", out)
	}
	out, err := run(t, reg, "mcp__s__structured", `{}`)
	if err != nil || out != `{"answer":42}` {
		t.Fatalf("structured = %q, %v", out, err)
	}
	out, err = run(t, reg, "mcp__s__big", `{}`)
	if err != nil || !strings.Contains(out, "…29520 chars truncated…") || !strings.Contains(out, "[full output saved to ") {
		t.Fatalf("big = %.80q…%v", out, err)
	}
	path := strings.TrimSuffix(out[strings.LastIndex(out, "saved to ")+9:], "]")
	if b, err := os.ReadFile(path); err != nil || len(b) != 50000 {
		t.Fatalf("saved file: %d bytes, %v", len(b), err)
	}
	_ = os.Remove(path)

	// images go through the context's image sink; without one the text says so
	tool, _ := reg.Get("mcp__s__img")
	var got []tools.Image
	ctx := tools.WithImages(context.Background(), tools.ImageSink{Add: func(i tools.Image) { got = append(got, i) }})
	out, err = tool.Run(ctx, json.RawMessage(`{}`))
	if err != nil || len(got) != 1 || got[0].Mime != "image/png" || !strings.Contains(out, "here is an image") {
		t.Fatalf("img = %q %v %v", out, got, err)
	}
	if out, _ = tool.Run(context.Background(), json.RawMessage(`{}`)); !strings.Contains(out, "cannot take images") {
		t.Fatalf("img without sink = %q", out)
	}
}

func TestExposureModesAndToolSearch(t *testing.T) {
	m, reg := newManager(t)
	startAll(t, m,
		stdioEntry("dir", func(c *ServerConfig) {
			c.ToolExposure = &ToolExposure{Keys: []string{"echo", "b*"}, Mode: map[string]string{"echo": ExposureHidden, "b*": ExposureDeferred}}
		}),
		stdioEntry("lazy", func(c *ServerConfig) { c.Exposure = ExposureDeferred; c.Description = "Lazy things" }),
		stdioEntry("pi", func(c *ServerConfig) { c.Exposure = "codemode" }),
		stdioEntry("gone", func(c *ServerConfig) { c.Exposure = ExposureHidden }),
	)
	have := map[string]bool{}
	for _, n := range reg.Names() {
		have[n] = true
	}
	if !have["mcp__dir__img"] || have["mcp__dir__echo"] || have["mcp__dir__big"] || have["mcp__lazy__echo"] || have["mcp__pi__echo"] ||
		have["mcp__gone__echo"] || !have[ToolSearchName] {
		t.Fatalf("offered = %v", reg.Names())
	}
	st := map[string]Status{}
	for _, s := range m.Statuses() {
		st[s.Name] = s
	}
	if st["dir"].Exposure != ExposureDirect || len(st["dir"].Overrides) != 2 || st["lazy"].Exposure != ExposureDeferred ||
		st["pi"].Exposure != ExposureDeferred || st["gone"].Exposure != ExposureHidden {
		t.Fatalf("statuses = %+v", st)
	}
	ts, _ := reg.Get(ToolSearchName)
	if !strings.Contains(ts.Description(), "lazy (9 tools: Lazy things)") || !strings.Contains(ts.Description(), "pi (9 tools") {
		t.Fatalf("description = %q", ts.Description())
	}
	out, err := run(t, reg, ToolSearchName, `{"query":"image picture","limit":50}`)
	if err != nil || !strings.Contains(out, "mcp__lazy__img") || !strings.Contains(out, "mcp__pi__img") {
		t.Fatalf("search = %q, %v", out, err)
	}
	if _, ok := reg.Get("mcp__lazy__img"); !ok {
		t.Fatal("a tool found by tool_search must be offered from then on")
	}
	if _, ok := reg.Get("mcp__lazy__echo"); ok {
		t.Fatal("tools that were not found stay deferred")
	}
	if out, _ = run(t, reg, ToolSearchName, `{"query":"zzzzzz"}`); !strings.Contains(out, "No unloaded") {
		t.Fatalf("no match = %q", out)
	}
	if _, err := run(t, reg, ToolSearchName, `{"query":"  "}`); err == nil {
		t.Fatal("empty query")
	}
	// the loaded tool works
	if out, err := run(t, reg, "mcp__lazy__img", `{}`); err != nil || !strings.Contains(out, "here is an image") {
		t.Fatalf("loaded tool = %q, %v", out, err)
	}
}

func TestToolNaming(t *testing.T) {
	none := func(string) bool { return false }
	if got := ToolName("my-srv", "do.it now", none); got != "mcp__my_srv__do_it_now" {
		t.Fatalf("got %s", got)
	}
	taken := map[string]bool{"mcp__s__a_b": true}
	got := ToolName("s", "a.b", func(n string) bool { return taken[n] })
	if !strings.HasPrefix(got, "mcp__s__a_b_") || len(got) != len("mcp__s__a_b_")+6 {
		t.Fatalf("collision name = %s", got)
	}
	if other := ToolName("s", "a-b", func(n string) bool { return taken[n] }); other == got {
		t.Fatalf("different originals must hash differently")
	}
	long := ToolName(strings.Repeat("s", 40), strings.Repeat("t", 40), none)
	if len(long) > 64 || !strings.HasPrefix(long, "mcp__sss") {
		t.Fatalf("long = %s (%d)", long, len(long))
	}
	if firstLine("\n\n  hello world  \nsecond", 100) != "hello world" || firstLine(strings.Repeat("é", 20), 10) != strings.Repeat("é", 9)+"…" {
		t.Fatal("firstLine")
	}
}

func TestResourceTools(t *testing.T) {
	m, reg := newManager(t)
	startAll(t, m, stdioEntry("a", nil), stdioEntry("noresources", nil))
	// tools are only offered when some server has resources
	out, err := run(t, reg, ListResourcesName, `{}`)
	if err != nil || !strings.Contains(out, "a\tfile:///notes.txt\tnotes\ttext/plain\tSome notes") || !strings.Contains(out, "noresources\tfile:///pic.png") {
		t.Fatalf("list = %q, %v", out, err)
	}
	if strings.Contains(out, "ui://") {
		t.Fatalf("ui:// resources are not for the model: %q", out)
	}
	if out, err = run(t, reg, ListResourcesName, `{"server":"a"}`); err != nil || strings.Contains(out, "noresources") {
		t.Fatalf("filtered list = %q, %v", out, err)
	}
	if out, err = run(t, reg, ListTemplatesName, `{}`); err != nil || !strings.Contains(out, "file:///{name}.txt") {
		t.Fatalf("templates = %q, %v", out, err)
	}
	if out, err = run(t, reg, ReadResourceName, `{"server":"a","uri":"file:///notes.txt"}`); err != nil || out != "hello notes" {
		t.Fatalf("read = %q, %v", out, err)
	}
	var imgs []tools.Image
	rt, _ := reg.Get(ReadResourceName)
	ctx := tools.WithImages(context.Background(), tools.ImageSink{Add: func(i tools.Image) { imgs = append(imgs, i) }})
	out, err = rt.Run(ctx, json.RawMessage(`{"server":"a","uri":"file:///pic.png"}`))
	if err != nil || len(imgs) != 1 || !strings.Contains(out, "image resource") {
		t.Fatalf("image resource = %q %v %v", out, imgs, err)
	}
	out, err = rt.Run(ctx, json.RawMessage(`{"server":"a","uri":"file:///data.bin"}`))
	if err != nil || !strings.Contains(out, "4 bytes) saved to ") {
		t.Fatalf("binary resource = %q, %v", out, err)
	}
	p := out[strings.LastIndex(out, "saved to ")+9:]
	if b, err := os.ReadFile(strings.TrimSuffix(p, "]")); err != nil || len(b) != 4 {
		t.Fatalf("binary file = %v, %v", b, err)
	}
	for _, bad := range []string{`{}`, `{"server":"a"}`, `{"server":"zzz","uri":"x"}`, `{"server":"a","uri":"file:///missing"}`} {
		if _, err := rt.Run(ctx, json.RawMessage(bad)); err == nil {
			t.Errorf("read %s should fail", bad)
		}
	}
	if _, err := run(t, reg, ListResourcesName, `{"server":"zzz"}`); err == nil {
		t.Error("unknown server")
	}
}

func TestNoResourceToolsWithoutCapability(t *testing.T) {
	m, reg := newManager(t)
	startAll(t, m, stdioEntry("tools-only", nil))
	if _, ok := reg.Get(ListResourcesName); !ok {
		t.Fatal("resource tools expected when the server has resources")
	}
	// a server without the resources capability gives a clear error for a named lookup
	srv := mcptest.New()
	srv.NoResources = true
	hs := httptest.NewServer(srv.HTTPHandler(mcptest.HTTPOptions{}))
	defer hs.Close()
	m2, reg2 := newManager(t)
	startAll(t, m2, Entry{Name: "web", Config: ServerConfig{URL: hs.URL}})
	if _, ok := reg2.Get(ListResourcesName); ok {
		t.Fatal("no resource tools without the capability")
	}
	// naming a server without the capability is an error that says so
	if err := m2.withResources(context.Background(), "web", func(*Server, *Client) error { return nil }); err == nil ||
		!strings.Contains(err.Error(), "does not offer resources") {
		t.Fatalf("err = %v", err)
	}
}

func TestFailedAndDisabledServers(t *testing.T) {
	m, reg := newManager(t)
	var notes []string
	var mu sync.Mutex
	m.Notice = func(s string) { mu.Lock(); notes = append(notes, s); mu.Unlock() }
	startAll(t, m,
		stdioEntry("good", nil),
		Entry{Name: "broken", Config: ServerConfig{Type: "stdio", Command: "/nonexistent/server"}},
		stdioEntry("off", func(c *ServerConfig) { f := false; c.Enabled = &f }),
		stdioEntry("envless", func(c *ServerConfig) { c.Env = map[string]string{"X": "${KOINE_DEFINITELY_UNSET_VAR}"} }),
		Entry{Name: "cmdless", Config: ServerConfig{Type: "stdio", Command: "${KOINE_DEFINITELY_UNSET_VAR}"}},
	)
	st := map[string]Status{}
	for _, s := range m.Statuses() {
		st[s.Name] = s
	}
	if st["good"].State != StateConnected || st["broken"].State != StateFailed || st["off"].State != StateDisabled || st["envless"].State != StateFailed ||
		st["cmdless"].State != StateFailed {
		t.Fatalf("states = %+v", st)
	}
	if !strings.Contains(st["envless"].Err, "KOINE_DEFINITELY_UNSET_VAR") || !strings.Contains(st["broken"].Err, "start") {
		t.Fatalf("errors = %+v", st)
	}
	if _, ok := reg.Get("mcp__good__echo"); !ok {
		t.Fatal("other servers still connect")
	}
	if err := m.Reconnect(context.Background(), "off"); err == nil {
		t.Fatal("disabled server")
	}
	if err := m.Reconnect(context.Background(), "nope"); err == nil {
		t.Fatal("unknown server")
	}
	if err := m.Reconnect(context.Background(), "broken"); err == nil {
		t.Fatal("still broken")
	}
}

func TestReconnectAfterCrashAndExplicit(t *testing.T) {
	m, reg := newManager(t)
	startAll(t, m, stdioEntry("s", nil))
	m.mu.Lock()
	s := m.servers["s"]
	m.mu.Unlock()
	// kill the process behind the connection: the next call reconnects
	s.mu.Lock()
	tr := s.client.T.(*stdioTransport)
	s.mu.Unlock()
	_ = tr.cmd.Process.Kill()
	waitFor(t, func() bool { return !tr.Alive() })
	out, err := run(t, reg, "mcp__s__echo", `{"text":"again"}`)
	if err != nil || out != "echo: again" {
		t.Fatalf("after reconnect = %q, %v", out, err)
	}
	if err := m.Reconnect(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, reg, "mcp__s__echo", `{"text":"third"}`); err != nil || out != "echo: third" {
		t.Fatalf("after Reconnect = %q, %v", out, err)
	}
	// a server that cannot be reached any more yields a clear error and a failed state
	m.mu.Lock()
	m.servers["s"].Entry.Config.Command = "/nonexistent/gone"
	m.mu.Unlock()
	s.drop()
	if _, err = run(t, reg, "mcp__s__echo", `{}`); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("err = %v", err)
	}
	if m.Statuses()[0].State != StateFailed {
		t.Fatalf("state = %s", m.Statuses()[0].State)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHTTPServerThroughManagerSurvivesSessionExpiry(t *testing.T) {
	srv := mcptest.New()
	hs := httptest.NewServer(srv.HTTPHandler(mcptest.HTTPOptions{ExpireSession: 4}))
	defer hs.Close()
	m, reg := newManager(t)
	startAll(t, m, Entry{Name: "web", Config: ServerConfig{URL: hs.URL, Headers: map[string]string{"X-Key": "${HOME_FOR_TEST}"}}})
	if m.Statuses()[0].State != StateFailed {
		t.Fatalf("an unset header variable must fail the server, got %+v", m.Statuses()[0])
	}
	t.Setenv("HOME_FOR_TEST", "k")
	if err := m.Reconnect(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	// initialize(1) + initialized(2) + tools/list x2 (3,4): the next request sees an unknown session and the call reconnects once
	if out, err := run(t, reg, "mcp__web__echo", `{"text":"x"}`); err != nil || out != "echo: x" {
		t.Fatalf("call across session expiry = %q, %v (calls %v)", out, err, srv.Calls)
	}
}

func TestToolListChangedRefresh(t *testing.T) {
	srv := mcptest.New()
	hs := httptest.NewServer(srv.HTTPHandler(mcptest.HTTPOptions{}))
	defer hs.Close()
	m, reg := newManager(t)
	startAll(t, m, Entry{Name: "web", Config: ServerConfig{URL: hs.URL}})
	if _, ok := reg.Get("mcp__web__late"); ok {
		t.Fatal("late should not exist yet")
	}
	srv.AddTool(map[string]any{"name": "late", "description": "Added later"})
	m.mu.Lock()
	s := m.servers["web"]
	m.mu.Unlock()
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	c.handle(&Message{Method: "notifications/tools/list_changed"})
	waitFor(t, func() bool { _, ok := reg.Get("mcp__web__late"); return ok })
	if _, ok := reg.Get("mcp__web__echo"); !ok {
		t.Fatal("existing tools stay")
	}
}

func TestLogPrefixAndServerStderr(t *testing.T) {
	m, _ := newManager(t)
	var sb lockedBuf
	m.Log = &sb
	script := `echo "starting up" >&2; read l; ` + shellInitReply + `; read l; sleep 5`
	startAll(t, m, Entry{Name: "logger", Config: ServerConfig{Type: "stdio", Command: "sh", Args: []string{"-c", script}}})
	waitFor(t, func() bool { return strings.Contains(sb.String(), "[logger] starting up\n") })
}

func TestStdioCwdAndEnv(t *testing.T) {
	m, _ := newManager(t)
	home := m.Home
	dir := filepath.Join(home, "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(home, "pwd.txt")
	script := `pwd > ` + out + `; echo "$KOINE_T_VAR" >> ` + out + `; read l; ` + shellInitReply + `; read l; sleep 5`
	startAll(t, m, Entry{Name: "cwd", Config: ServerConfig{Type: "stdio", Command: "sh", Args: []string{"-c", script}, Cwd: "~/work",
		Env: map[string]string{"KOINE_T_VAR": "!printf viaCommand"}}})
	b, _ := os.ReadFile(out)
	lines := strings.Fields(string(b))
	resolved, _ := filepath.EvalSymlinks(dir)
	if len(lines) != 2 || (lines[0] != dir && lines[0] != resolved) || lines[1] != "viaCommand" {
		t.Fatalf("server saw %q (state %+v)", b, m.Statuses())
	}
	rel := stdioEntry("rel", func(c *ServerConfig) { c.Cwd = "sub" })
	tr, err := m.transport(context.Background(), &Server{Name: "rel", Entry: rel})
	if err != nil || tr.(*stdioTransport).cfg.Dir != filepath.Join(m.Cwd, "sub") {
		t.Fatalf("relative cwd: %v %v", tr, err)
	}
	if expandHome("~", "/h") != "/h" || expandHome("~/x", "/h") != "/h/x" || expandHome("/a", "/h") != "/a" {
		t.Fatal("expandHome")
	}
}

func TestStatusSorted(t *testing.T) {
	m, _ := newManager(t)
	startAll(t, m, stdioEntry("b", nil), stdioEntry("a", nil))
	var names []string
	for _, s := range m.Statuses() {
		names = append(names, s.Name)
	}
	if !sort.StringsAreSorted(names) || len(names) != 2 {
		t.Fatalf("names = %v", names)
	}
}

func TestStartWaitsAtMostWait(t *testing.T) {
	m, _ := newManager(t)
	start := time.Now()
	m.Start(context.Background(), []Entry{stdioEntry("slow", func(c *ServerConfig) {
		c.Command, c.Args = "sh", []string{"-c", "sleep 3"}
	})}, 100*time.Millisecond)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Start waited %s", d)
	}
	if m.Statuses()[0].State != StateConnecting {
		t.Fatalf("state = %s", m.Statuses()[0].State)
	}
	m.Close() // must not hang on a server that never answered
}

func TestErrorTypes(t *testing.T) {
	if !errors.Is(ErrClosed, ErrClosed) || (&RPCError{Code: 5, Message: "m"}).Error() != "mcp error 5: m" {
		t.Fatal("errors")
	}
	if (Problem{File: "f", Msg: "m"}).String() != "f: m" {
		t.Fatal("problem")
	}
}
