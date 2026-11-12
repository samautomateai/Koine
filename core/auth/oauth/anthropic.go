package oauth

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// Anthropic signs in with a Claude Pro or Max account. The client id and scopes are those of Claude Code's own login, which Pi uses too;
// Anthropic may restrict third-party use of subscription tokens, hence Notice.
type Anthropic struct {
	Client
	AuthorizeURL string
	TokenURL     string
	CopyCodeURL  string // the redirect page that shows a code to paste, for machines without a loopback callback
	Port         int    // preferred callback port (0 = any); Anthropic accepts any loopback port
}

const (
	anthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicScopes   = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicSkew     = 5 * time.Minute
)

// NewAnthropic returns the Claude Pro/Max flow with its public endpoints.
func NewAnthropic(c Client) *Anthropic {
	tokenURL := "https://platform.claude.com/v1/oauth/token" //nolint:gosec // G101: a public endpoint, not a credential
	return &Anthropic{Client: c, AuthorizeURL: "https://claude.ai/oauth/authorize", TokenURL: tokenURL,
		CopyCodeURL: "https://platform.claude.com/oauth/code/callback", Port: 53692}
}

// ID implements Flow.
func (a *Anthropic) ID() string { return "anthropic" }

// Name implements Flow.
func (a *Anthropic) Name() string { return "Anthropic (Claude Pro/Max)" }

// Notice implements Flow.
func (a *Anthropic) Notice() string {
	return "Using a Claude subscription outside Anthropic's own apps may be against Anthropic's terms of service, and Anthropic can change or block it. " +
		"An API key (koine auth login anthropic --api-key) is the supported route."
}

func (a *Anthropic) authorizeURL(redirect, challenge, state string) string {
	q := url.Values{"code": {"true"}, "client_id": {anthropicClientID}, "response_type": {"code"}, "redirect_uri": {redirect},
		"scope": {anthropicScopes}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {state}}
	return a.AuthorizeURL + "?" + q.Encode()
}

// Login implements Flow: a browser sign-in answered on a loopback port (with a pasted redirect URL as the alternative), or with
// NoBrowser the copy-code page.
func (a *Anthropic) Login(ctx context.Context, ui UI, opt Options) (Result, error) {
	verifier, challenge := pkce()
	state := randomHex(16)
	var cb *callback
	redirect := a.CopyCodeURL
	question := "Paste the code Anthropic shows after you sign in: "
	if !opt.NoBrowser {
		cb, _ = startCallback(portsFor(a.Port), "/callback", state) // a failure leaves paste as the way in
		defer cb.close()
		if cb != nil {
			redirect = "http://localhost:" + strconv.Itoa(cb.port) + "/callback"
		}
		question = "Complete sign-in in your browser, or paste the redirect URL here: "
	}
	if cb == nil && !opt.NoBrowser {
		redirect = a.CopyCodeURL // no loopback port: Anthropic's page shows the code instead
		question = "Paste the code Anthropic shows after you sign in: "
	}
	target := a.authorizeURL(redirect, challenge, state)
	ui.notify("Open this address to sign in:\n%s", target)
	if !opt.NoBrowser && ui.OpenBrowser != nil {
		_ = ui.OpenBrowser(target) // the address above is the fallback
	}
	code, got, err := await(ctx, ui, cb, question, state)
	if err != nil {
		return Result{}, err
	}
	if got == "" {
		got = state
	}
	ui.notify("Exchanging the authorization code for tokens...")
	data, err := a.postJSON(ctx, "Anthropic token exchange", a.TokenURL, map[string]string{"grant_type": "authorization_code",
		"client_id": anthropicClientID, "code": code, "state": got, "redirect_uri": redirect, "code_verifier": verifier})
	if err != nil {
		return Result{}, err
	}
	t, err := a.parseTokens("Anthropic token exchange", data, anthropicSkew, true)
	return Result{Token: t}, err
}

// Refresh implements Flow.
func (a *Anthropic) Refresh(ctx context.Context, t Token) (Token, error) {
	if t.Refresh == "" {
		return Token{}, errors.New("the stored Claude login has no refresh token")
	}
	data, err := a.postJSON(ctx, "Anthropic token refresh", a.TokenURL, map[string]string{"grant_type": "refresh_token",
		"client_id": anthropicClientID, "refresh_token": t.Refresh})
	if err != nil {
		return Token{}, err
	}
	n, err := a.parseTokens("Anthropic token refresh", data, anthropicSkew, false)
	if err != nil {
		return Token{}, err
	}
	if n.Refresh == "" {
		n.Refresh = t.Refresh
	}
	n.Extra = t.Extra
	return n, nil
}
