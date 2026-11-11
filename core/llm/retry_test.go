package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var fast = RetryPolicy{BaseDelay: time.Millisecond, MaxDelay: 4 * time.Millisecond}

// flaky answers with status for the first `fails` calls and 200 afterwards.
func flaky(fails int32, status int, body string, hdr map[string]string) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= fails {
			for k, v := range hdr {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})), &n
}

func TestRetryPolicyDefaultsAndBackoff(t *testing.T) {
	var p RetryPolicy
	if p.attempts() != 4 || (RetryPolicy{Disabled: true, Attempts: 9}).attempts() != 1 || (RetryPolicy{Attempts: 2}).attempts() != 2 {
		t.Error("attempts")
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := p.backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	q := RetryPolicy{BaseDelay: time.Second, MaxDelay: 3 * time.Second}
	if q.backoff(1) != time.Second || q.backoff(2) != 2*time.Second || q.backoff(3) != 3*time.Second || q.backoff(10) != 3*time.Second {
		t.Error("backoff doubles and stops at MaxDelay")
	}
}

func TestRetrySettingsControlAttempts(t *testing.T) {
	post := func(srv *httptest.Server, p RetryPolicy) error {
		_, err := postJSON(context.Background(), nil, "x", srv.URL, nil, []byte(`{}`), p)
		return err
	}
	srv, n := flaky(100, 503, "down", nil)
	defer srv.Close()
	p := fast
	p.Attempts = 3
	if err := post(srv, p); err == nil || n.Load() != 3 {
		t.Errorf("3 attempts: %v after %d calls", err, n.Load())
	}
	n.Store(0)
	p = fast
	p.Disabled = true
	if err := post(srv, p); err == nil || n.Load() != 1 {
		t.Errorf("disabled: %v after %d calls", err, n.Load())
	}
	n.Store(0)
	if err := post(srv, fast); err == nil || n.Load() != 4 {
		t.Errorf("default is 4 attempts: %v after %d calls", err, n.Load())
	}
	var he *HTTPError
	if err := post(srv, fast); !errors.As(err, &he) || he.Status != 503 || he.Provider != "x" || !strings.Contains(err.Error(), "HTTP 503: down") {
		t.Errorf("error type: %v", err)
	}
}

func TestRetryAfterIsHonouredUpToTheLimit(t *testing.T) {
	srv, n := flaky(1, 429, "slow down", map[string]string{"Retry-After": "1"})
	defer srv.Close()
	start := time.Now()
	if _, err := postJSON(context.Background(), nil, "x", srv.URL, nil, []byte(`{}`), fast); err != nil || n.Load() != 2 {
		t.Fatalf("%v after %d calls", err, n.Load())
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Errorf("Retry-After: 1 should have been waited for, took %s", time.Since(start))
	}
	// longer than the limit: fail at once and say why
	srv2, n2 := flaky(5, 429, "slow down", map[string]string{"Retry-After": "120"})
	defer srv2.Close()
	_, err := postJSON(context.Background(), nil, "x", srv2.URL, nil, []byte(`{}`), fast)
	if err == nil || n2.Load() != 1 || !strings.Contains(err.Error(), "asks to wait 2m0s") {
		t.Errorf("long Retry-After: %v after %d calls", err, n2.Load())
	}
	// no limit (negative) would wait; check the parse of both header forms without waiting
	if retryAfter(http.Header{"Retry-After": {"7"}}) != 7*time.Second || retryAfter(http.Header{}) != 0 ||
		retryAfter(http.Header{"Retry-After": {"soon"}}) != 0 || retryAfter(http.Header{"Retry-After": {"-3"}}) != 0 {
		t.Error("retryAfter seconds")
	}
	date := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if d := retryAfter(http.Header{"Retry-After": {date}}); d < 25*time.Second || d > 31*time.Second {
		t.Errorf("retryAfter date: %s", d)
	}
}

func TestQuotaErrorsAreNotRetried(t *testing.T) {
	for _, body := range []string{`{"error":{"code":"insufficient_quota"}}`, "You exceeded your current quota", "Your credit balance is too low"} {
		srv, n := flaky(5, 429, body, nil)
		_, err := postJSON(context.Background(), nil, "openai", srv.URL, nil, []byte(`{}`), fast)
		srv.Close()
		if err == nil || n.Load() != 1 || !IsQuotaError(err) {
			t.Errorf("%q: %v after %d calls", body, err, n.Load())
		}
	}
	if IsQuotaError(errors.New("insufficient_quota")) || IsQuotaError(&HTTPError{Status: 500, Body: "insufficient_quota"}) ||
		IsQuotaError(&HTTPError{Status: 429, Body: "rate limit"}) || IsQuotaError(nil) {
		t.Error("only a 429 that names a quota is a quota error")
	}
}

func TestRetryNoticesAndCancellation(t *testing.T) {
	srv, _ := flaky(2, 500, "boom", nil)
	defer srv.Close()
	var notes []RetryNotice
	var resumed []int
	ctx := WithRetryNotify(context.Background(), func(n RetryNotice) { notes = append(notes, n) })
	ctx = WithRetryResume(ctx, func(attempt int) { resumed = append(resumed, attempt) })
	if _, err := postJSON(ctx, nil, "x", srv.URL, nil, []byte(`{}`), fast); err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 2 || resumed[0] != 1 || resumed[1] != 2 {
		t.Errorf("resume hook saw %v, want the two retries that started", resumed)
	}
	if len(notes) != 2 || notes[0].Attempt != 1 || notes[1].Attempt != 2 || notes[0].Of != 3 || notes[0].Delay != time.Millisecond ||
		notes[1].Delay != 2*time.Millisecond || notes[0].Err == nil {
		t.Errorf("notices: %+v", notes)
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv2, _ := flaky(5, 500, "boom", nil)
	defer srv2.Close()
	slow := RetryPolicy{BaseDelay: time.Hour}
	if _, err := postJSON(cctx, nil, "x", srv2.URL, nil, []byte(`{}`), slow); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	// cancelled while waiting to retry
	wctx, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	start := time.Now()
	if _, err := postJSON(wctx, nil, "x", srv2.URL, nil, []byte(`{}`), slow); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Errorf("cancelled during the wait: %v", err)
	}
}

func TestRequestTimeoutIsRetriedAndReported(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body) // the server notices a closed connection only once the body has been read
		if n.Add(1) == 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	p := fast
	p.Timeout = 50 * time.Millisecond
	if _, err := postJSON(context.Background(), nil, "x", srv.URL, nil, []byte(`{}`), p); err != nil || n.Load() != 2 {
		t.Errorf("a timed-out attempt is retried: %v after %d calls", err, n.Load())
	}
	n.Store(0)
	p.Disabled = true
	if _, err := postJSON(context.Background(), nil, "x", srv.URL, nil, []byte(`{}`), p); err == nil {
		t.Error("with retries off the timeout is an error")
	}
}

