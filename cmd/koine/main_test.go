package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/compat"
)

// fakeAnthropic speaks just enough of the Messages API: first call asks for
// tools, second call (after tool results) answers with their content.
func fakeAnthropic(t *testing.T, seen *[]map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		*seen = append(*seen, req)
		msgs := req["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)["content"].([]any)
		w.Header().Set("content-type", "application/json")
		if last[0].(map[string]any)["type"] == "tool_result" {
			var parts []string
			for _, c := range last {
				parts = append(parts, c.(map[string]any)["content"].(string))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "RESULTS: " + strings.Join(parts, " | ")}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "tool_use", "content": []any{
			map[string]any{"type": "tool_use", "id": "a", "name": "pi_echo", "input": map[string]any{"text": "hey"}},
			map[string]any{"type": "tool_use", "id": "b", "name": "oc_upper", "input": map[string]any{"text": "yo"}},
			map[string]any{"type": "tool_use", "id": "c", "name": "py_add", "input": map[string]any{"a": 20, "b": 22}},
			map[string]any{"type": "tool_use", "id": "d", "name": "bash", "input": map[string]any{"command": "echo hi from bash"}},
		}})
	}))
}

func project(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "../../core/app/testdata/proj"
	_ = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		rel, _ := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return nil
		}
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(dst, rel), b, info.Mode())
	})
	return dst
}

func isolate(t *testing.T, api string) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CACHE_HOME", h)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", api)
	t.Setenv("KOINE_TRUST", "") // trust must be explicit (-trust)
}

func TestEndToEndPrompt(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)

	// capture stdout (tui.Render prints there)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	var stderr bytes.Buffer
	code := run([]string{"-C", project(t), "-trust", "-p", "do everything"}, &stderr)
	_ = w.Close()
	os.Stdout = old
	outB, _ := io.ReadAll(r)
	out := string(outB)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	// Pi tool, opencode tool, Python tool and a built-in all ran through the real provider
	for _, want := range []string{"pi:hey", "YO@", "42", "hi from bash", "RESULTS:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// the system prompt carried the skill and the sub-agent
	sys, _ := seen[0]["system"].(string)
	if !strings.Contains(sys, "<name>greeter</name>") || !strings.Contains(sys, "reviewer") {
		t.Errorf("system prompt: %s", sys)
	}
	if got := len(seen[0]["tools"].([]any)); got < 10 {
		t.Errorf("expected builtins + extension tools advertised, got %d", got)
	}
}

func TestServeMode(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() { done <- run([]string{"-C", t.TempDir(), "-serve", addr}, &stderr) }()
	var resp *http.Response
	var err error
	for i := 0; i < 100; i++ {
		if resp, err = http.Get("http://" + addr + "/api/info"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"tools"`) {
		t.Fatalf("%s", b)
	}
	// occupy-port failure path
	if code := run([]string{"-C", t.TempDir(), "-serve", addr}, &stderr); code != 1 {
		t.Errorf("second server on same port should fail, got %d", code)
	}
}

func TestInteractiveFallsThroughToTUI(t *testing.T) {
	isolate(t, "http://127.0.0.1:1")
	oldIn, oldOut := os.Stdin, os.Stdout
	r, w, _ := os.Pipe()
	os.Stdin = r
	_ = w.Close() // immediate EOF
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devnull
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	var stderr bytes.Buffer
	if code := run([]string{"-C", t.TempDir()}, &stderr); code != 0 {
		t.Fatalf("%d %s", code, stderr.String())
	}
}

func TestBadFlagAndAPIFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"-nope"}, &stderr); code != 2 {
		t.Fatalf("bad flag: %d", code)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }))
	defer bad.Close()
	isolate(t, bad.URL) // 4xx is not retried: provider error -> non-zero exit
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	old := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = old }()
	if code := run([]string{"-C", t.TempDir(), "-p", "hi"}, &stderr); code != 1 {
		t.Fatalf("api failure should exit 1, got %d", code)
	}
}

func TestVersionAndUntrustedByDefault(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	var stderr bytes.Buffer
	code := run([]string{"-version"}, &stderr)
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	if code != 0 || strings.TrimSpace(string(b)) != "koine dev+pi"+piBaseline {
		t.Fatalf("%d %q", code, b)
	}

	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	t.Setenv("KOINE_TRUST", "")
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devnull
	defer func() { os.Stdout = old }()
	run([]string{"-C", project(t), "-p", "go"}, &stderr)
	for _, td := range seen[0]["tools"].([]any) {
		if n := td.(map[string]any)["name"]; n == "pi_echo" || n == "oc_upper" || n == "py_add" {
			t.Errorf("untrusted project tool %v was advertised to the model", n)
		}
	}
}

// The version printed by -version must be the one the compat manifest is pinned to.
func TestPiBaselineMatchesManifest(t *testing.T) {
	m, err := compat.Load()
	if err != nil {
		t.Fatal(err)
	}
	if piBaseline != m.Upstream.Baseline {
		t.Fatalf("main.piBaseline=%s but compat/pi.json baseline=%s — update both (task upstream:check --rebase)", piBaseline, m.Upstream.Baseline)
	}
}

func TestCompatFlagPrintsReport(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	var stderr bytes.Buffer
	code := run([]string{"-compat"}, &stderr)
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	if code != 0 || !strings.Contains(string(b), "pinned to") || !strings.Contains(string(b), piBaseline) {
		t.Fatalf("%d %q %s", code, b, stderr.String())
	}
}
