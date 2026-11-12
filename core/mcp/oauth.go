package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AuthRequiredError says a server needs the user to sign in. It names the command that does it.
type AuthRequiredError struct{ Server string }

func (e *AuthRequiredError) Error() string {
	return fmt.Sprintf("MCP server %s needs sign-in: run `koine mcp login %s`", e.Server, e.Server)
}

// OAuth is the Authorizer for an HTTP server that uses OAuth 2.1: it sends the stored access token, refreshes it when it has expired, and
// reports AuthRequiredError when only a new sign-in (Login) can help.
type OAuth struct {
	name, url string
	cfg       OAuthConfig
	store     *TokenStore
	hc        *http.Client
	res       Resolver
	mu        sync.Mutex
	now       func() time.Time
}

// NewOAuth returns the authorizer for one server. cfg may be nil.
func NewOAuth(name, serverURL string, cfg *OAuthConfig, store *TokenStore, hc *http.Client, res Resolver) *OAuth {
	o := &OAuth{name: name, url: serverURL, store: store, hc: hc, res: res, now: time.Now}
	if cfg != nil {
		o.cfg = *cfg
	}
	return o
}

// Token returns the stored access token, refreshed if it has expired. It is empty when the server has not been signed in to.
func (o *OAuth) Token(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	rec, ok := o.store.Get(o.name, o.url)
	if !ok {
		return "", nil
	}
	if rec.Expired(o.now()) && rec.RefreshToken != "" {
		if r, err := o.refresh(ctx, rec); err == nil {
			rec = r
		}
	}
	return rec.AccessToken, nil
}

// Challenge handles a 401: it refreshes the token when it can, and otherwise asks for a sign-in.
func (o *OAuth) Challenge(ctx context.Context, _ *http.Response) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	rec, ok := o.store.Get(o.name, o.url)
	if ok && rec.RefreshToken != "" {
		if _, err := o.refresh(ctx, rec); err == nil {
			return nil
		}
	}
	return &AuthRequiredError{Server: o.name}
}

func (o *OAuth) refresh(ctx context.Context, rec TokenRecord) (TokenRecord, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rec.RefreshToken}, "client_id": {rec.ClientID}, "resource": {o.url}}
	if rec.ClientSecret != "" {
		form.Set("client_secret", rec.ClientSecret)
	}
	tok, err := postToken(ctx, o.hc, rec.TokenEndpoint, form)
	if err != nil {
		return rec, err
	}
	rec.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		rec.RefreshToken = tok.RefreshToken
	}
	rec.ExpiresAt = expiry(tok.ExpiresIn, o.now())
	if tok.Scope != "" {
		rec.Scope = tok.Scope
	}
	return rec, o.store.Put(o.name, rec)
}

