package oauth

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const tokenJSON = `{"access_token":"fake-access","refresh_token":"fake-refresh","expires_in":3600}`

func anthropicFor(ts *tokenServer, c *clock) *Anthropic {
	a := NewAnthropic(c.client())
	a.TokenURL = ts.URL + "/token"
	a.AuthorizeURL = "https://claude.example/oauth/authorize"
	a.CopyCodeURL = "https://claude.example/oauth/code/callback"
	a.Port = 0
	return a
}

func TestAnthropicBrowserLoginUsesPKCEAndTheLoopbackCallback(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, tokenJSON)
	a := anthropicFor(ts, c)
	var seen url.Values
	ui := quickUI(t, nil)
	ui.OpenBrowser = browser(t, "the-code", true, &seen)
	res, err := a.Login(context.Background(), ui, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := c.Now().Add(time.Hour - 5*time.Minute).UnixMilli()
	if res.Token.Access != "fake-access" || res.Token.Refresh != "fake-refresh" || res.Token.Expires != want {
		t.Errorf("token %+v, want expiry %d", res.Token, want)
	}
	req := ts.last(t)
	if req["grant_type"] != "authorization_code" || req["code"] != "the-code" || req["client_id"] != anthropicClientID {
		t.Errorf("exchange %v", req)
	}
	if challengeOf(req["code_verifier"]) != seen.Get("code_challenge") || seen.Get("code_challenge_method") != "S256" {
		t.Errorf("the verifier does not match the challenge: %v / %v", req, seen)
	}
	if req["redirect_uri"] != seen.Get("redirect_uri") || !strings.HasPrefix(seen.Get("redirect_uri"), "http://localhost:") ||
		req["state"] != seen.Get("state") || seen.Get("state") == "" {
		t.Errorf("redirect or state differ: %v / %v", req, seen)
	}
	if !strings.Contains(seen.Get("scope"), "user:inference") || seen.Get("client_id") != anthropicClientID {
		t.Errorf("authorize query %v", seen)
	}
}

func TestAnthropicFallsBackToAnotherPortWhenThePreferredOneIsBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	busy := ln.Addr().(*net.TCPAddr).Port
	c := newClock()
	ts := newTokenServer(t, 200, tokenJSON)
	a := anthropicFor(ts, c)
	a.Port = busy
	var seen url.Values
	ui := quickUI(t, nil)
	ui.OpenBrowser = browser(t, "c", true, &seen)
	if _, err := a.Login(context.Background(), ui, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(seen.Get("redirect_uri"), ":"+strconv.Itoa(busy)+"/") {
		t.Errorf("used the busy port: %s", seen.Get("redirect_uri"))
	}
}

func TestAnthropicCopyCodeLoginTakesAPastedCode(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, tokenJSON)
	a := anthropicFor(ts, c)
	var notes []string
	ui := quickUI(t, &notes)
	opened := false
	ui.OpenBrowser = func(string) error { opened = true; return nil }
	ui.Prompt = func(q string, secret bool) (string, error) {
		u, _ := url.Parse(strings.TrimPrefix(notes[0], "Open this address to sign in:\n"))
		if u.Query().Get("redirect_uri") != a.CopyCodeURL || secret {
			t.Errorf("authorize %v secret=%v", u.Query(), secret)
		}
		return "pasted-code#" + u.Query().Get("state"), nil
	}
	res, err := a.Login(context.Background(), ui, Options{NoBrowser: true})
	if err != nil || res.Token.Access != "fake-access" {
		t.Fatalf("%+v %v", res, err)
	}
	if opened {
		t.Error("--no-browser opened a browser")
	}
	if r := ts.last(t); r["code"] != "pasted-code" || r["redirect_uri"] != a.CopyCodeURL {
		t.Errorf("exchange %v", r)
	}
}

func TestAnthropicPasteWinsTheRaceWhenTheBrowserIsElsewhere(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, tokenJSON)
	a := anthropicFor(ts, c)
	var notes []string
	ui := quickUI(t, &notes)
	ui.Race = true
	ui.Prompt = func(string, bool) (string, error) {
		u, _ := url.Parse(strings.TrimPrefix(notes[0], "Open this address to sign in:\n"))
		return u.Query().Get("redirect_uri") + "?code=remote-code&state=" + u.Query().Get("state"), nil
	}
	if _, err := a.Login(context.Background(), ui, Options{}); err != nil {
		t.Fatal(err)
	}
	if r := ts.last(t); r["code"] != "remote-code" {
		t.Errorf("exchange %v", r)
	}
}

