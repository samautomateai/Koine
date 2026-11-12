package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"koine/core/mcp/mcptest"
)

func httpClient(t *testing.T, h http.Handler, cfg HTTPConfig) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.URL = srv.URL
	cfg.RetryDelay = time.Millisecond
	c := NewClient(NewHTTP(cfg))
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestHTTPJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		c, _ := httpClient(t, mcptest.New().HTTPHandler(mcptest.HTTPOptions{SSE: sse}), HTTPConfig{Headers: map[string]string{"X-Test": "1"}})
		ctx := context.Background()
		tl, err := c.ListTools(ctx)
		if err != nil || len(tl) != 9 {
			t.Fatalf("sse=%v tools = %d, %v", sse, len(tl), err)
		}
		res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"over http"}`))
		if err != nil || res.Content[0].Text != "echo: over http" {
			t.Fatalf("sse=%v echo = %+v, %v", sse, res, err)
		}
		if sse { // progress notifications arrive in the same stream as the response
			c.Timeout = 5 * time.Second
			if res, err := c.CallTool(ctx, "progress", nil); err != nil || res.Content[0].Text != "progress done" {
				t.Fatalf("progress = %+v, %v", res, err)
			}
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if c.T.Alive() {
			t.Fatal("closed transport should not be alive")
		}
	}
}

func TestHTTPSendsSessionAndVersionHeaders(t *testing.T) {
	var seen []http.Header
	inner := mcptest.New().HTTPHandler(mcptest.HTTPOptions{})
	c, _ := httpClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		inner.ServeHTTP(w, r)
	}), HTTPConfig{})
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := seen[len(seen)-1]
	if last.Get("Mcp-Session-Id") != "sess-1" || last.Get("MCP-Protocol-Version") != "2025-06-18" {
		t.Fatalf("headers = %v", last)
	}
	if !strings.Contains(last.Get("Accept"), "text/event-stream") {
		t.Fatalf("accept = %q", last.Get("Accept"))
	}
}

func TestHTTPRetriesTransientFailures(t *testing.T) {
	c, _ := httpClient(t, mcptest.New().HTTPHandler(mcptest.HTTPOptions{FailFirst: 2}), HTTPConfig{}) // initialize succeeds on the third try
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n.Add(1); http.Error(w, "down", 500) }))
	defer always.Close()
	tr := NewHTTP(HTTPConfig{URL: always.URL, RetryDelay: time.Millisecond})
	err := NewClient(tr).Initialize(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 500 || n.Load() != 3 {
		t.Fatalf("err = %v after %d requests, want a 500 StatusError after 3", err, n.Load())
	}
	if !strings.Contains(err.Error(), "HTTP 500: down") || (&StatusError{Status: 404}).Error() != "mcp: HTTP 404" {
		t.Fatalf("message = %q", err)
	}
	// a client error is not retried
	n.Store(0)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n.Add(1); http.Error(w, "no", 400) }))
	defer bad.Close()
	if err := NewClient(NewHTTP(HTTPConfig{URL: bad.URL, RetryDelay: time.Millisecond})).Initialize(context.Background()); err == nil || n.Load() != 1 {
		t.Fatalf("err = %v after %d requests", err, n.Load())
	}
	// a dead server is retried and then reported
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if err := NewClient(NewHTTP(HTTPConfig{URL: url, RetryDelay: time.Millisecond})).Initialize(context.Background()); err == nil {
		t.Fatal("want a connection error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewClient(NewHTTP(HTTPConfig{URL: url, RetryDelay: time.Hour})).Initialize(ctx); err == nil {
		t.Fatal("cancelled")
	}
}

func TestHTTPSessionExpiry(t *testing.T) {
	c, _ := httpClient(t, mcptest.New().HTTPHandler(mcptest.HTTPOptions{ExpireSession: 2}), HTTPConfig{})
	_, err := c.ListTools(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
}

type fakeAuth struct {
	token     atomic.Value
	challenge func() error
	calls     atomic.Int32
}

func (f *fakeAuth) Token(context.Context) (string, error) {
	s, _ := f.token.Load().(string)
	return s, nil
}

func (f *fakeAuth) Challenge(context.Context, *http.Response) error {
	f.calls.Add(1)
	return f.challenge()
}

func TestHTTPBearerAndChallenge(t *testing.T) {
	h := mcptest.New().HTTPHandler(mcptest.HTTPOptions{Token: "good", MetadataURL: "http://example/prm"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	fa := &fakeAuth{}
	fa.token.Store("stale")
	fa.challenge = func() error { fa.token.Store("good"); return nil }
	c := NewClient(NewHTTP(HTTPConfig{URL: srv.URL, Auth: fa, RetryDelay: time.Millisecond}))
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fa.calls.Load() != 1 {
		t.Fatalf("challenge calls = %d", fa.calls.Load())
	}
	// a challenge that fails is the error; a challenge that does not help ends in 401
	fa2 := &fakeAuth{challenge: func() error { return &AuthRequiredError{Server: "x"} }}
	err := NewClient(NewHTTP(HTTPConfig{URL: srv.URL, Auth: fa2})).Initialize(context.Background())
	var ar *AuthRequiredError
	if !errors.As(err, &ar) || !strings.Contains(err.Error(), "koine mcp login x") {
		t.Fatalf("err = %v", err)
	}
	fa3 := &fakeAuth{challenge: func() error { return nil }}
	err = NewClient(NewHTTP(HTTPConfig{URL: srv.URL, Auth: fa3})).Initialize(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 401 {
		t.Fatalf("err = %v", err)
	}
	// an explicit Authorization header wins over the authorizer
	c4 := NewClient(NewHTTP(HTTPConfig{URL: srv.URL, Auth: fa2, Headers: map[string]string{"Authorization": "Bearer good"}}))
	if err := c4.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReadSSE(t *testing.T) {
	in := ": hello\nid: 1\nevent: message\ndata: {\"a\":\ndata: 1}\n\ndata:no-space\r\n\r\ndata: tail-without-blank-line"
	var got []string
	if err := readSSE(strings.NewReader(in), func(d []byte) bool { got = append(got, string(d)); return true }); err != nil {
		t.Fatal(err)
	}
	want := []string{"{\"a\":\n1}", "no-space", "tail-without-blank-line"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	got = nil
	_ = readSSE(strings.NewReader("data: a\n\ndata: b\n\n"), func(d []byte) bool { got = append(got, string(d)); return false })
	if len(got) != 1 {
		t.Fatalf("stopping early: %q", got)
	}
}

func TestHTTPBadResponses(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"not json":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"empty stream": func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Content-Type", "text/event-stream") },
		"other id": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}\n\ndata: garbage\n\n"))
		},
		"404 no session": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
	}
	for name, h := range cases {
		srv := httptest.NewServer(h)
		err := NewClient(NewHTTP(HTTPConfig{URL: srv.URL, RetryDelay: time.Millisecond})).Initialize(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
