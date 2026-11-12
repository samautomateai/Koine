package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Authorizer supplies bearer tokens to the HTTP transport. Token returns the current access token ("" when there is none). Challenge is
// called after a 401 and returns nil when a new token is available, so the request is worth repeating once.
type Authorizer interface {
	Token(ctx context.Context) (string, error)
	Challenge(ctx context.Context, resp *http.Response) error
}

// ErrSessionExpired means the server no longer knows the session id (HTTP 404), so the client must initialize again.
var ErrSessionExpired = errors.New("mcp: session expired")

// HTTPConfig says how to reach a streamable HTTP server.
type HTTPConfig struct {
	URL     string
	Headers map[string]string
	Client  *http.Client
	Auth    Authorizer
	// RetryDelay is the base wait before a retry (default 500ms, doubled each time). Network errors and 408, 429 and 5xx are retried twice.
	RetryDelay time.Duration
}

type httpTransport struct {
	cfg HTTPConfig

	mu      sync.Mutex
	session string
	version string
	handler func(*Message)
	closed  bool
}

// NewHTTP returns a streamable HTTP transport: each message is a POST whose answer is JSON or an SSE stream. The legacy SSE transport (a
// GET stream plus a separate POST endpoint) is not supported, as in Pi.
func NewHTTP(cfg HTTPConfig) Transport {
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 500 * time.Millisecond
	}
	return &httpTransport{cfg: cfg}
}

func (h *httpTransport) Start(context.Context) error { return nil }

func (h *httpTransport) SetHandler(fn func(*Message)) {
	h.mu.Lock()
	h.handler = fn
	h.mu.Unlock()
}

func (h *httpTransport) SetProtocolVersion(v string) {
	h.mu.Lock()
	h.version = v
	h.mu.Unlock()
}

func (h *httpTransport) Alive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.closed
}

func (h *httpTransport) emit(m *Message) {
	h.mu.Lock()
	fn := h.handler
	h.mu.Unlock()
	if fn != nil {
		fn(m)
	}
}

func retryable(status int) bool { return status == 408 || status == 429 || status >= 500 }

// post sends one message and returns the open response. It retries transient failures and repeats the request once after a 401 that the
// Authorizer could resolve. The caller closes the body.
func (h *httpTransport) post(ctx context.Context, payload []byte) (*http.Response, error) {
	attempts, authTried := 0, false
	for {
		resp, err := h.postOnce(ctx, payload)
		switch {
		case err != nil:
			if ctx.Err() != nil || attempts >= 2 {
				return nil, err
			}
		case resp.StatusCode == http.StatusUnauthorized && h.cfg.Auth != nil && !authTried:
			authTried = true
			cerr := h.cfg.Auth.Challenge(ctx, resp)
			_ = resp.Body.Close()
			if cerr != nil {
				return nil, cerr
			}
			continue
		case retryable(resp.StatusCode) && attempts < 2:
			_ = resp.Body.Close()
		default:
			return resp, nil
		}
		wait := h.cfg.RetryDelay << attempts
		attempts++
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

func (h *httpTransport) postOnce(ctx context.Context, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range h.cfg.Headers {
		req.Header.Set(k, v)
	}
	h.mu.Lock()
	if h.session != "" {
		req.Header.Set("Mcp-Session-Id", h.session)
	}
	if h.version != "" {
		req.Header.Set("MCP-Protocol-Version", h.version)
	}
	h.mu.Unlock()
	if h.cfg.Auth != nil && req.Header.Get("Authorization") == "" {
		tok, err := h.cfg.Auth.Token(ctx)
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	return h.cfg.Client.Do(req)
}

// StatusError is a non-success HTTP answer.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("mcp: HTTP %d", e.Status)
	}
	return fmt.Sprintf("mcp: HTTP %d: %s", e.Status, e.Body)
}

func (h *httpTransport) check(resp *http.Response) error {
	if resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusNotFound {
		h.mu.Lock()
		had := h.session != ""
		h.session = ""
		h.mu.Unlock()
		if had {
			return ErrSessionExpired
		}
	}
	return &StatusError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
}

func (h *httpTransport) Do(ctx context.Context, id int64, payload []byte) (*Message, error) {
	resp, err := h.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := h.check(resp); err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		h.mu.Lock()
		h.session = sid
		h.mu.Unlock()
	}
	want := strconv.FormatInt(id, 10)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var found *Message
		err := readSSE(resp.Body, func(data []byte) bool {
			var m Message
			if json.Unmarshal(data, &m) != nil {
				return true
			}
			if m.IsResponse() && string(m.ID) == want {
				found = &m
				return false
			}
			h.emit(&m)
			return true
		})
		if found != nil {
			return found, nil
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("mcp: stream ended without a response: %w", err)
	}
	var m Message
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("mcp: bad response: %w", err)
	}
	return &m, nil
}

func (h *httpTransport) Send(ctx context.Context, payload []byte) error {
	resp, err := h.post(ctx, payload)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return h.check(resp)
}

// Close ends the session politely (DELETE with the session id); servers may answer 405 and that is fine.
func (h *httpTransport) Close() error {
	h.mu.Lock()
	h.closed = true
	sid := h.session
	h.session = ""
	h.mu.Unlock()
	if sid == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.cfg.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Mcp-Session-Id", sid)
	for k, v := range h.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := h.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// readSSE parses a Server-Sent Events stream and calls fn with the data of each event until fn returns false or the stream ends.
func readSSE(r io.Reader, fn func(data []byte) bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var data []byte
	flush := func() bool {
		if len(data) == 0 {
			return true
		}
		d := data
		data = nil
		return fn(d)
	}
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if !flush() {
				return nil
			}
		case strings.HasPrefix(line, ":"):
		default:
			field, value, _ := strings.Cut(line, ":")
			if field == "data" {
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, strings.TrimPrefix(value, " ")...)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				flush()
				return nil
			}
			return err
		}
	}
}