func TestAnthropicLoginFailuresAreExplained(t *testing.T) {
	c := newClock()
	run := func(status int, body string, ui UI, opt Options) error {
		ts := newTokenServer(t, status, body)
		_, err := anthropicFor(ts, c).Login(context.Background(), ui, opt)
		return err
	}
	pasteWith := func(line string) UI {
		ui := quickUI(t, nil)
		ui.Prompt = func(string, bool) (string, error) { return line, nil }
		return ui
	}
	for name, tc := range map[string]struct {
		status int
		body   string
		ui     UI
		want   string
	}{
		"state mismatch": {200, tokenJSON, pasteWith("code#someone-elses-state"), "state mismatch"},
		"no code":        {200, tokenJSON, pasteWith("   "), "no authorization code"},
		"server refuses": {400, `{"error":"invalid_grant"}`, pasteWith("code#"), "HTTP 400"},
		"not json":       {200, `<html>`, pasteWith("code#"), "not JSON"},
		"no refresh":     {200, `{"access_token":"a","expires_in":10}`, pasteWith("code#"), "lacks"},
	} {
		if err := run(tc.status, tc.body, tc.ui, Options{NoBrowser: true}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
	// the user never finishes
	slow := quickUI(t, nil)
	slow.Timeout = 30 * time.Millisecond
	if err := run(200, tokenJSON, slow, Options{}); err == nil || !strings.Contains(err.Error(), "did not finish in time") {
		t.Errorf("timeout: %v", err)
	}
	// the browser reports a refusal
	denied := quickUI(t, nil)
	denied.OpenBrowser = func(target string) error {
		u, _ := url.Parse(target)
		go func() {
			_ = getQuiet(u.Query().Get("redirect_uri") + "?error=access_denied&error_description=no&state=" + u.Query().Get("state"))
		}()
		return nil
	}
	if err := run(200, tokenJSON, denied, Options{}); err == nil || !strings.Contains(err.Error(), "access_denied: no") {
		t.Errorf("refusal: %v", err)
	}
	// nowhere to paste and no callback
	if err := run(200, tokenJSON, quickUI(t, nil), Options{NoBrowser: true}); err == nil || !strings.Contains(err.Error(), "nowhere to paste") {
		t.Errorf("no prompt: %v", err)
	}
	// a prompt that fails
	failing := quickUI(t, nil)
	failing.Prompt = func(string, bool) (string, error) { return "", context.Canceled }
	if err := run(200, tokenJSON, failing, Options{NoBrowser: true}); err == nil {
		t.Error("a failing prompt should fail the login")
	}
}

func TestAnthropicRefresh(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, `{"access_token":"new-access","expires_in":1800}`)
	a := anthropicFor(ts, c)
	old := Token{Access: "old", Refresh: "old-refresh", Expires: 1, Extra: map[string]string{"k": "v"}}
	n, err := a.Refresh(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	if n.Access != "new-access" || n.Refresh != "old-refresh" || n.Extra["k"] != "v" || n.Expires != c.Now().Add(25*time.Minute).UnixMilli() {
		t.Errorf("refreshed %+v", n)
	}
	if r := ts.last(t); r["grant_type"] != "refresh_token" || r["refresh_token"] != "old-refresh" || r["client_id"] != anthropicClientID {
		t.Errorf("request %v", r)
	}
	if _, err := a.Refresh(context.Background(), Token{Access: "x"}); err == nil {
		t.Error("a login without a refresh token cannot be refreshed")
	}
	bad := anthropicFor(newTokenServer(t, 401, `{"error":"invalid_grant"}`), c)
	if _, err := bad.Refresh(context.Background(), old); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("rejected refresh: %v", err)
	}
	empty := anthropicFor(newTokenServer(t, 200, `{}`), c)
	if _, err := empty.Refresh(context.Background(), old); err == nil {
		t.Error("an empty answer is an error")
	}
}

func TestAnthropicNoticeWarnsAboutTheTerms(t *testing.T) {
	a := NewAnthropic(Client{})
	if !strings.Contains(a.Notice(), "terms") || a.ID() != "anthropic" || !strings.Contains(a.Name(), "Claude") {
		t.Errorf("%q %q %q", a.ID(), a.Name(), a.Notice())
	}
}
