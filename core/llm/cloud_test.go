package llm

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBedrockRouting(t *testing.T) {
	home := awsHome(t, "", "", nil)
	envWith := func(kv map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := kv[k]; ok {
				return v
			}
			return home(k)
		}
	}
	// a bearer token needs no AWS keys
	r := &Router{Getenv: envWith(map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "fake-bearer", "AWS_REGION": "eu-west-1"})}
	p, id, err := r.Resolve("amazon-bedrock/anthropic.claude-x")
	b, ok := p.(*Bedrock)
	if err != nil || !ok || id != "anthropic.claude-x" || b.Bearer != "fake-bearer" || b.BaseURL != "https://bedrock-runtime.eu-west-1.amazonaws.com" ||
		b.Region != "eu-west-1" {
		t.Fatalf("bearer: %#v %v", p, err)
	}
	if c, err := r.Credential("bedrock"); err != nil || c.Source != "env:AWS_BEARER_TOKEN_BEDROCK" {
		t.Errorf("credential: %+v %v", c, err)
	}
	// AWS keys, the proxy endpoint variable, and Pi's switches
	r = &Router{Getenv: envWith(map[string]string{"AWS_ACCESS_KEY_ID": "AKIDFAKE", "AWS_SECRET_ACCESS_KEY": "fake", "AWS_BEDROCK_SKIP_AUTH": "1",
		"AWS_BEDROCK_FORCE_CACHE": "true", "AWS_ENDPOINT_URL_BEDROCK_RUNTIME": "http://proxy.local:4000/"})}
	b, ok = mustResolve(t, r, "amazon-bedrock/m").(*Bedrock)
	if !ok || b.BaseURL != "http://proxy.local:4000" || !b.SkipAuth || !b.ForceCache || b.Region != DefaultAWSRegion {
		t.Fatalf("keys and switches: %#v", b)
	}
	if c, err := r.Credential("amazon-bedrock"); err != nil || c.Source != "env:AWS_BEARER_TOKEN_BEDROCK" && c.Source != "env:AWS_BEDROCK_SKIP_AUTH" {
		t.Errorf("skip auth credential: %+v %v", c, err)
	}
	// models.json baseUrl beats the variable; a bare Bedrock id is recognised
	r.Custom = map[string]ProviderConfig{"amazon-bedrock": {BaseURL: "http://gateway/bedrock"}}
	if b, _ = mustResolve(t, r, "us.anthropic.claude-sonnet-5-5-v1:0").(*Bedrock); b == nil || b.BaseURL != "http://gateway/bedrock" {
		t.Errorf("bare id / baseUrl: %#v", b)
	}
	// the auth.json entry's env object supplies settings
	r = &Router{Getenv: envWith(nil), Stored: func(string) (string, map[string]string, bool, error) {
		return "", map[string]string{"AWS_ACCESS_KEY_ID": "AKIDFAKE", "AWS_SECRET_ACCESS_KEY": "fake", "AWS_REGION": "ap-southeast-2"}, true, nil
	}}
	if b, _ = mustResolve(t, r, "amazon-bedrock/m").(*Bedrock); b == nil || b.Region != "ap-southeast-2" {
		t.Errorf("stored env: %#v", b)
	} else if c, err := b.Creds(); err != nil || c.AccessKeyID != "AKIDFAKE" {
		t.Errorf("stored creds: %+v %v", c, err)
	}
	// nothing at all: Resolve still works (the error comes with the request), auth check says "not ready"
	r = &Router{Getenv: envWith(nil)}
	b, _ = mustResolve(t, r, "amazon-bedrock/m").(*Bedrock)
	if b == nil {
		t.Fatal("no provider")
	}
	if _, err := b.Creds(); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("creds error: %v", err)
	}
	var ce *CredentialError
	if _, err := r.Credential("amazon-bedrock"); !errors.As(err, &ce) || !ce.Missing || !strings.Contains(err.Error(), "amazon-bedrock: no credentials found") {
		t.Errorf("missing credential: %v", err)
	}
	r = &Router{Getenv: envWith(map[string]string{"AWS_PROFILE": "nope"})}
	if _, err := r.Credential("amazon-bedrock"); !errors.As(err, &ce) || ce.Missing {
		t.Errorf("unusable profile should be invalid, not missing: %v", err)
	}
	r = &Router{Getenv: envWith(map[string]string{"AWS_REGION": "us-east-1/../evil"})}
	if p, _, _ := r.Resolve("amazon-bedrock/m"); !strings.Contains(p.(Missing).Reason, "not an AWS region") {
		t.Errorf("bad region: %v", p)
	}
	// hints for failed requests name the right variables
	he := &HTTPError{Provider: "amazon-bedrock", Status: 403, Body: `{"message":"denied"}`}
	if !strings.Contains(he.Error(), "AWS_BEARER_TOKEN_BEDROCK") || strings.Contains(he.Error(), "koine auth login") {
		t.Errorf("hint: %v", he)
	}
}

