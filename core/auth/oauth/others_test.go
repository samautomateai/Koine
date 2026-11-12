package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func jwt(account string) string {
	p, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
	return "h." + base64.RawURLEncoding.EncodeToString(p) + ".s"
}

func codexTokens(account string) string {
	return fmt.Sprintf(`{"access_token":%q,"refresh_token":"r1","expires_in":600}`, jwt(account))
}

func codexFor(ts *tokenServer, c *clock) *Codex {
	x := NewCodex(c.client())
	x.AuthBase = ts.URL
	x.Port = 0
	return x
}

func TestCodexBrowserLoginStoresTheAccountIdFromTheToken(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, codexTokens("acct-42"))
	x := codexFor(ts, c)
	var seen url.Values
	ui := quickUI(t, nil)
	ui.OpenBrowser = browser(t, "c1", true, &seen)
	res, err := x.Login(context.Background(), ui, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Token.Extra["accountId"] != "acct-42" || res.Token.Refresh != "r1" || res.Token.Expires != c.Now().Add(10*time.Minute).UnixMilli() {
		t.Errorf("token %+v", res.Token)
	}
	r := ts.last(t)
	if r["grant_type"] != "authorization_code" || r["client_id"] != codexClientID || challengeOf(r["code_verifier"]) != seen.Get("code_challenge") ||
		r["redirect_uri"] != seen.Get("redirect_uri") || seen.Get("originator") != "koine" {
		t.Errorf("exchange %v / %v", r, seen)
	}
}

func TestCodexBrowserLoginWithoutAListenerTakesAPastedURL(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, codexTokens("a"))
	x := codexFor(ts, c)
	hold, err := net.Listen("tcp", "127.0.0.1:0") // occupies the port Codex insists on
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Close() }()
	x.Port = hold.Addr().(*net.TCPAddr).Port
	var notes []string
	ui := quickUI(t, &notes)
	ui.Prompt = func(string, bool) (string, error) {
		u, _ := url.Parse(strings.TrimPrefix(notes[len(notes)-1], "Open this address to sign in:\n"))
		return fmt.Sprintf("http://localhost:%d/auth/callback?code=pasted&state=%s", x.Port, u.Query().Get("state")), nil
	}
	if _, err := x.Login(context.Background(), ui, Options{}); err != nil {
		t.Fatal(err)
	}
	if r := ts.last(t); r["code"] != "pasted" || !strings.Contains(notes[0], "Could not listen") {
		t.Errorf("%v %v", r, notes)
	}
}

func TestCodexDeviceFlowPollsUntilAuthorised(t *testing.T) {
	c := newClock()
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = w.Write([]byte(`{"device_auth_id":"dev1","user_code":"ABCD-1234","interval":"2"}`))
		case "/api/accounts/deviceauth/token":
			switch polls.Add(1) {
			case 1:
				w.WriteHeader(403)
			case 2:
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"code":"slow_down"}}`))
			case 3:
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"deviceauth_authorization_pending"}`))
			default:
				_, _ = w.Write([]byte(`{"authorization_code":"auth-code","code_verifier":"server-verifier"}`))
			}
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "auth-code" || r.Form.Get("code_verifier") != "server-verifier" || !strings.HasSuffix(r.Form.Get("redirect_uri"),
				"/deviceauth/callback") {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(codexTokens("dev-acct")))
		}
	}))
	defer srv.Close()
	x := NewCodex(c.client())
	x.AuthBase = srv.URL
	var notes []string
	res, err := x.Login(context.Background(), quickUI(t, &notes), Options{NoBrowser: true})
	if err != nil || res.Token.Extra["accountId"] != "dev-acct" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "ABCD-1234") || !strings.Contains(notes[0], "/codex/device") {
		t.Errorf("notes %v", notes)
	}
	if polls.Load() != 4 || c.slept[0] != 2*time.Second || c.slept[1] != 7*time.Second {
		t.Errorf("polls %d, sleeps %v", polls.Load(), c.slept)
	}
}

