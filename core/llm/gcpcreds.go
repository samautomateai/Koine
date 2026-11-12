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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	googleOAuth2Endpoint = "https://oauth2.googleapis.com/token"
	googleScope          = "https://www.googleapis.com/auth/cloud-platform"
)

// GoogleTokenSource turns a Google Application Default Credentials file into short-lived access tokens: a service account key is
// exchanged with a self-signed RS256 JWT (the "JWT bearer" grant), an authorized-user file (from `gcloud auth application-default login`)
// with its refresh token. Tokens are cached until a minute before they expire. The metadata server (Compute Engine, Cloud Run) and
// external-account (workload identity federation) files are not supported.
type GoogleTokenSource struct {
	HTTP *http.Client
	Now  func() time.Time // for tests; time.Now when nil

	kind     string // service_account | authorized_user
	project  string
	tokenURI string
	email    string
	key      *rsa.PrivateKey
	clientID string
	secret   string
	refresh  string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// Project is the project named in the credentials file (a service account's project_id, an authorized user's quota project), or "".
func (g *GoogleTokenSource) Project() string { return g.project }

func (g *GoogleTokenSource) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// googleCredentialsPath is GOOGLE_APPLICATION_CREDENTIALS, else gcloud's well-known application_default_credentials.json.
func googleCredentialsPath(get func(string) string) string {
	if p := get("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p
	}
	dir := get("CLOUDSDK_CONFIG")
	if dir == "" {
		home := get("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		dir = filepath.Join(home, ".config", "gcloud")
		if app := get("APPDATA"); app != "" && get("HOME") == "" {
			dir = filepath.Join(app, "gcloud")
		}
	}
	return filepath.Join(dir, "application_default_credentials.json")
}

// LoadGoogleCredentials reads the ADC file get points at. The errors name the file and what is wrong with it, never its contents.
func LoadGoogleCredentials(get func(string) string, client *http.Client) (*GoogleTokenSource, error) {
	path := googleCredentialsPath(get)
	b, err := os.ReadFile(path) //nolint:gosec // the user's own credentials file, named by Google's standard variable
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w for Google: set GOOGLE_CLOUD_API_KEY, or GOOGLE_APPLICATION_CREDENTIALS to a service account key file, "+
			"or run `gcloud auth application-default login`", ErrNoCredentials)
	}
	if err != nil {
		return nil, fmt.Errorf("credentials file %s: %w", path, err)
	}
	var f struct {
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		QuotaProject string `json:"quota_project_id"`
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		TokenURI     string `json:"token_uri"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("credentials file %s is not valid JSON", path)
	}
	g := &GoogleTokenSource{HTTP: client, kind: f.Type, project: f.ProjectID, tokenURI: f.TokenURI}
	if g.tokenURI == "" {
		g.tokenURI = googleOAuth2Endpoint
	}
	if err := checkTokenURI(g.tokenURI); err != nil {
		return nil, fmt.Errorf("credentials file %s: %w", path, err)
	}
	switch f.Type {
	case "service_account":
		key, err := parseRSAKey(f.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("credentials file %s: private_key: %w", path, err)
		}
		if f.ClientEmail == "" {
			return nil, fmt.Errorf("credentials file %s: client_email is missing", path)
		}
		g.key, g.email = key, f.ClientEmail
	case "authorized_user":
		if f.ClientID == "" || f.ClientSecret == "" || f.RefreshToken == "" {
			return nil, fmt.Errorf("credentials file %s: client_id, client_secret and refresh_token are required", path)
		}
		g.clientID, g.secret, g.refresh = f.ClientID, f.ClientSecret, f.RefreshToken
		if g.project == "" {
			g.project = f.QuotaProject
		}
	default:
		return nil, fmt.Errorf("credentials file %s has type %q; Koine supports service_account and authorized_user", path, f.Type)
	}
	return g, nil
}

// checkTokenURI accepts https, and plain http only to the local machine (tests and emulators); the key file decides where a signed
// assertion is sent.
func checkTokenURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("token_uri is not a URL")
	}
	host := u.Hostname()
	if u.Scheme == "https" || (u.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1")) {
		return nil
	}
	return errors.New("token_uri must be https")
}

func parseRSAKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("not a PEM block")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("not an RSA key")
	}
	k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not a PKCS#8 or PKCS#1 RSA key")
	}
	return k, nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// assertion is the signed JWT of the service account flow (RFC 7523): RS256 over base64url(header).base64url(claims).
func (g *GoogleTokenSource) assertion(now time.Time) (string, error) {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": g.email, "scope": googleScope, "aud": g.tokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	signing := b64url(hdr) + "." + b64url(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, g.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + b64url(sig), nil
}

// Token returns a valid access token, fetching a new one when the cached token is within a minute of expiring.
func (g *GoogleTokenSource) Token(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if g.token != "" && now.Add(time.Minute).Before(g.expiry) {
		return g.token, nil
	}
	form := url.Values{}
	if g.kind == "service_account" {
		jwt, err := g.assertion(now)
		if err != nil {
			return "", fmt.Errorf("service account: cannot sign the token request: %w", err)
		}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
		form.Set("assertion", jwt)
	} else {
		form.Set("grant_type", "refresh_token")
		form.Set("client_id", g.clientID)
		form.Set("client_secret", g.secret)
		form.Set("refresh_token", g.refresh)
	}
	hr, err := http.NewRequestWithContext(ctx, "POST", g.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	hr.Header.Set("content-type", "application/x-www-form-urlencoded")
	client := g.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(hr)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != 200 || out.AccessToken == "" {
		return "", fmt.Errorf("token request was refused (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(out.Error+" "+out.Description))
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 3600
	}
	g.token, g.expiry = out.AccessToken, now.Add(time.Duration(out.ExpiresIn)*time.Second)
	return g.token, nil
}
