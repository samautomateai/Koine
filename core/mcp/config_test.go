package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func byName(l Loaded, name string) *Entry {
	for i := range l.Servers {
		if l.Servers[i].Name == name {
			return &l.Servers[i]
		}
	}
	return nil
}

func problems(l Loaded) string {
	var sb strings.Builder
	for _, p := range l.Problems {
		sb.WriteString(p.String() + "\n")
	}
	return sb.String()
}

func TestLoadUserAndProject(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("KOINE_AGENT_DIR", "")
	write(t, filepath.Join(home, ".koine", "mcp.json"), `{"mcpServers":{
		"files":{"command":"npx","args":["-y","fs","."],"env":{"A":"${A}"},"timeout":5,"description":"File access"},
		"docs":{"url":"https://docs.example/mcp","headers":{"Authorization":"!echo Bearer x"}},
		"later":{"type":"streamable-http","url":"https://later.example/mcp","enabled":false}}}`)
	write(t, filepath.Join(cwd, ".koine", "mcp.json"), `{"mcpServers":{
		"files":{"enabled":false},
		"docs":{"exposure":"deferred","toolExposure":{"search_*":"direct","search_x":"hidden","*":"deferred"}},
		"proj":{"command":"./srv","cwd":"sub"}}}`)
	write(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers":{"claude":{"command":"c","alwaysAllow":["x"]}}}`)

	got := Load(cwd, home, true)
	if got.Skipped != 0 || len(got.Problems) != 0 {
		t.Fatalf("problems: %s skipped %d", problems(got), got.Skipped)
	}
	if len(got.Servers) != 5 {
		t.Fatalf("servers = %+v", got.Servers)
	}
	f := byName(got, "files")
	if f.Config.Command != "npx" || !f.Disabled() || f.Config.Timeout != 5 || f.Config.Description != "File access" || f.Source != SourceUser {
		t.Fatalf("files = %+v (an enabled-only project entry keeps the user's command)", f)
	}
	d := byName(got, "docs")
	if d.Config.Type != "http" || d.Config.Exposure != "deferred" || d.Config.Headers["Authorization"] == "" {
		t.Fatalf("docs = %+v", d)
	}
	te := d.Config.ToolExposure
	if te.ModeFor("search_x", "mcp__docs__search_x", "d") != "hidden" || te.ModeFor("search_y", "", "d") != "direct" ||
		te.ModeFor("other", "", "d") != "deferred" || te.ModeFor("search_x", "", "d") != "hidden" {
		t.Fatalf("toolExposure order/exact rules: %+v", te)
	}
	if p := byName(got, "proj"); p.Source != SourceProject || p.Config.Type != "stdio" {
		t.Fatalf("proj = %+v", p)
	}
	if c := byName(got, "claude"); c == nil || c.Config.Command != "c" {
		t.Fatalf("claude = %+v", c)
	}
	if byName(got, "later").Config.Type != "http" || !byName(got, "later").Disabled() {
		t.Fatal("later")
	}

	// untrusted: nothing from the project is acted on, but it is counted
	un := Load(cwd, home, false)
	if un.Skipped != 4 || byName(un, "proj") != nil || byName(un, "claude") != nil {
		t.Fatalf("untrusted: skipped %d servers %+v", un.Skipped, un.Servers)
	}
	if f := byName(un, "files"); f.Disabled() || f.Source != SourceUser {
		t.Fatalf("an untrusted project must not switch off or change user servers: %+v", f)
	}
}

func TestLoadProblems(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("KOINE_AGENT_DIR", "")
	write(t, filepath.Join(home, ".koine", "mcp.json"), `{"mcpServers":{
		"bad name":{"command":"x"},
		"legacy":{"type":"sse","url":"https://x/sse"},
		"both":{"command":"x","url":"https://x"},
		"neither":{},
		"weird":{"type":"carrier-pigeon","command":"x"},
		"stdio-url":{"type":"stdio","url":"https://x"},
		"http-cmd":{"type":"http","command":"x"},
		"badurl":{"url":"ftp://x"},
		"expo":{"command":"x","exposure":"sometimes"},
		"toolexpo":{"command":"x","toolExposure":{"a":"never"}},
		"neg":{"command":"x","timeout":-1},
		"a-b":{"command":"x"},
		"a_b":{"command":"y"},
		"notobj":3,
		"ok":{"command":"fine"}}}`)
	write(t, filepath.Join(cwd, ".koine", "mcp.json"), `{not json`)
	write(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers":{"only-override":{"enabled":false}}}`)
	got := Load(cwd, home, true)
	if byName(got, "ok") == nil || byName(got, "a-b") == nil || byName(got, "a_b") != nil {
		t.Fatalf("servers = %+v", got.Servers)
	}
	msg := problems(got)
	for _, want := range []string{`"bad name"`, "legacy SSE", "both command and url", "needs a command", "unknown type", `"stdio" needs a command`,
		`"http" needs a url`, "must start with http", "unknown exposure", "toolExposure", "negative", "same server", "entry must be an object", "invalid JSON",
		`"only-override"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("problems lack %q:\n%s", want, msg)
		}
	}
}

func TestToolExposureJSON(t *testing.T) {
	var te ToolExposure
	if err := json.Unmarshal([]byte(`{"b":"direct","a*":"hidden","b":"deferred"}`), &te); err != nil {
		t.Fatal(err)
	}
	if len(te.Keys) != 2 || te.Mode["b"] != "deferred" {
		t.Fatalf("%+v", te)
	}
	out, _ := json.Marshal(te)
	if string(out) != `{"b":"deferred","a*":"hidden"}` {
		t.Fatalf("marshal = %s", out)
	}
	for _, bad := range []string{`[]`, `{"a":1}`, `"x"`} {
		if json.Unmarshal([]byte(bad), &te) == nil {
			t.Errorf("%s should not parse", bad)
		}
	}
	if !globMatch("mcp__*__s?arch", "mcp__x__search") || globMatch("a*", "ba") || !globMatch("a.b", "a.b") || globMatch("a.b", "axb") {
		t.Fatal("globMatch")
	}
}

func TestOpencodeAndAgentDirOverride(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("KOINE_AGENT_DIR", filepath.Join(home, "agent"))
	write(t, filepath.Join(cwd, "opencode.json"), `{
	  // comments and trailing commas are allowed in opencode.json
	  "mcp": {
	    "loc": {"type":"local","command":["node","srv.js","--x"],"environment":{"K":"{env:KEY}"},"enabled":true,"timeout":5000,},
	    "rem": {"type":"remote","url":"https://r.example/mcp","headers":{"Authorization":"Bearer {env:TOK}"} /* c */},
	    "skip": {"type":"local","command":[]},
	    "other": {"type":"weird"}
	  }}`)
	write(t, filepath.Join(home, "agent", "mcp.json"), `{"mcpServers":{"u":{"command":"u"}}}`)
	got := Load(cwd, home, true)
	loc := byName(got, "loc")
	if loc == nil || loc.Config.Command != "node" || len(loc.Config.Args) != 2 || loc.Config.Env["K"] != "${KEY}" || loc.Config.Timeout != 5 {
		t.Fatalf("loc = %+v\n%s", loc, problems(got))
	}
	if r := byName(got, "rem"); r == nil || r.Config.Headers["Authorization"] != "Bearer ${TOK}" || r.Config.Type != "http" {
		t.Fatalf("rem = %+v", r)
	}
	if byName(got, "u") == nil || byName(got, "skip") != nil {
		t.Fatal("agent dir file or skipped entries")
	}
	if Load(cwd, home, false).Skipped != 2 {
		t.Fatal("untrusted opencode.json servers should be counted")
	}
	if UserFile(home) != filepath.Join(home, "agent", "mcp.json") || ProjectFile(cwd) != filepath.Join(cwd, ".koine", "mcp.json") {
		t.Fatal("file locations")
	}
}

func TestResolver(t *testing.T) {
	ctx := context.Background()
	r := Resolver{Getenv: func(k string) string { return map[string]string{"TOK": "abc", "EMPTY": ""}[k] },
		Run: func(_ context.Context, cmd, _ string) (string, error) {
			if cmd == "boom" {
				return "", os.ErrPermission
			}
			return "ran " + cmd + "\n\n", nil
		}}
	cases := map[string]string{"plain": "plain", "Bearer ${TOK}": "Bearer abc", "${EMPTY:-dflt}": "dflt", "${NOPE:-x}-${TOK}": "x-abc",
		"!echo hi": "ran echo hi"}
	for in, want := range cases {
		if got, err := r.Resolve(ctx, in); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := r.Resolve(ctx, "x ${NOPE} ${TOK}"); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("unset variable: %v", err)
	}
	if _, err := r.Resolve(ctx, "!boom"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command: %v", err)
	}
	if m, err := r.ResolveMap(ctx, map[string]string{"A": "${TOK}"}); err != nil || m["A"] != "abc" {
		t.Errorf("map = %v, %v", m, err)
	}
	if _, err := r.ResolveMap(ctx, map[string]string{"A": "${NOPE}"}); err == nil || !strings.Contains(err.Error(), "A:") {
		t.Errorf("map error = %v", err)
	}
	if m, err := r.ResolveMap(ctx, nil); m != nil || err != nil {
		t.Error("nil map")
	}
}

func TestRunShellCommands(t *testing.T) {
	out, err := Resolver{Dir: t.TempDir()}.Resolve(context.Background(), "!printf 'secret\\n'")
	if err != nil || out != "secret" {
		t.Fatalf("out = %q, %v", out, err)
	}
	_, err = Resolver{}.Resolve(context.Background(), "!echo nope >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}