func TestCodexDeviceFlowFailures(t *testing.T) {
	c := newClock()
	serve := func(usercode, token func(w http.ResponseWriter)) *Codex {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "usercode") {
				usercode(w)
			} else {
				token(w)
			}
		}))
		t.Cleanup(srv.Close)
		x := NewCodex(c.client())
		x.AuthBase = srv.URL
		return x
	}
	ok := func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"device_auth_id":"d","user_code":"U","interval":1}`))
	}
	for name, tc := range map[string]struct {
		x    *Codex
		want string
	}{
		"not enabled":     {serve(func(w http.ResponseWriter) { w.WriteHeader(404) }, nil), "not enabled"},
		"server error":    {serve(func(w http.ResponseWriter) { w.WriteHeader(500) }, nil), "HTTP 500"},
		"odd usercode":    {serve(func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{}`)) }, nil), "unexpected"},
		"poll fails":      {serve(ok, func(w http.ResponseWriter) { w.WriteHeader(500) }), "HTTP 500"},
		"odd grant":       {serve(ok, func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{}`)) }), "unexpected"},
		"never approved":  {serve(ok, func(w http.ResponseWriter) { w.WriteHeader(403) }), "expired before"},
		"expired by slow": {serve(ok, func(w http.ResponseWriter) { w.WriteHeader(400); _, _ = w.Write([]byte("slow_down")) }), "slow down"},
	} {
		if _, err := tc.x.Login(context.Background(), quickUI(t, nil), Options{NoBrowser: true}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
	// cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	x := serve(ok, func(w http.ResponseWriter) { w.WriteHeader(403) })
	if _, err := x.Login(ctx, quickUI(t, nil), Options{NoBrowser: true}); err == nil {
		t.Error("a cancelled login must fail")
	}
}

func TestCodexTokensNeedAnAccountAndRefreshKeepsIt(t *testing.T) {
	c := newClock()
	if _, err := codexFor(newTokenServer(t, 200, `{"access_token":"not-a-jwt","refresh_token":"r","expires_in":5}`), c).
		Refresh(context.Background(), Token{Refresh: "r"}); err == nil || !strings.Contains(err.Error(), "account id") {
		t.Errorf("no account: %v", err)
	}
	ts := newTokenServer(t, 200, codexTokens("again"))
	n, err := codexFor(ts, c).Refresh(context.Background(), Token{Refresh: "old"})
	if err != nil || n.Extra["accountId"] != "again" {
		t.Fatalf("%+v %v", n, err)
	}
	if r := ts.last(t); r["grant_type"] != "refresh_token" || r["refresh_token"] != "old" {
		t.Errorf("request %v", r)
	}
	if _, err := codexFor(ts, c).Refresh(context.Background(), Token{}); err == nil {
		t.Error("no refresh token")
	}
	if _, err := codexFor(newTokenServer(t, 400, "bad"), c).Refresh(context.Background(), Token{Refresh: "x"}); err == nil {
		t.Error("rejected refresh")
	}
	for _, bad := range []string{"", "a.b", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("no json")) + ".c"} {
		if accountID(bad) != "" {
			t.Errorf("accountID(%q) should be empty", bad)
		}
	}
	if x := NewCodex(Client{}); x.ID() != "openai-codex" || x.Name() == "" || x.Notice() == "" {
		t.Error("identity")
	}
}

// copilotServer fakes github.com and api.github.com.
func copilotServer(t *testing.T, poll func(n int32) string) (*Copilot, *clock, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != copilotClientID || r.Form.Get("scope") != "read:user" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"WXYZ","interval":5,"expires_in":900,` +
				`"verification_uri":"https://github.example/login/device"}`))
		case "/access":
			_, _ = w.Write([]byte(poll(n.Add(1))))
		case "/copilot":
			if r.Header.Get("Authorization") != "Bearer gh-token" && r.Header.Get("Authorization") != "Bearer stored-gh" {
				w.WriteHeader(401)
				return
			}
			if r.Header.Get("Editor-Version") == "" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"token":"tid=1;exp=2;proxy-ep=proxy.individual.githubcopilot.com;","expires_at":1893456000}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := newClock()
	g := NewCopilot(c.client())
	g.DeviceURL, g.AccessURL, g.TokenURL = srv.URL+"/device", srv.URL+"/access", srv.URL+"/copilot"
	return g, c, &n
}

