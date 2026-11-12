package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/mcp"
	"koine/core/mcp/mcptest"
)

// TestMCPCLIHelper is the go-test helper-process pattern: with the variable set, the test binary is an MCP stdio server.
func TestMCPCLIHelper(t *testing.T) {
	if os.Getenv(mcptest.EnvStdio) == "" {
		t.Skip("helper process")
	}
	mcptest.Main()
}

// mcpEnv gives the test its own home and project and returns them.
func mcpEnv(t *testing.T) (home, proj string) {
	t.Helper()
	home, proj = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KOINE_AGENT_DIR", "")
	t.Setenv("KOINE_TRUST", "")
	return home, proj
}

func mcpRun(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(append([]string{"mcp"}, args...), streams{In: strings.NewReader(""), Out: &o, Err: &e})
	return code, o.String(), e.String()
}

func helperServerJSON(names ...string) string {
	servers := map[string]any{}
	for _, n := range names {
		servers[n] = map[string]any{"command": os.Args[0], "args": []string{"-test.run=^TestMCPCLIHelper$"},
			"env": map[string]string{mcptest.EnvStdio: "1", "GORACE": "atexit_sleep_ms=0"}}
	}
	b, _ := json.Marshal(map[string]any{"mcpServers": servers})
	return string(b)
}

