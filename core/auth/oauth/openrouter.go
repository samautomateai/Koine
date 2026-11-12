package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
)

// OpenRouter signs in with OpenRouter's PKCE flow, which hands back a permanent API key instead of expiring tokens. The callback uses a
// random path on any free port.
type OpenRouter struct {
	Client
	AuthorizeURL string
	KeyURL       string
}

// NewOpenRouter returns the OpenRouter flow with its public endpoints.
func NewOpenRouter(c Client) *OpenRouter {
	return &OpenRouter{Client: c, AuthorizeURL: "https://openrouter.ai/auth", KeyURL: "https://openrouter.ai/api/v1/auth/keys"}
}

// ID implements Flow.
func (o *OpenRouter) ID() string { return "openrouter" }

// Name implements Flow.
func (o *OpenRouter) Name() string { return "OpenRouter" }

// Notice implements Flow.
func (o *OpenRouter) Notice() string { return "" }

// Login implements Flow. The key is stored as an ordinary API key, so it works everywhere one does.
func (o *OpenRouter) Login(ctx context.Context, ui UI, opt Options) (Result, error) {
	verifier, challenge := pkce()
	path := "/oauth/callback/" + randomHex(16)
	var cb *callback
	if !opt.NoBrowser {
		cb, _ = startCallback([]int{0}, path, "")
		defer cb.close()
	}
	redirect := "http://localhost:3000" + path // with no listener the page will not load; the user copies its address from the browser bar
	if cb != nil {
		redirect = "http://localhost:" + strconv.Itoa(cb.port) + path
	}
	q := url.Values{"callback_url": {redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	target := o.AuthorizeURL + "?" + q.Encode()
	ui.notify("Open this address to sign in:\n%s", target)
	if !opt.NoBrowser && ui.OpenBrowser != nil {
		_ = ui.OpenBrowser(target)
	}
	code, _, err := await(ctx, ui, cb, "Complete sign-in in your browser, or paste the redirect URL here: ", "")
	if err != nil {
		return Result{}, err
	}
	data, err := o.postJSON(ctx, "OpenRouter key exchange", o.KeyURL, map[string]string{"code": code, "code_verifier": verifier,
		"code_challenge_method": "S256"})
	if err != nil {
		return Result{}, err
	}
	var r struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(data, &r) != nil || r.Key == "" {
		return Result{}, errors.New("OpenRouter's answer carries no key")
	}
	return Result{APIKey: r.Key}, nil
}

// Refresh implements Flow. A key does not expire.
func (o *OpenRouter) Refresh(_ context.Context, t Token) (Token, error) { return t, nil }