// googleKeyFile writes a service account key file with a freshly generated key and returns its path and the public key.
func googleKeyFile(t *testing.T, tokenURI string) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "file-project", "client_email": "sa@file-project.example",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": tokenURI})
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, &key.PublicKey
}

// verifyJWT checks the RS256 signature of a JWT with pub and returns its claims.
func verifyJWT(t *testing.T, jwt string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("bad signature: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if string(hdr) != `{"alg":"RS256","typ":"JWT"}` {
		t.Errorf("header = %s", hdr)
	}
	return claims
}

// tokenServer is a fake OAuth token endpoint. It counts requests and records the last form.
func tokenServer(t *testing.T, status int, reply string) (*httptest.Server, *atomic.Int32, *url.Values) {
	t.Helper()
	var n atomic.Int32
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_ = r.ParseForm()
		form = r.PostForm
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &n, &form
}

func TestGoogleServiceAccountTokenExchange(t *testing.T) {
	tok, calls, form := tokenServer(t, 200, `{"access_token":"fake-access-1","expires_in":3600}`)
	path, pub := googleKeyFile(t, tok.URL)
	now := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	src, err := LoadGoogleCredentials(func(k string) string {
		if k == "GOOGLE_APPLICATION_CREDENTIALS" {
			return path
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	src.Now = func() time.Time { return now }
	if src.Project() != "file-project" {
		t.Errorf("project = %q", src.Project())
	}
	got, err := src.Token(context.Background())
	if err != nil || got != "fake-access-1" {
		t.Fatalf("token %q %v", got, err)
	}
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("form = %v", *form)
	}
	claims := verifyJWT(t, form.Get("assertion"), pub)
	if claims["iss"] != "sa@file-project.example" || claims["scope"] != googleScope || claims["aud"] != tok.URL ||
		int64(claims["iat"].(float64)) != now.Unix() || int64(claims["exp"].(float64)) != now.Unix()+3600 {
		t.Errorf("claims = %v", claims)
	}
	// cached until a minute before expiry, then fetched again
	src.Now = func() time.Time { return now.Add(58 * time.Minute) }
	_, _ = src.Token(context.Background())
	if calls.Load() != 1 {
		t.Errorf("token should be cached, %d calls", calls.Load())
	}
	src.Now = func() time.Time { return now.Add(59*time.Minute + 30*time.Second) }
	_, _ = src.Token(context.Background())
	if calls.Load() != 2 {
		t.Errorf("token should be refreshed near expiry, %d calls", calls.Load())
	}
}

func TestGoogleAuthorizedUserRefresh(t *testing.T) {
	tok, _, form := tokenServer(t, 200, `{"access_token":"fake-access-2"}`)
	dir := t.TempDir()
	doc := `{"type":"authorized_user","client_id":"cid.example","client_secret":"fake-client-secret","refresh_token":"fake-refresh",` +
		`"quota_project_id":"quota-proj","token_uri":"` + tok.URL + `"}`
	if err := os.WriteFile(filepath.Join(dir, "application_default_credentials.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := LoadGoogleCredentials(func(k string) string {
		if k == "CLOUDSDK_CONFIG" {
			return dir
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Token(context.Background())
	if err != nil || got != "fake-access-2" || src.Project() != "quota-proj" {
		t.Fatalf("token %q project %q err %v", got, src.Project(), err)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "fake-refresh" || form.Get("client_id") != "cid.example" ||
		form.Get("client_secret") != "fake-client-secret" {
		t.Errorf("form = %v", *form)
	}
	// the default location is ~/.config/gcloud
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "gcloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoogleCredentials(func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}, nil); err != nil {
		t.Errorf("well-known location: %v", err)
	}
}

func TestGoogleCredentialErrorsNeverLeakSecrets(t *testing.T) {
	write := func(doc string) func(string) string {
		p := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return func(k string) string {
			if k == "GOOGLE_APPLICATION_CREDENTIALS" {
				return p
			}
			return ""
		}
	}
	for name, c := range map[string]struct{ doc, want string }{
		"not json": {`nope`, "not valid JSON"},
		"external": {`{"type":"external_account"}`, `has type "external_account"`},
		"bad pem": {`{"type":"service_account","client_email":"a@b",` +
			`"private_key":"-----BEGIN PRIVATE KEY-----\nQUJD\n-----END PRIVATE KEY-----\n"}`, "private_key"},
		"no pem":      {`{"type":"service_account","client_email":"a@b","private_key":"secret-text"}`, "not a PEM block"},
		"user fields": {`{"type":"authorized_user","client_id":"x"}`, "refresh_token are required"},
		"insecure uri": {`{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z",` +
			`"token_uri":"http://evil.example/token"}`, "must be https"},
		"bad uri": {`{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z","token_uri":"::"}`, "not a URL"},
	} {
		_, err := LoadGoogleCredentials(write(c.doc), nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "secret-text") || strings.Contains(err.Error(), "fake")) {
			t.Errorf("%s: the error leaks file contents: %v", name, err)
		}
	}
	path, _ := googleKeyFile(t, "https://oauth2.example/token")
	raw, _ := os.ReadFile(path)
	var m map[string]string
	_ = json.Unmarshal(raw, &m)
	delete(m, "client_email")
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoogleCredentials(func(string) string { return path }, nil); err == nil || !strings.Contains(err.Error(), "client_email") {
		t.Errorf("missing client_email: %v", err)
	}
	if _, err := LoadGoogleCredentials(func(k string) string {
		if k == "CLOUDSDK_CONFIG" {
			return t.TempDir()
		}
		return ""
	}, nil); !errors.Is(err, ErrNoCredentials) || !strings.Contains(err.Error(), "GOOGLE_CLOUD_API_KEY") {
		t.Errorf("no file: %v", err)
	}
	if _, err := LoadGoogleCredentials(func(string) string { return t.TempDir() }, nil); err == nil || errors.Is(err, ErrNoCredentials) {
		t.Errorf("a directory in place of the file: %v", err)
	}
}

func TestGoogleTokenRefusedAndPKCS1Key(t *testing.T) {
	tok, _, _ := tokenServer(t, 400, `{"error":"invalid_grant","error_description":"Bad Request"}`)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	doc, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "sa@x.example", "token_uri": tok.URL,
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))})
	p := filepath.Join(t.TempDir(), "k.json")
	if err := os.WriteFile(p, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := LoadGoogleCredentials(func(string) string { return p }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = src.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 400): invalid_grant Bad Request") {
		t.Errorf("refused token: %v", err)
	}
	tok.Close()
	if _, err = src.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "token request failed") {
		t.Errorf("unreachable token endpoint: %v", err)
	}
}

