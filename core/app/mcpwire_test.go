package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/core/agent"
	"koine/core/mcp"
	"koine/core/mcp/mcptest"
)

// TestMCPHelper is not a test: when the environment asks for it, the test binary runs as an MCP server on stdio (the go-test helper-process
// pattern), so the app tests can start a real stdio server without a second binary.
func TestMCPHelper(t *testing.T) {
	if os.Getenv(mcptest.EnvStdio) == "" {
		t.Skip("helper process")
	}
	mcptest.Main()
}

// mcpJSON is an mcp.json with one stdio server that runs this test binary as the helper.
func mcpJSON(t *testing.T, names ...string) string {
	t.Helper()
	servers := map[string]any{}
	for _, n := range names {
		servers[n] = map[string]any{"command": os.Args[0], "args": []string{"-test.run=^TestMCPHelper$"},
			"env": map[string]string{mcptest.EnvStdio: "1", "GORACE": "atexit_sleep_ms=0"}}
	}
	b, _ := json.Marshal(map[string]any{"mcpServers": servers})
	return string(b)
}

func noticeText(evs []agent.Event) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Text)
	}
	return strings.Join(out, "\n")
}

func TestMCPUserServerToolsAndSlashCommand(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".koine", "mcp.json"), mcpJSON(t, "demo"))
	a, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tool, ok := a.Tools.Get("mcp__demo__echo")
	if !ok {
		t.Fatalf("tools = %v; warnings %v", a.Tools.Names(), a.Warnings)
	}
	if out, err := tool.Run(context.Background(), json.RawMessage(`{"text":"via app"}`)); err != nil || out != "echo: via app" {
		t.Fatalf("out = %q, %v", out, err)
	}
	if _, ok := a.Tools.Get(mcp.ListResourcesName); !ok {
		t.Fatal("resource tools expected")
	}
	if st := a.MCPStatuses(); len(st) != 1 || st[0].State != mcp.StateConnected {
		t.Fatalf("statuses = %+v", st)
	}
	if out := noticeText(runEvents(t, a, "/mcp")); !strings.Contains(out, "demo: connected, 9 tools, exposure direct, user") {
		t.Fatalf("/mcp = %q", out)
	}
	if out := noticeText(runEvents(t, a, "/mcp reconnect demo")); !strings.Contains(out, "demo reconnected") {
		t.Fatalf("/mcp reconnect = %q", out)
	}
	if out := noticeText(runEvents(t, a, "/mcp reconnect nope")); !strings.Contains(out, "no MCP server named") {
		t.Fatalf("/mcp reconnect nope = %q", out)
	}
	if out := noticeText(runEvents(t, a, "/mcp reconnect")); !strings.Contains(out, "usage") {
		t.Fatalf("/mcp reconnect (bare) = %q", out)
	}
	if out := noticeText(runEvents(t, a, "/mcp login x")); !strings.Contains(out, "koine mcp login") {
		t.Fatalf("/mcp login = %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".koine", "mcp.log")); err != nil {
		t.Fatalf("server log: %v", err)
	}
	// /reload replaces the servers instead of adding to them
	a.Reload(context.Background())
	n := 0
	for _, name := range a.Tools.Names() {
		if strings.HasPrefix(name, "mcp__demo__echo") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("echo registered %d times after reload", n)
	}
}

func TestMCPToolSelectionPattern(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".koine", "mcp.json"), mcpJSON(t, "demo"))
	a, err := NewWithOptions(context.Background(), t.TempDir(), Options{RunOptions: RunOptions{Tools: []string{"mcp__demo__*"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	names := a.Agent.Tools.Names()
	if len(names) != 9 || names[0] != "mcp__demo__annotated" {
		t.Fatalf("selected = %v", names)
	}
}

func TestMCPProjectServersNeedTrust(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_TRUST", "")
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".koine", "mcp.json"), mcpJSON(t, "repo"))
	writeFile(t, filepath.Join(proj, ".mcp.json"), mcpJSON(t, "claude"))
	a, err := New(context.Background(), proj)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, ok := a.Tools.Get("mcp__repo__echo"); ok || len(a.MCPStatuses()) != 0 {
		t.Fatalf("an untrusted project must not start its MCP servers: %v", a.Tools.Names())
	}
	if w := strings.Join(a.Warnings, "\n"); !strings.Contains(w, "2 project MCP server(s) NOT started") {
		t.Fatalf("warnings = %q", w)
	}
	if err := a.TrustProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"mcp__repo__echo", "mcp__claude__echo"} {
		if _, ok := a.Tools.Get(n); !ok {
			t.Fatalf("after /trust %s is missing: %v", n, a.Tools.Names())
		}
	}
}

func TestMCPProblemsAndFailuresAreWarnings(t *testing.T) {
	isolate(t)
	old := mcp.ConnectWait
	mcp.ConnectWait = 300 * time.Millisecond
	defer func() { mcp.ConnectWait = old }()
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".koine", "mcp.json"), (`{"mcpServers":{
		"legacy":{"type":"sse","url":"https://x/sse"},
		"missing":{"command":"/nonexistent/mcp-server"},
		"slow":{"command":"sh","args":["-c","sleep 5"]}}}`))
	a, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := strings.Join(a.Warnings, "\n")
	if !strings.Contains(w, `mcp: `) || !strings.Contains(w, "legacy SSE") || !strings.Contains(w, "server missing") {
		t.Fatalf("warnings = %q", w)
	}
	if info := strings.Join(a.Info, "\n"); !strings.Contains(info, "server slow is still connecting") {
		t.Fatalf("info = %q", info)
	}
	if out := noticeText(runEvents(t, a, "/mcp")); !strings.Contains(out, "missing: failed") || !strings.Contains(out, "slow: connecting") {
		t.Fatalf("/mcp = %q", out)
	}
}

func TestMCPNoServersConfigured(t *testing.T) {
	isolate(t)
	a, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.MCPStatuses() != nil {
		t.Fatal("no manager without servers")
	}
	if out := noticeText(runEvents(t, a, "/mcp")); !strings.Contains(out, "no MCP servers configured") {
		t.Fatalf("/mcp = %q", out)
	}
	if out := noticeText(runEvents(t, a, "/mcp reconnect x")); !strings.Contains(out, "usage") {
		t.Fatalf("/mcp reconnect = %q", out)
	}
	if mcpSkipped := a.startMCP(context.Background()); mcpSkipped != 0 {
		t.Fatal("skipped")
	}
}
