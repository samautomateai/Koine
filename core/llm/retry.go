package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy says how a provider call is retried. The zero value is the default: four attempts, waits of 2 s, 4 s and 8 s, no per-request
// timeout. The names follow Pi's `retry.*` and `httpIdleTimeoutMs` settings.
type RetryPolicy struct {
	Disabled       bool          // no retries at all (retry.enabled false, or retry.maxRetries 0)
	Attempts       int           // tries in all, first included (retry.maxRetries + 1); 0 = 4
	BaseDelay      time.Duration // wait before the first retry, doubled after each (retry.baseDelayMs); 0 = 2 s
	MaxDelay       time.Duration // longest backoff wait (retry.maxAgentDelayMs); 0 = 60 s
	MaxServerDelay time.Duration // longest Retry-After Koine will wait for (retry.provider.maxRetryDelayMs); 0 = 60 s, negative = no limit
	Timeout        time.Duration // longest wait for one response (retry.provider.timeoutMs, else httpIdleTimeoutMs); 0 = none
}

func (p RetryPolicy) attempts() int {
	switch {
	case p.Disabled:
		return 1
	case p.Attempts > 0:
		return p.Attempts
	}
	return 4
}

// backoff is the wait before retry number n (1 is the first retry).
func (p RetryPolicy) backoff(n int) time.Duration {
	base, limit := orDur(p.BaseDelay, 2*time.Second), orDur(p.MaxDelay, 60*time.Second)
	d := base
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

func orDur(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// RetryNotice describes a retry that is about to happen.
type RetryNotice struct {
	Attempt int // the retry's number, from 1
	Of      int // the most retries there will be
	Delay   time.Duration
	Err     error // why the last attempt failed
}

type notifyKey struct{}

// WithRetryNotify returns a context whose provider calls report each retry to fn before waiting.
func WithRetryNotify(ctx context.Context, fn func(RetryNotice)) context.Context {
	return context.WithValue(ctx, notifyKey{}, fn)
}

type resumeKey struct{}

// WithRetryResume returns a context whose provider calls report each retry attempt when its wait is over and it starts (the retry's number).
func WithRetryResume(ctx context.Context, fn func(attempt int)) context.Context {
	return context.WithValue(ctx, resumeKey{}, fn)
}

// HTTPError is a provider answer that was not a success.
type HTTPError struct {
	Provider string
	Status   int
	Body     string // as far as it was read, for matching; Error shortens it
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, truncate(e.Body, 500))
}

// quotaHints are phrases of billing and quota failures that arrive as 429 but do not clear by waiting.
var quotaHints = []string{"insufficient_quota", "exceeded your current quota", "credit balance is too low", "billing_hard_limit_reached",
	"usage limit has been reached", "out of credits"}

// IsQuotaError reports whether err is a 429 that retrying cannot fix: the account is out of quota or credit.
func IsQuotaError(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 429 {
		return false
	}
	s := strings.ToLower(he.Body)
	for _, h := range quotaHints {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}

// retryAfter reads a Retry-After header: seconds or an HTTP date. Zero means none.
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(max(n, 0)) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

// postJSON POSTs payload and returns the body of a 200 response. Network errors, timeouts, 429 and 5xx are retried as pol says; other
// statuses, and 429s that are about quota or billing, fail at once, and so does a Retry-After longer than pol allows.
func postJSON(ctx context.Context, client *http.Client, label, url string, headers map[string]string, payload []byte,
	pol RetryPolicy) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	notify, _ := ctx.Value(notifyKey{}).(func(RetryNotice))
	resume, _ := ctx.Value(resumeKey{}).(func(int))
	n := pol.attempts()
	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt < n; attempt++ {
		if attempt > 0 {
			if notify != nil {
				notify(RetryNotice{Attempt: attempt, Of: n - 1, Delay: wait, Err: lastErr})
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			if resume != nil {
				resume(attempt)
			}
		}
		data, status, h, err := post(ctx, client, url, headers, payload, pol.Timeout)
		wait = pol.backoff(attempt + 1)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		case status == 200:
			return data, nil
		default:
			he := &HTTPError{Provider: label, Status: status, Body: truncate(string(data), 4000)}
			if (status != 429 && status < 500) || IsQuotaError(he) {
				return nil, he
			}
			lastErr = he
			if d := retryAfter(h); d > 0 {
				if lim := orDur(pol.MaxServerDelay, 60*time.Second); pol.MaxServerDelay >= 0 && d > lim {
					return nil, fmt.Errorf("%w (the server asks to wait %s, more than the %s allowed)", he, d.Round(time.Second), lim)
				}
				wait = max(wait, d)
			}
		}
	}
	return nil, lastErr
}

// post makes one attempt, giving up after timeout (0 = never).
func post(ctx context.Context, client *http.Client, url string, headers map[string]string, payload []byte, timeout time.Duration) (
	data []byte, status int, h http.Header, err error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	hr, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, nil, err
	}
	hr.Header.Set("content-type", "application/json")
	for k, v := range headers {
		hr.Header.Set(k, v)
	}
	resp, err := client.Do(hr)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err = io.ReadAll(resp.Body)
	return data, resp.StatusCode, resp.Header, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