func TestRouterGivesItsRetryPolicyToProviders(t *testing.T) {
	r := &Router{Retry: RetryPolicy{Attempts: 7}, Getenv: func(k string) string { return "key" }}
	p, _, err := r.Resolve("claude-sonnet-5-5")
	if err != nil || p.(*Anthropic).Retry.Attempts != 7 {
		t.Errorf("anthropic: %v %v", p, err)
	}
	p, _, err = r.Resolve("openai/gpt-4o")
	if err != nil || p.(*OpenAICompat).Retry.Attempts != 7 {
		t.Errorf("openai: %v %v", p, err)
	}
}

func TestIsOverflowPerProvider(t *testing.T) {
	he := func(provider string, status int, body string) error {
		return fmt.Errorf("wrapped: %w", &HTTPError{Provider: provider, Status: status, Body: body})
	}
	yes := []error{
		he("anthropic", 400, `{"error":{"message":"prompt is too long: 250000 tokens > 200000 maximum"}}`),
		he("openai", 400, `{"error":{"code":"context_length_exceeded"}}`),
		he("groq", 400, "Please reduce the length of the messages or completion"),
		he("mistral", 400, "Prompt contains 40000 tokens, too large for model with 32768 maximum context length"),
		he("xai", 400, "This model's maximum prompt length is 131072"),
		he("lmstudio", 400, "tokens to keep from the initial prompt is greater than the context length"),
		he("ollama", 400, "prompt too long; exceeded max context length"),
		he("anything", 413, "nope"),
		he("my-own-provider", 400, "the request exceeds the available context size"), // a provider from the config: any known wording
		errors.New("anthropic: HTTP 400: prompt is too long"),
		errors.New("HTTP 413: Request Too Large"),
	}
	for _, e := range yes {
		if !IsOverflow(e) {
			t.Errorf("%v should be an overflow", e)
		}
	}
	no := []error{nil, errors.New("HTTP 401: bad key"), he("anthropic", 429, "slow down"), he("anthropic", 400, "invalid x"),
		he("openai", 400, "exceeds the available context size"), // llama.cpp wording is not OpenAI's
	}
	for _, e := range no {
		if IsOverflow(e) {
			t.Errorf("%v is not an overflow", e)
		}
	}
}