// vertexAPI is a fake Vertex endpoint that records the request and answers like generateContent.
func vertexAPI(t *testing.T) (*httptest.Server, *http.Request) {
	t.Helper()
	last := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*last = *r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi from vertex"}]},"finishReason":"STOP"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, last
}

func TestVertexWithApplicationDefaultCredentials(t *testing.T) {
	tok, calls, _ := tokenServer(t, 200, `{"access_token":"fake-vertex-token","expires_in":3600}`)
	keyPath, _ := googleKeyFile(t, tok.URL)
	api, last := vertexAPI(t)
	env := map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": keyPath, "GOOGLE_CLOUD_LOCATION": "europe-west4", "GOOGLE_CLOUD_PROJECT": "env-project"}
	r := &Router{Getenv: func(k string) string { return env[k] }, Custom: map[string]ProviderConfig{"google-vertex": {BaseURL: api.URL + "/v1"}}}
	p, id, err := r.Resolve("vertex/gemini-2.5-flash")
	if err != nil || id != "gemini-2.5-flash" {
		t.Fatalf("%v %v", p, err)
	}
	user := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hello"}}}}
	for i := 0; i < 2; i++ {
		resp, err := Stream(context.Background(), p, Request{Model: id, Messages: user}, nil)
		if err != nil || len(resp.Content) != 1 || resp.Content[0].Text != "hi from vertex" {
			t.Fatalf("response %+v %v", resp, err)
		}
	}
	if want := "/v1/projects/env-project/locations/europe-west4/publishers/google/models/gemini-2.5-flash:streamGenerateContent"; last.URL.Path != want {
		t.Errorf("path = %q", last.URL.Path)
	}
	if last.Header.Get("authorization") != "Bearer fake-vertex-token" || last.Header.Get("x-goog-api-key") != "" || calls.Load() != 1 {
		t.Errorf("auth %q, token calls %d", last.Header.Get("authorization"), calls.Load())
	}
	if c, err := r.Credential("google-vertex"); err != nil || !strings.HasPrefix(c.Source, "adc:") {
		t.Errorf("credential: %+v %v", c, err)
	}
	// the project falls back to the key file's, and "global" uses the global host
	delete(env, "GOOGLE_CLOUD_PROJECT")
	env["GOOGLE_CLOUD_LOCATION"] = "global"
	r.Custom = nil
	g := mustResolve(t, r, "google-vertex/gemini-2.5-pro").(*Gemini)
	if g.BaseURL != "https://aiplatform.googleapis.com/v1" || g.ModelPath != "projects/file-project/locations/global/publishers/google/models/" {
		t.Errorf("global: %s %s", g.BaseURL, g.ModelPath)
	}
	env["GOOGLE_CLOUD_LOCATION"] = "us-central1"
	if g = mustResolve(t, r, "google-vertex/m").(*Gemini); g.BaseURL != "https://us-central1-aiplatform.googleapis.com/v1" {
		t.Errorf("regional: %s", g.BaseURL)
	}
	// a token failure becomes a request error that names the provider and leaks nothing
	tok.Close()
	g = mustResolve(t, r, "google-vertex/m").(*Gemini)
	g.HTTP = &http.Client{Timeout: time.Second}
	_, err = g.Complete(context.Background(), Request{Model: "m", Messages: user})
	if err == nil || !strings.Contains(err.Error(), "google-vertex: token request failed") {
		t.Errorf("token failure: %v", err)
	}
}

