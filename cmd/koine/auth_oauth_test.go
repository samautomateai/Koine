package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/core/app"
	"koine/core/auth/oauth"
)

// rewrite sends every non-loopback request to the fake server, so no login ever reaches a real provider.
type rewrite struct{ to *url.URL }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if h := req.URL.Hostname(); h == "127.0.0.1" || h == "localhost" {
		return http.DefaultTransport.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

// fakeProviders answers the token and key endpoints of every flow.
func fakeProviders(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth/token"):
			_, _ = w.Write([]byte(`{"access_token":"fake-claude-access","refresh_token":"fake-claude-refresh","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/auth/keys"):
			_, _ = w.Write([]byte(`{"key":"fake-or-key"}`))
		case r.URL.Path == "/login/device/code":
			_, _ = w.Write([]byte(`{"device_code":"d","user_code":"CODE-1","interval":1,"expires_in":900,` +
				`"verification_uri":"https://github.com/login/device"}`))
		case r.URL.Path == "/login/oauth/access_token":
			_, _ = w.Write([]byte(`{"access_token":"fake-gh"}`))
		case r.URL.Path == "/copilot_internal/v2/token":
			_, _ = w.Write([]byte(`{"token":"fake-copilot","expires_at":4102444800}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	oldClient, oldOpen := oauthClient, app.BrowserOpener
	oauthClient = oauth.Client{HTTP: &http.Client{Transport: rewrite{u}}, Sleep: func(context.Context, time.Duration) error { return nil }}
	app.BrowserOpener = func(target string) error {
		q, _ := url.Parse(target)
		go func() {
			if resp, err := http.Get(q.Query().Get("callback_url") + "?code=cb-code"); err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { oauthClient, app.BrowserOpener = oldClient, oldOpen })
}

func TestAuthLoginSignsInWithAnAccountAndTheLoginIsUsable(t *testing.T) {
	dir := authEnv(t)
	fakeProviders(t)
	code, out, errs := authCLI(t, "pasted-code#\n", true, "login", "anthropic", "--no-browser")
	if code != 0 || !strings.Contains(out, "signed in to Anthropic") || !strings.Contains(out, "terms") || strings.Contains(out+errs, "fake-claude-access") {
		t.Fatalf("login: %d %q %q", code, out, errs)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if !strings.Contains(string(b), `"type": "oauth"`) || !strings.Contains(string(b), "fake-claude-refresh") {
		t.Errorf("auth.json: %s", b)
	}
	if code, out, _ := authCLI(t, "", false, "check", "--provider", "anthropic"); code != 0 || out != "ready\n" {
		t.Errorf("check: %d %q", code, out)
	}
	if code, out, _ := authCLI(t, "", false, "check", "--provider", "anthropic", "--json", "--credentials"); code != 0 ||
		!strings.Contains(out, `"type":"oauth"`) || !strings.Contains(out, "fake-claude-access") {
		t.Errorf("json: %d %q", code, out)
	}
	if code, out, _ := authCLI(t, "", false, "print-bearer-token", "--provider", "anthropic", "--min-expiry", "30m"); code != 0 ||
		out != "fake-claude-access\n" {
		t.Errorf("print-bearer-token: %d %q", code, out)
	}
	if code, _, errs := authCLI(t, "", false, "print-api-key", "--provider", "anthropic"); code != 1 || !strings.Contains(errs, "print-bearer-token") {
		t.Errorf("print-api-key: %d %q", code, errs)
	}
	// a login that lapses within --min-expiry is renewed first; with --no-refresh it is reported
	if code, out, _ := authCLI(t, "", false, "print-bearer-token", "--provider", "anthropic", "--min-expiry", "3h"); code != 0 ||
		out != "fake-claude-access\n" {
		t.Errorf("min-expiry renewal: %d %q", code, out)
	}
	if code, out, errs := authCLI(t, "", false, "check", "--provider", "anthropic", "--no-refresh", "--json"); code != 0 {
		t.Errorf("fresh login with --no-refresh: %d %q %q", code, out, errs)
	}
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"anthropic":{"type":"oauth","access":"a","refresh":"r","expires":1}}`), 0o600)
	if code, out, _ := authCLI(t, "", false, "check", "--provider", "anthropic", "--no-refresh"); code != 2 || out != "invalid\n" {
		t.Errorf("expired with --no-refresh: %d %q", code, out)
	}
	if code, out, _ := authCLI(t, "", false, "check", "--provider", "anthropic"); code != 0 || out != "ready\n" {
		t.Errorf("expired login is renewed: %d %q", code, out)
	}
}

func TestLoginShortcutsAndOtherProviders(t *testing.T) {
	dir := authEnv(t)
	fakeProviders(t)
	var o, e strings.Builder
	st := streams{In: strings.NewReader("\n"), Out: &o, Err: &e, InTTY: true}
	if code := run([]string{"login", "github-copilot"}, st); code != 0 || !strings.Contains(o.String(), "CODE-1") || !strings.Contains(e.String(),
		"Enterprise") {
		t.Fatalf("copilot: %d %q %q", code, o.String(), e.String())
	}
	o.Reset()
	if code := run([]string{"login", "openrouter"}, streams{In: strings.NewReader(""), Out: &o, Err: &e, InTTY: true}); code != 0 ||
		!strings.Contains(o.String(), "API key") {
		t.Fatalf("openrouter: %d %q", code, o.String())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if !strings.Contains(string(b), "fake-copilot") || !strings.Contains(string(b), `"key": "fake-or-key"`) {
		t.Errorf("auth.json: %s", b)
	}
	o.Reset()
	if code := run([]string{"logout", "github-copilot"}, streams{Out: &o, Err: &e}); code != 0 || !strings.Contains(o.String(), "Removed") {
		t.Errorf("logout: %d %q", code, o.String())
	}
	if code, _, errs := authCLI(t, "", true, "login", "openai-codex", "--api-key"); code != 2 || !strings.Contains(errs, "has no API key") {
		t.Errorf("codex --api-key: %d %q", code, errs)
	}
	// a failed login is reported and stores nothing
	if code, _, errs := authCLI(t, "", true, "login", "openai-codex", "--no-browser"); code != 1 || !strings.Contains(errs, "signing in to") {
		t.Errorf("failed login: %d %q", code, errs)
	}
	if code, _, errs := authCLI(t, "", true, "login", "anthropic", "--no-browser"); code != 1 || !strings.Contains(errs, "no input") {
		t.Errorf("no pasted input: %d %q", code, errs)
	}
}