func TestCopilotDeviceFlow(t *testing.T) {
	g, c, polls := copilotServer(t, func(n int32) string {
		return map[int32]string{1: `{"error":"authorization_pending"}`, 2: `{"error":"slow_down","interval":10}`}[n] + ternary(n >= 3,
			`{"access_token":"gh-token"}`)
	})
	var notes []string
	ui := quickUI(t, &notes)
	var opened string
	ui.OpenBrowser = func(u string) error { opened = u; return nil }
	asked := ""
	ui.Prompt = func(q string, _ bool) (string, error) { asked = q; return "  ", nil }
	res, err := g.Login(context.Background(), ui, Options{})
	if err != nil {
		t.Fatal(err)
	}
	wantExp := int64(1893456000)*1000 - 5*60*1000
	if res.Token.Refresh != "gh-token" || !strings.Contains(res.Token.Access, "proxy-ep=") || res.Token.Expires != wantExp || res.Token.Extra != nil {
		t.Errorf("token %+v", res.Token)
	}
	if opened != "https://github.example/login/device" || !strings.Contains(notes[0], "WXYZ") || !strings.Contains(asked, "Enterprise") {
		t.Errorf("opened %q notes %v asked %q", opened, notes, asked)
	}
	if polls.Load() != 3 || c.slept[0] != 5*time.Second || c.slept[2] != 10*time.Second {
		t.Errorf("polls %d sleeps %v", polls.Load(), c.slept)
	}
	// --no-browser does not open the page
	opened = ""
	ui.Prompt = nil
	if _, err := g.Login(context.Background(), ui, Options{NoBrowser: true}); err != nil || opened != "" {
		t.Errorf("no-browser: %v opened %q", err, opened)
	}
}

func ternary(c bool, s string) string {
	if c {
		return s
	}
	return ""
}

