package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"koine/core/app"
	"koine/core/llm"
)

type say string

func (s say) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: []llm.Block{{Type: "text", Text: string(s)}}}, nil
}

func newServer(t *testing.T) (*Server, *httptest.Server) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CACHE_HOME", h)
	a, err := app.New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.Agent.Provider = say("hello from model")
	t.Cleanup(a.Close)
	s := &Server{App: a}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestInfo(t *testing.T) {
	_, ts := newServer(t)
	r, err := http.Get(ts.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	b, _ := io.ReadAll(r.Body)
	if r.Header.Get("content-type") != "application/json" || !strings.Contains(string(b), `"tools"`) || !strings.Contains(string(b), "bash") {
		t.Fatalf("%s", b)
	}
}

func TestPromptStreamsSSE(t *testing.T) {
	_, ts := newServer(t)
	r, err := http.Post(ts.URL+"/api/prompt", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	b, _ := io.ReadAll(r.Body)
	body := string(b)
	ct := r.Header.Get("content-type")
	if ct != "text/event-stream" || !strings.Contains(body, `data: {"type":"text","text":"hello from model"}`) ||
		!strings.HasSuffix(strings.TrimSpace(body), `{"type":"done"}`) {
		t.Fatalf("%q", body)
	}
}

func TestPromptSlashCommandOverHTTP(t *testing.T) {
	_, ts := newServer(t)
	r, _ := http.Post(ts.URL+"/api/prompt", "application/json", strings.NewReader(`{"text":"/tools"}`))
	defer func() { _ = r.Body.Close() }()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "bash") {
		t.Fatalf("%s", b)
	}
}

func TestPromptRejectsBadBody(t *testing.T) {
	_, ts := newServer(t)
	for _, body := range []string{`{`, `{"text":""}`, `{}`} {
		r, _ := http.Post(ts.URL+"/api/prompt", "application/json", strings.NewReader(body))
		_ = r.Body.Close()
		if r.StatusCode != 400 {
			t.Errorf("%q -> %d", body, r.StatusCode)
		}
	}
}

func TestConcurrentRunRejected(t *testing.T) {
	s, ts := newServer(t)
	s.mu.Lock() // simulate a run in flight
	r, _ := http.Post(ts.URL+"/api/prompt", "application/json", strings.NewReader(`{"text":"hi"}`))
	s.mu.Unlock()
	_ = r.Body.Close()
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("%d", r.StatusCode)
	}
}
