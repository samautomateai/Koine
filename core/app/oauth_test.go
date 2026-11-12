package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"koine/core/agentdir"
	"koine/core/auth/oauth"
)

// toServer sends every request to the fake server, whatever host the flow thinks it is talking to; the loopback callback stays real.
type toServer struct{ target *url.URL }

func (r toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	if h := req.URL.Hostname(); h == "127.0.0.1" || h == "localhost" {
		return http.DefaultTransport.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func fakeAccounts(t *testing.T, status int, body string) (oauth.Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return oauth.Client{HTTP: &http.Client{Transport: toServer{u}}}, &n
}

const expiredClaude = `{"anthropic":{"type":"oauth","access":"old-access","refresh":"old-refresh","expires":1,"availableModelIds":["x"]}}`

func TestAnExpiredLoginIsRenewedStoredAndPreferredToTheEnvironment(t *testing.T) {
	home := providerEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "fake-env-key")
	writeAgentFile(t, home, "auth.json", expiredClaude)
	a := startPlain(t, t.TempDir())
	c, hits := fakeAccounts(t, 200, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	a.providers.OAuth = c
	cred, err := a.router.Credential("anthropic")
	if err != nil || cred.Key != "new-access" || !cred.OAuth || !cred.Bearer || !strings.Contains(cred.Source, "OAuth") {
		t.Fatalf("%+v %v", cred, err)
	}
	if _, err := a.router.Credential("anthropic"); err != nil || hits.Load() != 1 {
		t.Errorf("a fresh login must not be renewed again: %d requests, %v", hits.Load(), err)
	}
	b, _ := os.ReadFile(filepath.Join(agentdir.Of(home), "auth.json"))
	if !strings.Contains(string(b), "new-refresh") || !strings.Contains(string(b), "availableModelIds") || strings.Contains(string(b), "old-access") {
		t.Errorf("auth.json after renewal: %s", b)
	}
}

func TestAFailedRenewalFallsThroughToTheEnvironmentAndIsReportedOtherwise(t *testing.T) {
	home := providerEnv(t)
	writeAgentFile(t, home, "auth.json", expiredClaude)
	a := startPlain(t, t.TempDir())
	a.providers.OAuth, _ = fakeAccounts(t, 400, `{"error":"invalid_grant"}`)
	if _, err := a.router.Credential("anthropic"); err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Errorf("without a fallback: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "fake-env-key")
	if c, err := a.router.Credential("anthropic"); err != nil || c.Key != "fake-env-key" {
		t.Errorf("with a fallback: %+v %v", c, err)
	}
}

func TestNoRefreshAndSkewDecideWhenALoginCountsAsExpired(t *testing.T) {
	home := providerEnv(t)
	far := strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
	writeAgentFile(t, home, "auth.json", `{"anthropic":{"type":"oauth","access":"a1","refresh":"r1","expires":`+far+`},
	  "openai-codex":{"type":"oauth","access":"","refresh":"r","expires":`+far+`},
	  "xai":{"type":"oauth","access":"x","refresh":"r","expires":`+far+`}}`)
	a := startPlain(t, t.TempDir())
	c, hits := fakeAccounts(t, 200, `{"access_token":"a2","refresh_token":"r2","expires_in":3600}`)
	a.providers.OAuth = c
	if cred, err := a.router.Credential("anthropic"); err != nil || cred.Key != "a1" || hits.Load() != 0 {
		t.Fatalf("fresh: %+v %v", cred, err)
	}
	a.providers.Skew, a.providers.NoRefresh = 2*time.Hour, true
	if _, err := a.router.Credential("anthropic"); err == nil || !strings.Contains(err.Error(), "--no-refresh") {
		t.Errorf("no-refresh: %v", err)
	}
	a.providers.NoRefresh = false
	if cred, err := a.router.Credential("anthropic"); err != nil || cred.Key != "a2" {
		t.Errorf("skew forces a renewal: %+v %v", cred, err)
	}
	if _, err := a.router.Credential("openai-codex"); err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Errorf("empty access token: %v", err)
	}
	if _, err := a.router.Credential("xai"); err == nil || !strings.Contains(err.Error(), "Koine cannot use") {
		t.Errorf("no flow: %v", err)
	}
}

func TestLoginInASessionSignsInWithTheAccountOrAKey(t *testing.T) {
	providerEnv(t)
	a := startPlain(t, t.TempDir())
	c, _ := fakeAccounts(t, 200, `{"key":"fake-or-key"}`)
	a.providers.OAuth = c
	oldOpen := BrowserOpener
	t.Cleanup(func() { BrowserOpener = oldOpen })
	BrowserOpener = func(target string) error {
		u, _ := url.Parse(target)
		go func() {
			if resp, err := http.Get(u.Query().Get("callback_url") + "?code=abc"); err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	notices, errs := typed(t, a, "/login openrouter")
	if len(errs) != 0 || !strings.Contains(strings.Join(notices, "\n"), "stored its API key") {
		t.Fatalf("openrouter: %v %v", notices, errs)
	}
	if c, err := a.router.Credential("openrouter"); err != nil || c.Key != "fake-or-key" {
		t.Errorf("stored key: %+v %v", c, err)
	}
	// the choice between the account and a key
	a.Choose = func(title string, opts []string) int { return 1 }
	a.Prompt = func(q string, secret bool) (string, error) { return "fake-typed-key", nil }
	if _, errs := typed(t, a, "/login anthropic"); len(errs) != 0 {
		t.Errorf("anthropic key: %v", errs)
	}
	if c, _ := a.router.Credential("anthropic"); c.Key != "fake-typed-key" || c.OAuth {
		t.Errorf("anthropic: %+v", c)
	}
	a.Choose = nil
	for line, want := range map[string]string{
		"/login openai-codex --api-key": "has no API key",
		"/login nosuch":                 "unknown provider",
	} {
		if _, errs := typed(t, a, line); len(errs) != 1 || !strings.Contains(errs[0], want) {
			t.Errorf("%s: %v", line, errs)
		}
	}
	a.NoUserBash = true
	if _, errs := typed(t, a, "/login github-copilot"); len(errs) != 1 || !strings.Contains(errs[0], "nobody to sign in") {
		t.Errorf("print mode: %v", errs)
	}
	a.NoUserBash = false
	if notices, _ := typed(t, a, "/login"); len(notices) != 1 || !strings.Contains(notices[0], "Sign in with your account: anthropic, openai-codex") {
		t.Errorf("usage: %v", notices)
	}
}

func TestLoginOAuthReportsFailuresAndUnknownProviders(t *testing.T) {
	providerEnv(t)
	a := startPlain(t, t.TempDir())
	if _, err := a.providers.LoginOAuth(context.Background(), "groq", oauth.UI{}, oauth.Options{}); err == nil {
		t.Error("groq has no account sign-in")
	}
	c, _ := fakeAccounts(t, 500, `x`)
	a.providers.OAuth = c
	var said []string
	ui := oauth.UI{Timeout: time.Second, Prompt: func(string, bool) (string, error) { return "code#", nil }, Notify: func(m string) { said = append(said, m) }}
	if _, err := a.providers.LoginOAuth(context.Background(), "anthropic", ui, oauth.Options{NoBrowser: true}); err == nil ||
		!strings.Contains(err.Error(), "HTTP 500") || len(said) < 2 || !strings.Contains(said[0], "terms") {
		t.Errorf("%v %v", err, said)
	}
}