func expiry(secs int64, now time.Time) time.Time {
	if secs <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(secs) * time.Second)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func postToken(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var tok tokenResponse
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(body, &tok)
	if resp.StatusCode != http.StatusOK || tok.AccessToken == "" {
		msg := tok.Error
		if tok.Description != "" {
			msg += ": " + tok.Description
		}
		if msg == "" {
			msg = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
		return nil, fmt.Errorf("token endpoint: %s", msg)
	}
	return &tok, nil
}

// ---- discovery ----

// ASMetadata is the part of OAuth authorization server metadata (RFC 8414) that sign-in needs.
type ASMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// secureURL accepts https URLs and http only on the loopback interface, which is the rule for every OAuth endpoint we follow.
func secureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("%s must use https (http is allowed only on localhost)", raw)
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func getJSON(ctx context.Context, hc *http.Client, rawURL string, out any) error {
	if err := secureURL(rawURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

var resourceMetaRE = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// wellKnown builds <origin>/.well-known/<suffix><path> (RFC 9728 and 8414 insert the well-known segment before the path).
func wellKnown(base, suffix string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	p := strings.TrimRight(u.Path, "/")
	return u.Scheme + "://" + u.Host + "/.well-known/" + suffix + p
}

// Discover finds the authorization server of an MCP server: the protected resource metadata (RFC 9728, from the 401's resource_metadata
// when the server sends one, else the well-known URL) names it, and its metadata (RFC 8414, else OpenID configuration) gives the
// endpoints. Without resource metadata the authorization server is assumed to live at the server's own origin.
func Discover(ctx context.Context, hc *http.Client, serverURL string, cfg OAuthConfig) (*ASMetadata, string, error) {
	if err := secureURL(serverURL); err != nil {
		return nil, "", err
	}
	if cfg.AuthServerMetadataURL != "" {
		var md ASMetadata
		if err := getJSON(ctx, hc, cfg.AuthServerMetadataURL, &md); err != nil {
			return nil, "", fmt.Errorf("authorization server metadata: %w", err)
		}
		return &md, "", checkMetadata(&md)
	}
	metaURL, scope := probe(ctx, hc, serverURL)
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	candidates := []string{metaURL, wellKnown(serverURL, "oauth-protected-resource")}
	var found bool
	for _, c := range candidates {
		if c != "" && getJSON(ctx, hc, c, &prm) == nil && len(prm.AuthorizationServers) > 0 {
			found = true
			break
		}
	}
	var issuer string
	if found {
		issuer = prm.AuthorizationServers[0]
		if scope == "" {
			scope = strings.Join(prm.ScopesSupported, " ")
		}
	} else {
		u, _ := url.Parse(serverURL)
		issuer = u.Scheme + "://" + u.Host
	}
	var md ASMetadata
	var lastErr error
	for _, c := range []string{wellKnown(issuer, "oauth-authorization-server"), wellKnown(issuer, "openid-configuration"),
		strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"} {
		md = ASMetadata{}
		if lastErr = getJSON(ctx, hc, c, &md); lastErr == nil && md.AuthorizationEndpoint != "" {
			break
		}
	}
	if md.AuthorizationEndpoint == "" {
		return nil, "", fmt.Errorf("no authorization server metadata found for %s: %w", issuer, lastErr)
	}
	return &md, scope, checkMetadata(&md)
}

func checkMetadata(md *ASMetadata) error {
	for _, u := range []string{md.AuthorizationEndpoint, md.TokenEndpoint} {
		if err := secureURL(u); err != nil {
			return err
		}
	}
	if md.RegistrationEndpoint != "" {
		if err := secureURL(md.RegistrationEndpoint); err != nil {
			return err
		}
	}
	if len(md.CodeChallengeMethods) > 0 {
		ok := false
		for _, m := range md.CodeChallengeMethods {
			ok = ok || m == "S256"
		}
		if !ok {
			return errors.New("the authorization server does not support PKCE S256")
		}
	}
	return nil
}

// probe sends an unauthenticated request to the server and returns the resource_metadata URL and scope of its 401 challenge, if any.
func probe(ctx context.Context, hc *http.Client, serverURL string) (metaURL, scope string) {
	body := []byte(`{"jsonrpc":"2.0","id":0,"method":"ping"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(body))
	if err != nil {
		return "", ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return "", ""
	}
	defer func() { _ = resp.Body.Close() }()
	h := resp.Header.Get("WWW-Authenticate")
	if m := resourceMetaRE.FindStringSubmatch(h); m != nil {
		metaURL = m[1]
	}
	if m := regexp.MustCompile(`scope="([^"]+)"`).FindStringSubmatch(h); m != nil {
		scope = m[1]
	}
	return metaURL, scope
}

// ---- PKCE ----

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCEPair returns a code verifier and its S256 challenge (RFC 7636).
func PKCEPair() (verifier, challenge string) {
	verifier = randomString(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// ---- registration ----

func (o *OAuth) registerClient(ctx context.Context, md *ASMetadata, redirect string) (id, secret string, err error) {
	if o.cfg.ClientID != "" {
		secret = o.cfg.ClientSecret
		if secret != "" {
			if secret, err = o.res.Resolve(ctx, secret); err != nil {
				return "", "", fmt.Errorf("oauth.clientSecret: %w", err)
			}
		}
		return o.cfg.ClientID, secret, nil
	}
	switch o.cfg.ClientRegistration {
	case "", "auto", "dcr":
	case "cimd":
		return "", "", errors.New("clientRegistration \"cimd\" (Client ID Metadata Document) is not supported; use dcr or set oauth.clientId")
	default:
		return "", "", fmt.Errorf("unknown oauth.clientRegistration %q", o.cfg.ClientRegistration)
	}
	if md.RegistrationEndpoint == "" {
		return "", "", errors.New("the authorization server offers no dynamic client registration; set oauth.clientId")
	}
	name := o.cfg.ClientName
	if name == "" {
		name = "koine"
	}
	reg, _ := json.Marshal(map[string]any{"client_name": name, "redirect_uris": []string{redirect},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, md.RegistrationEndpoint, bytes.NewReader(reg))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.hc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if resp.StatusCode/100 != 2 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil || out.ClientID == "" {
		return "", "", fmt.Errorf("client registration failed (HTTP %d)", resp.StatusCode)
	}
	return out.ClientID, out.ClientSecret, nil
}

// ---- interactive sign-in ----

// LoginOptions are the interactive parts of Login.
type LoginOptions struct {
	// Open shows the authorization URL to the user, normally by starting their browser. Nil uses the platform's opener.
	Open func(url string) error
	// Notify receives the URL as text as well, for a user whose browser did not open.
	Notify func(msg string)
	// Timeout bounds the wait for the browser (default five minutes).
	Timeout time.Duration
}

// Login runs the authorization code flow with PKCE against the server: discovery, client registration, the browser step on a loopback
// callback, and the token exchange. The tokens are stored for Token to use.
func (o *OAuth) Login(ctx context.Context, opt LoginOptions) error {
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Minute
	}
	if opt.Open == nil {
		opt.Open = OpenBrowser
	}
	md, scope, err := Discover(ctx, o.hc, o.url, o.cfg)
	if err != nil {
		return err
	}
	if o.cfg.Scope != "" {
		scope = o.cfg.Scope
	}
	cb, err := newCallback(o.cfg)
	if err != nil {
		return err
	}
	defer cb.close()
	id, secret, err := o.registerClient(ctx, md, cb.redirect)
	if err != nil {
		return err
	}
	verifier, challenge := PKCEPair()
	state := randomString(16)
	q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {cb.redirect}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "state": {state}, "resource": {o.url}}
	if scope != "" {
		q.Set("scope", scope)
	}
	authURL := md.AuthorizationEndpoint
	if strings.Contains(authURL, "?") {
		authURL += "&" + q.Encode()
	} else {
		authURL += "?" + q.Encode()
	}
	if opt.Notify != nil {
		opt.Notify("Open this URL to sign in to " + o.name + ":\n" + authURL)
	}
	if err := opt.Open(authURL); err != nil && opt.Notify != nil {
		opt.Notify("could not open a browser: " + err.Error())
	}
	wctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	code, err := cb.wait(wctx, state)
	if err != nil {
		return err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {cb.redirect}, "client_id": {id},
		"code_verifier": {verifier}, "resource": {o.url}}
	if secret != "" {
		form.Set("client_secret", secret)
	}
	tok, err := postToken(ctx, o.hc, md.TokenEndpoint, form)
	if err != nil {
		return err
	}
	rec := TokenRecord{ServerURL: o.url, ClientID: id, ClientSecret: secret, TokenEndpoint: md.TokenEndpoint, AccessToken: tok.AccessToken,
		RefreshToken: tok.RefreshToken, TokenType: tok.TokenType, Scope: tok.Scope, ExpiresAt: expiry(tok.ExpiresIn, o.now())}
	return o.store.Put(o.name, rec)
}

// callback is the loopback HTTP server that receives the authorization response.
type callbackResult struct{ code, state, err string }

type callback struct {
	srv      *http.Server
	ln       net.Listener
	redirect string
	result   chan callbackResult
}

func newCallback(cfg OAuthConfig) (*callback, error) {
	host, port, path := "127.0.0.1", cfg.CallbackPort, "/callback"
	if cfg.CallbackURL != "" {
		u, err := url.Parse(cfg.CallbackURL)
		if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) {
			return nil, errors.New("oauth.callbackUrl must be an http URL on localhost, 127.0.0.1 or [::1]")
		}
		host = u.Hostname()
		if u.Port() != "" {
			port, _ = strconv.Atoi(u.Port())
		}
		if u.Path != "" {
			path = u.Path
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("callback listener: %w", err)
	}
	cb := &callback{ln: ln, result: make(chan callbackResult, 1)}
	cb.redirect = "http://" + net.JoinHostPort(host, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)) + path
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
		if res.err != "" && q.Get("error_description") != "" {
			res.err += ": " + q.Get("error_description")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.code == "" && res.err == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "<!doctype html><title>koine</title><p>Signed in. You can close this tab.")
		select {
		case cb.result <- res:
		default:
		}
	})
	cb.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = cb.srv.Serve(ln) }()
	return cb, nil
}

func (c *callback) wait(ctx context.Context, state string) (string, error) {
	select {
	case r := <-c.result:
		if r.err != "" {
			return "", fmt.Errorf("authorization failed: %s", r.err)
		}
		if r.state != state {
			return "", errors.New("authorization response had the wrong state; sign-in aborted")
		}
		return r.code, nil
	case <-ctx.Done():
		return "", fmt.Errorf("sign-in did not finish: %w", ctx.Err())
	}
}

func (c *callback) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.srv.Shutdown(ctx)
}

// OpenBrowser starts the platform's URL opener. Only http(s) URLs are passed on.
func OpenBrowser(target string) error {
	if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
		return errors.New("refusing to open a non-http URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
