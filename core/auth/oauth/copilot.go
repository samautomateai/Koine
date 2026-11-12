package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Copilot signs in to GitHub Copilot with GitHub's device flow. The GitHub token (refresh) is exchanged for a short-lived Copilot token
// (access), which is what requests carry. Enterprise hosts sign in against their own domain.
type Copilot struct {
	Client
	// Domain-derived endpoints; empty means github.com. Tests point these at a fake server.
	DeviceURL, AccessURL, TokenURL string
}

const (
	copilotClientID = "Iv1.b507a08c87ecfe98"
	copilotSkew     = 5 * time.Minute
	copilotAgent    = "GitHubCopilotChat/0.35.0"
)

// CopilotHeaders are sent with every Copilot request; the API refuses clients that do not look like an editor plugin.
var CopilotHeaders = map[string]string{
	"User-Agent":             copilotAgent,
	"Editor-Version":         "vscode/1.107.0",
	"Editor-Plugin-Version":  "copilot-chat/0.35.0",
	"Copilot-Integration-Id": "vscode-chat",
}

// NewCopilot returns the GitHub Copilot flow with its public endpoints.
func NewCopilot(c Client) *Copilot { return &Copilot{Client: c} }

// ID implements Flow.
func (g *Copilot) ID() string { return "github-copilot" }

// Name implements Flow.
func (g *Copilot) Name() string { return "GitHub Copilot" }

// Notice implements Flow.
func (g *Copilot) Notice() string { return "" }

// domainOf cleans what a user types for an enterprise host: a bare host name or a URL.
func domainOf(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", nil
	}
	if !strings.Contains(in, "://") {
		in = "https://" + in
	}
	u, err := url.Parse(in)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("that is not a host name or URL")
	}
	return u.Hostname(), nil
}

func (g *Copilot) urls(domain string) (device, access, token string) {
	if domain == "" {
		domain = "github.com"
	}
	device, access, token = "https://"+domain+"/login/device/code", "https://"+domain+"/login/oauth/access_token",
		"https://api."+domain+"/copilot_internal/v2/token"
	if g.DeviceURL != "" {
		device = g.DeviceURL
	}
	if g.AccessURL != "" {
		access = g.AccessURL
	}
	if g.TokenURL != "" {
		token = g.TokenURL
	}
	return device, access, token
}

// Login implements Flow.
func (g *Copilot) Login(ctx context.Context, ui UI, opt Options) (Result, error) {
	domain := ""
	if ui.Prompt != nil {
		in, err := ui.Prompt("GitHub Enterprise host (leave blank for github.com): ", false)
		if err != nil {
			return Result{}, err
		}
		if domain, err = domainOf(in); err != nil {
			return Result{}, fmt.Errorf("GitHub Enterprise host: %w", err)
		}
	}
	deviceURL, accessURL, _ := g.urls(domain)
	data, err := g.postForm(ctx, "GitHub device code request", deviceURL, url.Values{"client_id": {copilotClientID}, "scope": {"read:user"}},
		map[string]string{"User-Agent": copilotAgent})
	if err != nil {
		return Result{}, err
	}
	var dev struct {
		DeviceCode string  `json:"device_code"`
		UserCode   string  `json:"user_code"`
		URI        string  `json:"verification_uri"`
		Interval   float64 `json:"interval"`
		ExpiresIn  float64 `json:"expires_in"`
	}
	if json.Unmarshal(data, &dev) != nil || dev.DeviceCode == "" || dev.UserCode == "" || dev.ExpiresIn <= 0 {
		return Result{}, errors.New("GitHub answered the device code request with something unexpected")
	}
	if !strings.HasPrefix(dev.URI, "https://") { // the page is shown and may be opened, so it must be a web address
		return Result{}, errors.New("GitHub gave a verification address that is not an https URL")
	}
	ui.notify("Open %s and enter the code %s", dev.URI, dev.UserCode)
	if !opt.NoBrowser && ui.OpenBrowser != nil {
		_ = ui.OpenBrowser(dev.URI)
	}
	var github string
	interval := time.Duration(dev.Interval * float64(time.Second))
	if interval == 0 {
		interval = 5 * time.Second
	}
	err = g.pollDevice(ctx, interval, time.Duration(dev.ExpiresIn*float64(time.Second)), true, func() pollResult {
		b, e := g.postForm(ctx, "GitHub device authorization", accessURL, url.Values{"client_id": {copilotClientID},
			"device_code": {dev.DeviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, map[string]string{"User-Agent": copilotAgent})
		if e != nil {
			return pollResult{state: "fail", err: e}
		}
		var r struct {
			Token    string  `json:"access_token"`
			Error    string  `json:"error"`
			Desc     string  `json:"error_description"`
			Interval float64 `json:"interval"`
		}
		_ = json.Unmarshal(b, &r)
		switch {
		case r.Token != "":
			github = r.Token
			return pollResult{state: "done"}
		case r.Error == "authorization_pending":
			return pollResult{state: "pending"}
		case r.Error == "slow_down":
			return pollResult{state: "slow", interval: time.Duration(r.Interval * float64(time.Second))}
		case r.Error != "":
			return pollResult{state: "fail", err: fmt.Errorf("GitHub sign-in failed: %s %s", r.Error, r.Desc)}
		}
		return pollResult{state: "fail", err: errors.New("GitHub answered the device authorization with something unexpected")}
	})
	if err != nil {
		return Result{}, err
	}
	t, err := g.copilotToken(ctx, github, domain)
	return Result{Token: t}, err
}

// copilotToken trades the GitHub token for a Copilot token.
func (g *Copilot) copilotToken(ctx context.Context, github, domain string) (Token, error) {
	_, _, tokenURL := g.urls(domain)
	hdr := map[string]string{"Authorization": "Bearer " + github}
	for k, v := range CopilotHeaders {
		hdr[k] = v
	}
	data, err := g.send(ctx, "Copilot token request", "GET", tokenURL, nil, hdr)
	if err != nil {
		return Token{}, err
	}
	var r struct {
		Token     string  `json:"token"`
		ExpiresAt float64 `json:"expires_at"`
	}
	if json.Unmarshal(data, &r) != nil || r.Token == "" || r.ExpiresAt <= 0 {
		return Token{}, errors.New("GitHub answered the Copilot token request with something unexpected (is Copilot enabled for this account?)")
	}
	t := Token{Access: r.Token, Refresh: github, Expires: int64(r.ExpiresAt*1000) - copilotSkew.Milliseconds()}
	if domain != "" {
		t.Extra = map[string]string{"enterpriseUrl": domain}
	}
	return t, nil
}

// Refresh implements Flow: the GitHub token is long-lived, so refreshing fetches a new Copilot token with it.
func (g *Copilot) Refresh(ctx context.Context, t Token) (Token, error) {
	if t.Refresh == "" {
		return Token{}, errors.New("the stored Copilot login has no GitHub token")
	}
	return g.copilotToken(ctx, t.Refresh, t.Extra["enterpriseUrl"])
}
