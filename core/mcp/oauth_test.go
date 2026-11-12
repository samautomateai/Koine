package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"koine/core/mcp/mcptest"
)

// authFixture is one server that plays the MCP resource server and the OAuth authorization server, so the whole protocol runs on loopback.
// The browser step is played by Open, which requests the authorization URL the way a browser would and follows the redirect.
type authFixture struct {
	srv *httptest.Server

	mu         sync.Mutex
	valid      map[string]bool   // accepted access tokens
	codes      map[string]string // code -> PKCE challenge
	registered int
	wrongState bool
	denyAuth   string
	noPRM      bool
	metaMut    func(map[string]any)
	tokenErr   bool
	authParams url.Values
	refreshed  int
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{valid: map[string]bool{"tok1": true}, codes: map[string]string{}}
	mcpH := mcptest.New().HTTPHandler(mcptest.HTTPOptions{})
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.valid[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource/mcp", scope="read"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpH.ServeHTTP(w, r)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		if f.noPRM {
			http.NotFound(w, nil)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.srv.URL + "/mcp", "authorization_servers": []string{f.srv.URL + "/as"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/as", func(w http.ResponseWriter, _ *http.Request) {
		f.writeMetadata(w, "/as")
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) { f.writeMetadata(w, "") })
	mux.HandleFunc("/as/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.registered++
		f.mu.Unlock()
		if body["client_name"] != "koine" || body["token_endpoint_auth_method"] != "none" {
			http.Error(w, "bad registration", 400)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "cid"})
	})
	mux.HandleFunc("/as/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.authParams = q
		f.mu.Unlock()
		if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
			http.Error(w, "bad request", 400)
			return
		}
		cb, _ := url.Parse(q.Get("redirect_uri"))
		out := cb.Query()
		if f.denyAuth != "" {
			out.Set("error", "access_denied")
			out.Set("error_description", f.denyAuth)
		} else {
			code := "code-" + q.Get("state")[:4]
			f.mu.Lock()
			f.codes[code] = q.Get("code_challenge")
			f.mu.Unlock()
			out.Set("code", code)
		}
		out.Set("state", q.Get("state"))
		if f.wrongState {
			out.Set("state", "forged")
		}
		cb.RawQuery = out.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
	})
	mux.HandleFunc("/as/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.tokenErr {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "nope"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if f.codes[r.Form.Get("code")] != base64.RawURLEncoding.EncodeToString(sum[:]) || r.Form.Get("resource") != f.srv.URL+"/mcp" {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "PKCE or resource mismatch"})
				return
			}
			if r.Form.Get("client_secret") != "" {
				f.authParams.Set("seen_secret", r.Form.Get("client_secret"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok1", "token_type": "Bearer", "refresh_token": "r1",
				"expires_in": 3600, "scope": "read"})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "r1" && r.Form.Get("refresh_token") != "r2" {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			f.refreshed++
			f.valid["tok2"] = true
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok2", "refresh_token": "r2", "expires_in": 60})
		default:
			w.WriteHeader(400)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *authFixture) writeMetadata(w http.ResponseWriter, prefix string) {
	md := map[string]any{"issuer": f.srv.URL + prefix, "authorization_endpoint": f.srv.URL + "/as/authorize", "token_endpoint": f.srv.URL + "/as/token",
		"registration_endpoint": f.srv.URL + "/as/register", "code_challenge_methods_supported": []string{"S256"}}
	if f.metaMut != nil {
		f.metaMut(md)
	}
	_ = json.NewEncoder(w).Encode(md)
}

func (f *authFixture) url() string { return f.srv.URL + "/mcp" }

// browser plays the user: it requests the authorization URL and follows the redirect to the loopback callback.
func (f *authFixture) browser(url string) error {
	go func() {
		resp, err := http.Get(url) //nolint:gosec,noctx // a fixed test URL
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	return nil
}

func newOAuth(f *authFixture, store *TokenStore, cfg *OAuthConfig) *OAuth {
	return NewOAuth("remote", f.url(), cfg, store, &http.Client{Timeout: 10 * time.Second}, Resolver{})
}

func newStore(t *testing.T) *TokenStore {
	return &TokenStore{Path: filepath.Join(t.TempDir(), "agent", "mcp-auth.json")}
}

func TestOAuthLoginDiscoversRegistersAndStoresTokens(t *testing.T) {
	f := newAuthFixture(t)
	store := newStore(t)
	o := newOAuth(f, store, nil)
	var notes []string
	opts := LoginOptions{Open: f.browser, Notify: func(s string) { notes = append(notes, s) }, Timeout: 10 * time.Second}
	if err := o.Login(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	rec, ok := store.Get("remote", f.url())
	if !ok || rec.AccessToken != "tok1" || rec.RefreshToken != "r1" || rec.ClientID != "cid" || rec.TokenEndpoint != f.srv.URL+"/as/token" ||
		rec.ExpiresAt.IsZero() {
		t.Fatalf("record = %+v", rec)
	}
	if f.registered != 1 {
		t.Fatalf("registered %d times", f.registered)
	}
	q := f.authParams
	if q.Get("client_id") != "cid" || q.Get("resource") != f.url() || q.Get("scope") != "read" ||
		!strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") ||
		!strings.HasSuffix(q.Get("redirect_uri"), "/callback") {
		t.Fatalf("authorize params = %v", q)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "/as/authorize?") {
		t.Fatalf("notes = %q", notes)
	}
	if info, err := os.Stat(store.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", info, err)
	}
	if info, _ := os.Stat(filepath.Dir(store.Path)); info.Mode().Perm() != 0o700 {
		t.Fatalf("token dir mode = %v", info.Mode().Perm())
	}
	// the token is used on the connection
	c := NewClient(NewHTTP(HTTPConfig{URL: f.url(), Auth: o, RetryDelay: time.Millisecond}))
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	// the record belongs to the URL: the same name pointing elsewhere gets no token
	if _, ok := store.Get("remote", "https://elsewhere.example/mcp"); ok {
		t.Fatal("token must not follow the name to another URL")
	}
	// logout
	if gone, err := store.Delete("remote"); !gone || err != nil {
		t.Fatalf("delete = %v %v", gone, err)
	}
	if gone, _ := store.Delete("remote"); gone {
		t.Fatal("second delete")
	}
}

func TestOAuthRefreshAndChallenge(t *testing.T) {
	f := newAuthFixture(t)
	store := newStore(t)
	o := newOAuth(f, store, nil)
	if err := o.Login(context.Background(), LoginOptions{Open: f.browser, Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	// 1. an expired token is refreshed before the request
	rec, _ := store.Get("remote", f.url())
	rec.ExpiresAt = time.Now().Add(-time.Hour)
	_ = store.Put("remote", rec)
	tok, err := o.Token(context.Background())
	if err != nil || tok != "tok2" || f.refreshed != 1 {
		t.Fatalf("token = %q, %v (refreshed %d)", tok, err, f.refreshed)
	}
	if rec, _ = store.Get("remote", f.url()); rec.RefreshToken != "r2" {
		t.Fatalf("rotated refresh token not stored: %+v", rec)
	}
	// 2. the server revokes the access token: the 401 triggers a refresh and the request is repeated
	f.mu.Lock()
	f.valid = map[string]bool{}
	f.mu.Unlock()
	rec.AccessToken, rec.ExpiresAt = "revoked", time.Now().Add(time.Hour)
	_ = store.Put("remote", rec)
	c := NewClient(NewHTTP(HTTPConfig{URL: f.url(), Auth: o, RetryDelay: time.Millisecond}))
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 3. a refresh token the server rejects means a new sign-in is needed
	rec.RefreshToken, rec.AccessToken = "bad", "revoked"
	_ = store.Put("remote", rec)
	err = NewClient(NewHTTP(HTTPConfig{URL: f.url(), Auth: o, RetryDelay: time.Millisecond})).Initialize(context.Background())
	var ar *AuthRequiredError
	if !errors.As(err, &ar) || ar.Server != "remote" {
		t.Fatalf("err = %v", err)
	}
	// 4. not signed in at all
	empty := newOAuth(f, newStore(t), nil)
	if tok, err := empty.Token(context.Background()); tok != "" || err != nil {
		t.Fatalf("token = %q %v", tok, err)
	}
	if err := empty.Challenge(context.Background(), nil); !errors.As(err, &ar) {
		t.Fatalf("challenge = %v", err)
	}
}

func TestOAuthManagerReportsNeedsLogin(t *testing.T) {
	f := newAuthFixture(t)
	m, _ := newManager(t)
	m.Store = newStore(t)
	startAll(t, m, Entry{Name: "remote", Config: ServerConfig{URL: f.url()}})
	st := m.Statuses()[0]
	if st.State != StateNeedsLogin || !strings.Contains(st.Err, "koine mcp login remote") {
		t.Fatalf("status = %+v", st)
	}
	// after signing in, Reconnect works and the tools appear
	signIn := NewOAuth("remote", f.url(), nil, m.Store, m.httpClient(), Resolver{})
	if err := signIn.Login(context.Background(), LoginOptions{Open: f.browser, Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconnect(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	if m.Statuses()[0].State != StateConnected {
		t.Fatalf("status = %+v", m.Statuses()[0])
	}
	// an Authorization header in the config switches OAuth off
	m2, _ := newManager(t)
	m2.Store = m.Store
	startAll(t, m2, Entry{Name: "remote", Config: ServerConfig{URL: f.url(), Headers: map[string]string{"authorization": "Bearer nope"}}})
	if st := m2.Statuses()[0]; st.State != StateFailed || !strings.Contains(st.Err, "401") {
		t.Fatalf("status = %+v", st)
	}
}

func TestOAuthConfiguredClientAndCallbackPort(t *testing.T) {
	f := newAuthFixture(t)
	f.metaMut = func(md map[string]any) { delete(md, "registration_endpoint") }
	t.Setenv("KOINE_TEST_SECRET", "s3cret")
	port := freePort(t)
	o := NewOAuth("remote", f.url(), &OAuthConfig{ClientID: "mine", ClientSecret: "${KOINE_TEST_SECRET}", CallbackPort: port, Scope: "custom"}, newStore(t),
		&http.Client{Timeout: 10 * time.Second}, Resolver{})
	if err := o.Login(context.Background(), LoginOptions{Open: f.browser, Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	q := f.authParams
	if q.Get("client_id") != "mine" || q.Get("scope") != "custom" || q.Get("seen_secret") != "s3cret" ||
		!strings.Contains(q.Get("redirect_uri"), ":"+strconv.Itoa(port)+"/callback") {
		t.Fatalf("params = %v", q)
	}
	rec, _ := o.store.Get("remote", f.url())
	if rec.ClientID != "mine" || rec.ClientSecret != "s3cret" {
		t.Fatalf("record = %+v", rec)
	}
	// callbackUrl on loopback is honoured; one elsewhere is refused
	o2 := NewOAuth("remote", f.url(), &OAuthConfig{ClientID: "mine", CallbackURL: "http://localhost:" + strconv.Itoa(freePort(t)) + "/cb"}, newStore(t),
		&http.Client{Timeout: 10 * time.Second}, Resolver{})
	if err := o2.Login(context.Background(), LoginOptions{Open: f.browser, Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.authParams.Get("redirect_uri"), "/cb") {
		t.Fatalf("redirect = %s", f.authParams.Get("redirect_uri"))
	}
	for _, bad := range []string{"https://localhost:1/cb", "http://example.com/cb", "::"} {
		if _, err := newCallback(OAuthConfig{CallbackURL: bad}); err == nil {
			t.Errorf("callback url %q should be refused", bad)
		}
	}
	if _, err := newCallback(OAuthConfig{CallbackPort: port, CallbackURL: ""}); err != nil { // the port is free again after the login
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	cb, err := newCallback(OAuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer cb.close()
	u, _ := url.Parse(cb.redirect)
	n, _ := strconv.Atoi(u.Port())
	return n
}

func TestOAuthLoginFailures(t *testing.T) {
	login := func(f *authFixture, cfg *OAuthConfig, open func(string) error, timeout time.Duration) error {
		if open == nil {
			open = f.browser
		}
		return newOAuth(f, newStore(t), cfg).Login(context.Background(), LoginOptions{Open: open, Timeout: timeout})
	}
	check := func(name string, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, want)
		}
	}
	f := newAuthFixture(t)
	f.wrongState = true
	check("wrong state", login(f, nil, nil, 5*time.Second), "wrong state")
	f = newAuthFixture(t)
	f.denyAuth = "user said no"
	check("denied", login(f, nil, nil, 5*time.Second), "access_denied: user said no")
	f = newAuthFixture(t)
	check("browser never returns", login(f, nil, func(string) error { return errors.New("no display") }, 100*time.Millisecond), "did not finish")
	f = newAuthFixture(t)
	f.tokenErr = true
	check("token endpoint error", login(f, nil, nil, 5*time.Second), "invalid_grant: nope")
	f = newAuthFixture(t)
	f.metaMut = func(md map[string]any) { delete(md, "registration_endpoint") }
	check("no registration", login(f, nil, nil, time.Second), "set oauth.clientId")
	f = newAuthFixture(t)
	check("cimd", login(f, &OAuthConfig{ClientRegistration: "cimd"}, nil, time.Second), "not supported")
	check("unknown registration", login(f, &OAuthConfig{ClientRegistration: "magic"}, nil, time.Second), "unknown oauth.clientRegistration")
	check("bad secret variable", login(f, &OAuthConfig{ClientID: "x", ClientSecret: "${KOINE_UNSET_FOR_TEST}"}, nil, time.Second), "clientSecret")
	check("bad callback", login(f, &OAuthConfig{CallbackURL: "http://example.com/cb"}, nil, time.Second), "localhost")
	f = newAuthFixture(t)
	f.metaMut = func(md map[string]any) { md["authorization_endpoint"] = "http://evil.example/authorize" }
	check("insecure endpoint", login(f, nil, nil, time.Second), "must use https")
	f = newAuthFixture(t)
	f.metaMut = func(md map[string]any) { md["code_challenge_methods_supported"] = []string{"plain"} }
	check("no S256", login(f, nil, nil, time.Second), "PKCE S256")
	f = newAuthFixture(t)
	f.metaMut = func(md map[string]any) { md["registration_endpoint"] = "http://evil.example/register" }
	check("insecure registration endpoint", login(f, nil, nil, time.Second), "must use https")
	f = newAuthFixture(t)
	f.metaMut = func(md map[string]any) { md["registration_endpoint"] = f.srv.URL + "/as/nothing" }
	check("registration rejected", login(f, nil, nil, time.Second), "registration failed")
}

func TestOAuthDiscoveryFallbacks(t *testing.T) {
	ctx := context.Background()
	hc := &http.Client{Timeout: 5 * time.Second}
	// no protected resource metadata: the authorization server is the server's own origin
	f := newAuthFixture(t)
	f.noPRM = true
	md, _, err := Discover(ctx, hc, f.url(), OAuthConfig{})
	if err != nil || md.AuthorizationEndpoint != f.srv.URL+"/as/authorize" {
		t.Fatalf("md = %+v, %v", md, err)
	}
	// an explicit metadata URL is used as given
	md, _, err = Discover(ctx, hc, f.url(), OAuthConfig{AuthServerMetadataURL: f.srv.URL + "/.well-known/oauth-authorization-server"})
	if err != nil || md.TokenEndpoint == "" {
		t.Fatalf("md = %+v, %v", md, err)
	}
	if _, _, err = Discover(ctx, hc, f.url(), OAuthConfig{AuthServerMetadataURL: "http://evil.example/md"}); err == nil {
		t.Fatal("insecure metadata URL")
	}
	if _, _, err = Discover(ctx, hc, f.url(), OAuthConfig{AuthServerMetadataURL: f.srv.URL + "/missing"}); err == nil {
		t.Fatal("missing metadata")
	}
	if _, _, err = Discover(ctx, hc, "http://evil.example/mcp", OAuthConfig{}); err == nil {
		t.Fatal("insecure server URL")
	}
	// OpenID configuration is the fallback to RFC 8414
	oidc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": "http://127.0.0.1/a", "token_endpoint": "http://127.0.0.1/t"})
	}))
	defer oidc.Close()
	if md, _, err = Discover(ctx, hc, oidc.URL+"/mcp", OAuthConfig{}); err != nil || md.TokenEndpoint != "http://127.0.0.1/t" {
		t.Fatalf("oidc md = %+v, %v", md, err)
	}
	none := httptest.NewServer(http.NotFoundHandler())
	defer none.Close()
	if _, _, err = Discover(ctx, hc, none.URL+"/mcp", OAuthConfig{}); err == nil || !strings.Contains(err.Error(), "no authorization server metadata") {
		t.Fatalf("err = %v", err)
	}
	if wellKnown("https://h.example/a/b/", "x") != "https://h.example/.well-known/x/a/b" || wellKnown("::", "x") != "" {
		t.Fatal("wellKnown")
	}
}

func TestPKCEPair(t *testing.T) {
	v, c := PKCEPair()
	sum := sha256.Sum256([]byte(v))
	if len(v) < 43 || c != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatalf("verifier %q challenge %q", v, c)
	}
	if v2, _ := PKCEPair(); v2 == v {
		t.Fatal("verifiers must be random")
	}
	// RFC 7636 appendix B
	sum = sha256.Sum256([]byte("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("S256 test vector")
	}
}

func TestTokenStoreFile(t *testing.T) {
	s := newStore(t)
	if _, ok := s.Get("a", "u"); ok {
		t.Fatal("empty store")
	}
	if gone, err := s.Delete("a"); gone || err != nil {
		t.Fatalf("delete from empty = %v %v", gone, err)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a", "u"); ok {
		t.Fatal("corrupt store reads as empty")
	}
	if _, err := s.Delete("a"); err == nil {
		t.Fatal("delete on a corrupt file should say so")
	}
	if err := s.Put("a", TokenRecord{ServerURL: "u", AccessToken: "t"}); err != nil { // a corrupt file is replaced
		t.Fatal(err)
	}
	if r, ok := s.Get("a", "u"); !ok || r.AccessToken != "t" {
		t.Fatalf("record = %+v", r)
	}
	if !(TokenRecord{ExpiresAt: time.Now().Add(30 * time.Second)}).Expired(time.Now()) || (TokenRecord{}).Expired(time.Now()) {
		t.Fatal("Expired")
	}
	if AuthFile("/h") != filepath.Join("/h", ".koine", "mcp-auth.json") && os.Getenv("KOINE_AGENT_DIR") == "" {
		t.Fatalf("AuthFile = %s", AuthFile("/h"))
	}
	if err := OpenBrowser("file:///etc/passwd"); err == nil {
		t.Fatal("OpenBrowser must refuse non-http URLs")
	}
}
