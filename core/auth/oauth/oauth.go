// Package oauth signs in to subscription providers (Claude Pro/Max, ChatGPT Plus/Pro for Codex, GitHub Copilot) and to OpenRouter with the
// same browser and device flows Pi uses, on the standard library. A flow returns a token for auth.json; Refresh renews it. Nothing here
// reads project files, and every endpoint is fixed in code (tests replace them), so a folder cannot redirect a login.
package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"koine/core/auth"
)

// Token is a stored login (see auth.OAuthToken).
type Token = auth.OAuthToken

// UI is what a login needs from the frontend. Every function is optional; a flow that needs one it lacks fails with a clear message.
type UI struct {
	Notify      func(msg string)                                   // show a URL, a code or progress
	Prompt      func(question string, secret bool) (string, error) // ask for a pasted code or redirect URL
	OpenBrowser func(url string) error                             // open a page in the user's browser
	Race        bool                                               // also read a pasted code while the browser callback is awaited
	Timeout     time.Duration                                      // how long to wait for the user (default 10 minutes)
}

func (u UI) notify(format string, a ...any) {
	if u.Notify != nil {
		u.Notify(fmt.Sprintf(format, a...))
	}
}

func (u UI) timeout() time.Duration {
	if u.Timeout > 0 {
		return u.Timeout
	}
	return 10 * time.Minute
}

// Options are choices made on the command line.
type Options struct {
	// NoBrowser selects the flow for machines without a browser or a reachable loopback port: a pasted code for Claude, a device code for
	// ChatGPT. GitHub Copilot always uses a device code and merely does not open the page.
	NoBrowser bool
}

// Result is a finished login: a token to store, or a permanent API key (OpenRouter).
type Result struct {
	Token  Token
	APIKey string
}

// Flow is one provider's sign-in.
type Flow interface {
	ID() string // the provider id the login is stored under
	Name() string
	Notice() string // a warning shown before the login, "" for none
	Login(ctx context.Context, ui UI, opt Options) (Result, error)
	Refresh(ctx context.Context, t Token) (Token, error)
}

// Client carries what the flows share. The zero value uses the real clock and a client with a 30 second timeout.
type Client struct {
	HTTP  *http.Client
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error // tests replace it so polling does not wait
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return errors.New("login cancelled")
	}
}

var order = []string{"anthropic", "openai-codex", "github-copilot", "openrouter"}

// IDs lists the providers that can be signed in to, in the order /login offers them.
func IDs() []string { return append([]string(nil), order...) }

// Has reports whether provider has a sign-in flow.
func Has(provider string) bool {
	for _, id := range order {
		if id == provider {
			return true
		}
	}
	return false
}

// Lookup returns the flow of a provider.
func Lookup(provider string, c Client) (Flow, bool) {
	switch provider {
	case "anthropic":
		return NewAnthropic(c), true
	case "openai-codex":
		return NewCodex(c), true
	case "github-copilot":
		return NewCopilot(c), true
	case "openrouter":
		return NewOpenRouter(c), true
	}
	return nil, false
}

// ---- helpers shared by the flows ----

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand does not fail on supported platforms
	return b
}

// pkce returns a code verifier and its S256 challenge (RFC 7636).
func pkce() (verifier, challenge string) {
	verifier = base64.RawURLEncoding.EncodeToString(randomBytes(32))
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }

// parseInput reads what a user pastes after signing in: the redirect URL, "code#state", a query string or the bare code.
func parseInput(in string) (code, state string) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", ""
	}
	if u, err := url.Parse(in); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Query().Get("code"), u.Query().Get("state")
	}
	if c, s, ok := strings.Cut(in, "#"); ok {
		return c, s
	}
	if strings.Contains(in, "code=") {
		q, _ := url.ParseQuery(strings.TrimPrefix(in, "?"))
		return q.Get("code"), q.Get("state")
	}
	return in, ""
}

type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
}

// send performs a request and returns the body of a 2xx answer. Other answers become errors that carry the status and the start of the
// body, which is where servers say what is wrong; tokens are never part of an error body.
func (c Client) send(ctx context.Context, what, method, target string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return nil, &StatusError{What: what, Status: resp.StatusCode, Body: msg}
	}
	return data, nil
}

// StatusError is a non-2xx answer from a provider's login server.
type StatusError struct {
	What   string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s failed (HTTP %d)", e.What, e.Status)
	}
	return fmt.Sprintf("%s failed (HTTP %d): %s", e.What, e.Status, e.Body)
}

func (c Client) postJSON(ctx context.Context, what, target string, v any) ([]byte, error) {
	b, _ := json.Marshal(v)
	return c.send(ctx, what, http.MethodPost, target, b, map[string]string{"Content-Type": "application/json"})
}

func (c Client) postForm(ctx context.Context, what, target string, v url.Values, extra map[string]string) ([]byte, error) {
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for k, x := range extra {
		h[k] = x
	}
	return c.send(ctx, what, http.MethodPost, target, []byte(v.Encode()), h)
}

// parseTokens reads an OAuth token answer. skew is taken off the lifetime so that a token is renewed a little before it lapses (Pi does
// this for Claude).
func (c Client) parseTokens(what string, data []byte, skew time.Duration, needRefresh bool) (Token, error) {
	var r tokenResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return Token{}, fmt.Errorf("%s: the answer is not JSON: %w", what, err)
	}
	if r.AccessToken == "" || r.ExpiresIn <= 0 || (needRefresh && r.RefreshToken == "") {
		return Token{}, fmt.Errorf("%s: the answer lacks an access token, a refresh token or a lifetime", what)
	}
	exp := c.now().Add(time.Duration(r.ExpiresIn*float64(time.Second)) - skew)
	return Token{Access: r.AccessToken, Refresh: r.RefreshToken, Expires: exp.UnixMilli()}, nil
}

// pollDevice calls poll every interval until it returns done, fails, or the lifetime ends. "slow" lengthens the interval as RFC 8628
// asks: to the server's value when it gave one, else by five seconds.
type pollResult struct {
	state    string // "done", "pending", "slow", "fail"
	interval time.Duration
	err      error
}

func (c Client) pollDevice(ctx context.Context, interval, lifetime time.Duration, waitFirst bool, poll func() pollResult) error {
	if interval < time.Second {
		interval = time.Second
	}
	deadline := c.now().Add(lifetime)
	slowed := false
	if waitFirst {
		if err := c.sleep(ctx, interval); err != nil {
			return err
		}
	}
	for c.now().Before(deadline) {
		if ctx.Err() != nil {
			return errors.New("login cancelled")
		}
		r := poll()
		switch r.state {
		case "done":
			return nil
		case "fail":
			return r.err
		case "slow":
			slowed = true
			if r.interval > 0 {
				interval = max(time.Second, r.interval)
			} else {
				interval += 5 * time.Second
			}
		}
		if err := c.sleep(ctx, interval); err != nil {
			return err
		}
	}
	if slowed {
		return errors.New("the device code expired after the server asked to slow down; if this machine's clock is off (a VM, WSL), fix it and retry")
	}
	return errors.New("the device code expired before the sign-in finished")
}
