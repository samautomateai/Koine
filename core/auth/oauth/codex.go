package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Codex signs in with a ChatGPT Plus/Pro account for OpenAI's Codex backend. It has a browser flow (loopback callback on port 1455, the
// one the Codex client id is registered for) and a device-code flow for machines without a browser.
type Codex struct {
	Client
	AuthBase string // https://auth.openai.com
	Port     int    // the registered callback port; the redirect URI must match exactly, so there is no fallback to another port
}

const (
	codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexScope    = "openid profile email offline_access"
	codexLifetime = 15 * time.Minute
)

// NewCodex returns the ChatGPT flow with its public endpoints.
func NewCodex(c Client) *Codex {
	return &Codex{Client: c, AuthBase: "https://auth.openai.com", Port: 1455}
}

// ID implements Flow.
func (x *Codex) ID() string { return "openai-codex" }

// Name implements Flow.
func (x *Codex) Name() string { return "OpenAI (ChatGPT Plus/Pro, Codex)" }

// Notice implements Flow.
func (x *Codex) Notice() string {
	return "This signs in with your ChatGPT subscription for OpenAI's Codex backend; OpenAI's terms for third-party clients apply."
}

// accountID reads the ChatGPT account id from the access token, which is a JWT; the backend wants it in a header.
func accountID(access string) string {
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	var auth struct {
		ID string `json:"chatgpt_account_id"`
	}
	_ = json.Unmarshal(claims["https://api.openai.com/auth"], &auth)
	return auth.ID
}

func (x *Codex) token(what string, data []byte) (Token, error) {
	t, err := x.parseTokens(what, data, 0, true)
	if err != nil {
		return t, err
	}
	id := accountID(t.Access)
	if id == "" {
		return Token{}, errors.New("the access token carries no ChatGPT account id; this account may not include Codex")
	}
	t.Extra = map[string]string{"accountId": id}
	return t, nil
}

func (x *Codex) exchange(ctx context.Context, code, verifier, redirect string) (Token, error) {
	data, err := x.postForm(ctx, "OpenAI token exchange", x.AuthBase+"/oauth/token", url.Values{"grant_type": {"authorization_code"},
		"client_id": {codexClientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}, nil)
	if err != nil {
		return Token{}, err
	}
	return x.token("OpenAI token exchange", data)
}

// Login implements Flow.
func (x *Codex) Login(ctx context.Context, ui UI, opt Options) (Result, error) {
	if opt.NoBrowser {
		return x.device(ctx, ui)
	}
	verifier, challenge := pkce()
	state := randomHex(16)
	cb, err := startCallback([]int{x.Port}, "/auth/callback", state)
	if err != nil {
		ui.notify("Could not listen on port %d (%v); paste the redirect URL instead, or use --no-browser for a device code.", x.Port, err)
	}
	defer cb.close()
	redirect := "http://localhost:" + strconv.Itoa(x.Port) + "/auth/callback"
	if cb != nil {
		redirect = "http://localhost:" + strconv.Itoa(cb.port) + "/auth/callback"
	}
	q := url.Values{"response_type": {"code"}, "client_id": {codexClientID}, "redirect_uri": {redirect}, "scope": {codexScope},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {state}, "id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow": {"true"}, "originator": {"koine"}}
	target := x.AuthBase + "/oauth/authorize?" + q.Encode()
	ui.notify("Open this address to sign in:\n%s", target)
	if ui.OpenBrowser != nil {
		_ = ui.OpenBrowser(target)
	}
	code, _, err := await(ctx, ui, cb, "Complete sign-in in your browser, or paste the redirect URL here: ", state)
	if err != nil {
		return Result{}, err
	}
	t, err := x.exchange(ctx, code, verifier, redirect)
	return Result{Token: t}, err
}

// device is the device-code flow: show a code, the user enters it at a page, and the server then hands over an authorization code and the
// PKCE verifier it chose.
func (x *Codex) device(ctx context.Context, ui UI) (Result, error) {
	data, err := x.postJSON(ctx, "OpenAI device code request", x.AuthBase+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": codexClientID})
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Status == 404 {
			return Result{}, errors.New("OpenAI device-code sign-in is not enabled for this account or server; sign in with the browser instead")
		}
		return Result{}, err
	}
	var dev struct {
		ID       string          `json:"device_auth_id"`
		UserCode string          `json:"user_code"`
		Interval json.RawMessage `json:"interval"`
	}
	if json.Unmarshal(data, &dev) != nil || dev.ID == "" || dev.UserCode == "" {
		return Result{}, errors.New("OpenAI answered the device code request with something unexpected")
	}
	interval := 5 * time.Second
	if s, e := strconv.ParseFloat(strings.Trim(string(dev.Interval), `" `), 64); e == nil && s >= 0 {
		interval = time.Duration(s * float64(time.Second))
	}
	page := x.AuthBase + "/codex/device"
	ui.notify("Open %s and enter the code %s", page, dev.UserCode)
	var grant struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	err = x.pollDevice(ctx, interval, codexLifetime, false, func() pollResult {
		b, e := x.postJSON(ctx, "OpenAI device authorization", x.AuthBase+"/api/accounts/deviceauth/token",
			map[string]string{"device_auth_id": dev.ID, "user_code": dev.UserCode})
		var se *StatusError
		switch {
		case e == nil:
			if json.Unmarshal(b, &grant) != nil || grant.Code == "" || grant.Verifier == "" {
				return pollResult{state: "fail", err: errors.New("OpenAI answered the device authorization with something unexpected")}
			}
			return pollResult{state: "done"}
		case errors.As(e, &se) && (se.Status == 403 || se.Status == 404 || strings.Contains(se.Body, "authorization_pending")):
			return pollResult{state: "pending"}
		case errors.As(e, &se) && strings.Contains(se.Body, "slow_down"):
			return pollResult{state: "slow"}
		}
		return pollResult{state: "fail", err: e}
	})
	if err != nil {
		return Result{}, err
	}
	t, err := x.exchange(ctx, grant.Code, grant.Verifier, x.AuthBase+"/deviceauth/callback")
	return Result{Token: t}, err
}

// Refresh implements Flow.
func (x *Codex) Refresh(ctx context.Context, t Token) (Token, error) {
	if t.Refresh == "" {
		return Token{}, errors.New("the stored ChatGPT login has no refresh token")
	}
	data, err := x.postForm(ctx, "OpenAI token refresh", x.AuthBase+"/oauth/token", url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {t.Refresh}, "client_id": {codexClientID}}, nil)
	if err != nil {
		return Token{}, err
	}
	n, err := x.token("OpenAI token refresh", data)
	return n, err
}