func TestVertexAPIKeyAndConfigurationErrors(t *testing.T) {
	api, last := vertexAPI(t)
	r := &Router{Getenv: func(k string) string {
		if k == "GOOGLE_CLOUD_API_KEY" {
			return "fake-vertex-key"
		}
		return ""
	}, Custom: map[string]ProviderConfig{"google-vertex": {BaseURL: api.URL + "/v1"}}}
	p, _, _ := r.Resolve("google-vertex/gemini-2.5-flash")
	one := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "x"}}}}
	if _, err := p.Complete(context.Background(), Request{Model: "gemini-2.5-flash", Messages: one}); err != nil {
		t.Fatal(err)
	}
	if last.URL.Path != "/v1/publishers/google/models/gemini-2.5-flash:generateContent" || last.Header.Get("x-goog-api-key") != "fake-vertex-key" ||
		last.Header.Get("authorization") != "" {
		t.Errorf("express mode: %s %v", last.URL.Path, last.Header)
	}
	if g := mustResolve(t, &Router{Getenv: r.Getenv}, "google-vertex/m").(*Gemini); g.BaseURL != "https://aiplatform.googleapis.com/v1" {
		t.Errorf("express base: %s", g.BaseURL)
	}
	// ADC without a project or location, with unsafe values, and with no credentials at all
	dir := t.TempDir()
	doc := `{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z"}`
	if err := os.WriteFile(filepath.Join(dir, "application_default_credentials.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLOUDSDK_CONFIG": dir}
	rr := &Router{Getenv: func(k string) string { return env[k] }}
	reason := func() string {
		p, _, _ := rr.Resolve("google-vertex/m")
		if m, ok := p.(Missing); ok {
			return m.Reason
		}
		return "resolved"
	}
	if got := reason(); !strings.Contains(got, "set GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION") {
		t.Errorf("no project: %s", got)
	}
	env["GOOGLE_CLOUD_PROJECT"], env["GOOGLE_CLOUD_LOCATION"] = "p", "us-central1.evil.example/"
	if got := reason(); !strings.Contains(got, "not a valid Google Cloud project id or location") {
		t.Errorf("unsafe location: %s", got)
	}
	env["CLOUDSDK_CONFIG"] = t.TempDir()
	if got := reason(); !strings.Contains(got, "google-vertex: no credentials found for Google") {
		t.Errorf("no credentials: %s", got)
	}
	var ce *CredentialError
	if _, err := rr.Credential("google-vertex"); !errors.As(err, &ce) || !ce.Missing {
		t.Errorf("auth check: %v", err)
	}
	if got := (&HTTPError{Provider: "google-vertex", Status: 401}).Hint(); !strings.Contains(got, "gcloud auth application-default login") {
		t.Errorf("hint: %s", got)
	}
}