func TestMCPAddRemoveAndUsage(t *testing.T) {
	home, proj := mcpEnv(t)
	code, out, errOut := mcpRun(t, "add", "files", "--env", "A=1", "--env=B=${B}", "--timeout", "30", "--description", "Files", "--exposure", "deferred",
		"--cwd", "sub", "--", "npx", "-y", "server-fs", ".")
	if code != 0 || !strings.Contains(out, "added files to "+filepath.Join(home, ".koine", "mcp.json")) {
		t.Fatalf("add: %d %q %q", code, out, errOut)
	}
	code, out, errOut = mcpRun(t, "add", "-C", proj, "docs", "--url", "https://d.example/mcp", "--bearer-token-env-var", "DOCS_TOKEN",
		"--header", "X-A=b", "-l")
	if code != 0 {
		t.Fatalf("add --local: %d %q %q", code, out, errOut)
	}
	user, _ := os.ReadFile(filepath.Join(home, ".koine", "mcp.json"))
	local, _ := os.ReadFile(filepath.Join(proj, ".koine", "mcp.json"))
	for _, want := range []string{`"command": "npx"`, `"-y"`, `"timeout": 30`, `"exposure": "deferred"`, `"cwd": "sub"`, `"B": "${B}"`} {
		if !strings.Contains(string(user), want) {
			t.Errorf("user file lacks %s:\n%s", want, user)
		}
	}
	if !strings.Contains(string(local), `"Authorization": "Bearer ${DOCS_TOKEN}"`) || !strings.Contains(string(local), `"url": "https://d.example/mcp"`) ||
		strings.Contains(string(user), "d.example") {
		t.Fatalf("local file:\n%s", local)
	}
	if code, _, errOut = mcpRun(t, "remove", "files"); code != 0 {
		t.Fatalf("remove: %d %q", code, errOut)
	}
	if code, _, errOut = mcpRun(t, "rm", "files"); code != 1 || !strings.Contains(errOut, `no server named "files"`) {
		t.Fatalf("second remove: %d %q", code, errOut)
	}
	if code, _, _ = mcpRun(t, "remove", "-C", proj, "docs", "--local"); code != 0 {
		t.Fatal("remove --local")
	}

	for name, args := range map[string][]string{
		"no command":       {},
		"unknown":          {"frobnicate"},
		"add no name":      {"add", "--url", "https://x"},
		"add nothing":      {"add", "n"},
		"add both":         {"add", "n", "--url", "https://x", "--", "cmd"},
		"bad kv":           {"add", "n", "--env", "NOEQUALS", "--", "cmd"},
		"bad header":       {"add", "n", "--url", "https://x", "--header", "=v"},
		"unknown option":   {"add", "n", "--nope"},
		"missing value":    {"add", "n", "--url"},
		"bad timeout":      {"add", "n", "--timeout", "soon", "--", "c"},
		"negative timeout": {"add", "n", "--timeout=-1", "--", "c"},
		"remove no name":   {"remove"},
		"logout no name":   {"logout"},
		"login no name":    {"login"},
	} {
		if code, _, _ := mcpRun(t, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
	if code, out, _ = mcpRun(t, "--help"); code != 0 || !strings.Contains(out, "koine mcp <command>") {
		t.Fatalf("help: %d %q", code, out)
	}
	if code, _, _ = mcpRun(t, "add", "bad name", "--", "c"); code != 1 {
		t.Fatalf("invalid name: exit %d, want 1", code)
	}
	if code, _, _ = mcpRun(t, "add", "x", "--url", "ftp://x"); code != 1 {
		t.Fatalf("invalid url: exit %d, want 1", code)
	}
	if !strings.Contains(usage, "koine mcp add|remove|list|login|logout") {
		t.Fatal("usage text should mention the subcommand")
	}
}

func TestMCPList(t *testing.T) {
	home, proj := mcpEnv(t)
	mcpFile := filepath.Join(home, ".koine", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcpFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := mcpRun(t, "list", "-C", proj); code != 0 || !strings.Contains(out, "no MCP servers configured") {
		t.Fatalf("empty list: %d %q", code, out)
	}
	if err := os.WriteFile(mcpFile, []byte(helperServerJSON("good")), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := mcpRun(t, "list", "-C", proj)
	if code != 0 || !strings.Contains(out, "good") || !strings.Contains(out, "connected") || !strings.Contains(out, "9") {
		t.Fatalf("list: %d %q %q", code, out, errOut)
	}
	// a project override of one tool's exposure is marked; the project is untrusted until -a
	projFile := filepath.Join(proj, ".koine", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projFile), 0o755); err != nil {
		t.Fatal(err)
	}
	override := `{"mcpServers":{"good":{"toolExposure":{"echo":"hidden"}},"repo":{"command":"/nonexistent/prog"}}}`
	if err := os.WriteFile(projFile, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = mcpRun(t, "list", "-C", proj)
	if code != 0 || !strings.Contains(errOut, "2 project server(s) not started") || strings.Contains(out, "repo") {
		t.Fatalf("untrusted list: %d %q %q", code, out, errOut)
	}
	code, out, errOut = mcpRun(t, "list", "-C", proj, "-a")
	if code != 1 || !strings.Contains(out, "echo=hidden (differs from the server's direct)") || !strings.Contains(out, "failed") ||
		!strings.Contains(errOut, "repo:") {
		t.Fatalf("trusted list: %d %q %q", code, out, errOut)
	}
	// invalid entries fail the command too
	if err := os.WriteFile(projFile, []byte(`{"mcpServers":{"legacy":{"type":"sse","url":"https://x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut = mcpRun(t, "list", "-C", proj); code != 0 || !strings.Contains(errOut, "1 project server(s) not started") {
		t.Fatalf("list with an untrusted invalid project file: %d %q", code, errOut) // untrusted files are counted, never interpreted
	}
	if code, _, errOut = mcpRun(t, "list", "-C", proj, "--trust"); code != 1 || !strings.Contains(errOut, "legacy SSE") {
		t.Fatalf("trusted invalid: %d %q", code, errOut)
	}
	t.Setenv("KOINE_TRUST", "1")
	if code, _, _ = mcpRun(t, "ls", "-C", proj); code != 1 {
		t.Fatalf("KOINE_TRUST list: %d", code)
	}
}

// fakeOAuthServer is an MCP endpoint that wants a bearer token, with just enough authorization server around it to sign in.
func fakeOAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	mcpH := mcptest.New().HTTPHandler(mcptest.HTTPOptions{})
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/prm"`)
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		mcpH.ServeHTTP(w, r)
	})
	mux.HandleFunc("/prm", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{srv.URL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token",
			"registration_endpoint": srv.URL + "/register"})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"cid"}`))
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		cb, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
		q := cb.Query()
		q.Set("code", "c1")
		q.Set("state", r.URL.Query().Get("state"))
		cb.RawQuery = q.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","refresh_token":"r","expires_in":3600}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPLoginAndLogout(t *testing.T) {
	home, proj := mcpEnv(t)
	srv := fakeOAuthServer(t)
	old := mcpOpenBrowser
	mcpOpenBrowser = func(u string) error {
		go func() {
			if resp, err := http.Get(u); err == nil { //nolint:noctx // test URL
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	defer func() { mcpOpenBrowser = old }()
	if code, _, errOut := mcpRun(t, "add", "remote", "--url", srv.URL+"/mcp"); code != 0 {
		t.Fatalf("add: %d %q", code, errOut)
	}
	if code, _, errOut := mcpRun(t, "list", "-C", proj); code != 1 || !strings.Contains(errOut, "koine mcp login remote") {
		t.Fatalf("list before login: %d %q", code, errOut)
	}
	code, out, errOut := mcpRun(t, "login", "-C", proj, "remote")
	if code != 0 || !strings.Contains(out, "signed in to remote") || !strings.Contains(errOut, "/authorize?") {
		t.Fatalf("login: %d %q %q", code, out, errOut)
	}
	info, err := os.Stat(mcp.AuthFile(home))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", info, err)
	}
	if code, out, errOut = mcpRun(t, "list", "-C", proj); code != 0 || !strings.Contains(out, "connected") {
		t.Fatalf("list after login: %d %q %q", code, out, errOut)
	}
	if code, out, _ = mcpRun(t, "logout", "remote"); code != 0 || !strings.Contains(out, "signed out of remote") {
		t.Fatalf("logout: %d %q", code, out)
	}
	if code, out, _ = mcpRun(t, "logout", "remote"); code != 0 || !strings.Contains(out, "was not signed in") {
		t.Fatalf("second logout: %d %q", code, out)
	}
	if err := os.WriteFile(mcp.AuthFile(home), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = mcpRun(t, "logout", "remote"); code != 1 {
		t.Fatalf("logout with a corrupt token file: %d", code)
	}

	// refusals
	if err := os.WriteFile(filepath.Join(home, ".koine", "mcp.json"), []byte(`{"mcpServers":{
		"local":{"command":"x"},"hdr":{"url":"https://x/mcp","headers":{"authorization":"Bearer z"}},
		"bad":{"url":"${KOINE_UNSET_FOR_CLI_TEST}"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"local": "stdio server", "hdr": "own Authorization header", "nope": "no server named", "bad": "url:"} {
		if code, _, errOut = mcpRun(t, "login", "-C", proj, name); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("login %s: %d %q, want %q", name, code, errOut, want)
		}
	}
}