func TestCopilotFailuresAndRefresh(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"denied":  {`{"error":"access_denied","error_description":"nope"}`, "access_denied nope"},
		"garbage": {`{}`, "unexpected"},
	} {
		g, _, _ := copilotServer(t, func(int32) string { return tc.body })
		if _, err := g.Login(context.Background(), quickUI(t, nil), Options{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	g, _, _ := copilotServer(t, func(int32) string { return `{"access_token":"x"}` })
	n, err := g.Refresh(context.Background(), Token{Refresh: "stored-gh", Extra: map[string]string{"enterpriseUrl": "ghe.example.com"}})
	if err != nil || n.Refresh != "stored-gh" || n.Extra["enterpriseUrl"] != "ghe.example.com" {
		t.Errorf("refresh %+v %v", n, err)
	}
	if _, err := g.Refresh(context.Background(), Token{}); err == nil {
		t.Error("no GitHub token")
	}
	if _, err := g.Refresh(context.Background(), Token{Refresh: "wrong"}); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("rejected: %v", err)
	}
	// an address that is not https is never shown or opened
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"d","user_code":"u","verification_uri":"javascript:alert(1)","expires_in":9}`))
	}))
	defer bad.Close()
	g2 := NewCopilot(newClock().client())
	g2.DeviceURL = bad.URL
	if _, err := g2.Login(context.Background(), quickUI(t, nil), Options{}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("untrusted address: %v", err)
	}
	// the device code lapses
	g3, _, _ := copilotServer(t, func(int32) string { return `{"error":"authorization_pending"}` })
	if _, err := g3.Login(context.Background(), quickUI(t, nil), Options{}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("lapse: %v", err)
	}
	// a Copilot token answer without a token
	g4, _, _ := copilotServer(t, func(int32) string { return `{"access_token":"gh-token"}` })
	g4.TokenURL = g4.DeviceURL // answers with a device code JSON: no token field
	if _, err := g4.Login(context.Background(), quickUI(t, nil), Options{}); err == nil {
		t.Error("an answer without a Copilot token is an error")
	}
}

func TestCopilotEnterpriseDomains(t *testing.T) {
	for in, want := range map[string]string{"": "", "  ": "", "ghe.example.com": "ghe.example.com", "https://ghe.example.com/x": "ghe.example.com"} {
		if got, err := domainOf(in); err != nil || got != want {
			t.Errorf("domainOf(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := domainOf("http://"); err == nil {
		t.Error("a URL without a host is rejected")
	}
	g := NewCopilot(Client{})
	d, a, tk := g.urls("")
	if d != "https://github.com/login/device/code" || a != "https://github.com/login/oauth/access_token" ||
		tk != "https://api.github.com/copilot_internal/v2/token" {
		t.Errorf("%s %s %s", d, a, tk)
	}
	if _, _, tk := g.urls("ghe.example.com"); tk != "https://api.ghe.example.com/copilot_internal/v2/token" {
		t.Errorf("enterprise token url %s", tk)
	}
	ui := quickUI(t, nil)
	ui.Prompt = func(string, bool) (string, error) { return "http://", nil }
	if _, err := g.Login(context.Background(), ui, Options{}); err == nil || !strings.Contains(err.Error(), "Enterprise") {
		t.Errorf("bad host: %v", err)
	}
	ui.Prompt = func(string, bool) (string, error) { return "", context.Canceled }
	if _, err := g.Login(context.Background(), ui, Options{}); err == nil {
		t.Error("a failing prompt fails the login")
	}
	if g.ID() != "github-copilot" || g.Name() == "" || g.Notice() != "" {
		t.Error("identity")
	}
}

func TestOpenRouterLoginYieldsAnAPIKey(t *testing.T) {
	ts := newTokenServer(t, 200, `{"key":"fake-or-key"}`)
	o := NewOpenRouter(newClock().client())
	o.KeyURL, o.AuthorizeURL = ts.URL+"/keys", "https://openrouter.example/auth"
	var seen url.Values
	ui := quickUI(t, nil)
	ui.OpenBrowser = browser(t, "or-code", false, &seen)
	res, err := o.Login(context.Background(), ui, Options{})
	if err != nil || res.APIKey != "fake-or-key" || res.Token.Access != "" {
		t.Fatalf("%+v %v", res, err)
	}
	r := ts.last(t)
	if r["code"] != "or-code" || challengeOf(r["code_verifier"]) != seen.Get("code_challenge") || !strings.Contains(seen.Get("callback_url"),
		"/oauth/callback/") {
		t.Errorf("exchange %v / %v", r, seen)
	}
	// headless: the page does not load, the user pastes its address
	ui = quickUI(t, nil)
	ui.OpenBrowser = func(string) error { t.Error("opened a browser"); return nil }
	ui.Prompt = func(string, bool) (string, error) { return "http://localhost:3000/oauth/callback/x?code=pasted", nil }
	if _, err := o.Login(context.Background(), ui, Options{NoBrowser: true}); err != nil || ts.last(t)["code"] != "pasted" {
		t.Errorf("paste: %v %v", err, ts.last(t))
	}
	for name, srv := range map[string]*tokenServer{"refused": newTokenServer(t, 403, "no"), "no key": newTokenServer(t, 200, `{}`)} {
		o.KeyURL = srv.URL
		if _, err := o.Login(context.Background(), ui, Options{NoBrowser: true}); err == nil {
			t.Errorf("%s should fail", name)
		}
	}
	if tk, err := o.Refresh(context.Background(), Token{Access: "k"}); err != nil || tk.Access != "k" {
		t.Error("a key is not refreshed")
	}
	if o.ID() != "openrouter" || o.Name() == "" || o.Notice() != "" {
		t.Error("identity")
	}
}

func TestCallbackServerIgnoresStrangers(t *testing.T) {
	cb, err := startCallback([]int{0}, "/cb", "right")
	if err != nil {
		t.Fatal(err)
	}
	defer cb.close()
	base := fmt.Sprintf("http://127.0.0.1:%d/cb", cb.port)
	for _, u := range []string{base + "?code=x&state=wrong", base + "?state=right", base + "?code=x"} {
		resp, err := http.Get(u)
		if err != nil || resp.StatusCode != 400 {
			t.Fatalf("%s: %v %v", u, resp, err)
		}
		_ = resp.Body.Close()
	}
	resp, err := http.Post(base+"?code=x&state=right", "text/plain", nil)
	if err != nil || resp.StatusCode != 400 {
		t.Errorf("POST: %v", err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	select {
	case r := <-cb.result:
		t.Fatalf("a stranger ended the login: %+v", r)
	default:
	}
	if err := getQuiet(base + "?code=real&state=right"); err != nil {
		t.Fatal(err)
	}
	if r := <-cb.result; r.code != "real" {
		t.Errorf("%+v", r)
	}
	var none *callback
	none.close() // closing nothing is fine
	if _, err := startCallback(nil, "/x", ""); err == nil {
		t.Error("no ports")
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string][2]string{
		"":                                     {"", ""},
		"  abc  ":                              {"abc", ""},
		"abc#st":                               {"abc", "st"},
		"http://localhost:1/cb?code=c&state=s": {"c", "s"},
		"code=c&state=s":                       {"c", "s"},
		"?code=c":                              {"c", ""},
	} {
		if c, s := parseInput(in); c != want[0] || s != want[1] {
			t.Errorf("parseInput(%q) = %q, %q", in, c, s)
		}
	}
	v, ch := pkce()
	if len(v) < 43 || challengeOf(v) != ch {
		t.Error("pkce")
	}
	if got := strings.Join(IDs(), ","); got != "anthropic,openai-codex,github-copilot,openrouter" {
		t.Error(got)
	}
	for _, id := range IDs() {
		if f, ok := Lookup(id, Client{}); !ok || f.ID() != id || !Has(id) {
			t.Errorf("lookup %s", id)
		}
	}
	if _, ok := Lookup("groq", Client{}); ok || Has("groq") {
		t.Error("groq has no sign-in")
	}
	if (&StatusError{What: "x", Status: 500}).Error() != "x failed (HTTP 500)" {
		t.Error("StatusError")
	}
	var c Client
	if c.http().Timeout == 0 || c.now().IsZero() {
		t.Error("defaults")
	}
	if err := c.sleep(context.Background(), time.Millisecond); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.sleep(ctx, time.Hour); err == nil {
		t.Error("cancelled sleep")
	}
	if (UI{}).timeout() != 10*time.Minute {
		t.Error("default timeout")
	}
	(UI{}).notify("nothing listens")
	if _, err := c.send(context.Background(), "x", "GET", "http://127.0.0.1:1/", nil, nil); err == nil {
		t.Error("connection refused")
	}
	if _, err := c.send(context.Background(), "x", "GET", "http://bad host/", nil, nil); err == nil {
		t.Error("bad url")
	}
	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(strings.Repeat("x", 500)))
	}))
	defer long.Close()
	if _, err := c.send(context.Background(), "x", "GET", long.URL, nil, nil); err == nil || !strings.HasSuffix(err.Error(), "...") {
		t.Errorf("long body: %v", err)
	}
}
