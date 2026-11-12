package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"koine/core/agentdir"
	"koine/core/auth"
	"koine/core/auth/oauth"
	"koine/core/llm"
	"koine/core/trust"
)

// ProviderSetup is everything that decides which provider and credential a model uses, outside the model itself: the router, built from
// settings, models.json, auth.json and the environment (REQ-AUTH-001, REQ-LLM-007). `koine auth` uses it without starting an App.
type ProviderSetup struct {
	Router   *llm.Router
	Store    *auth.Store
	Secrets  *auth.Resolver
	Custom   map[string]llm.ProviderConfig
	Warnings []string
	Skipped  int // project provider entries left out because the folder is not trusted

	// OAuth is the client the subscription logins use (tests point it at a fake server). Skew makes a login count as expired this long
	// before it is (`koine auth print-bearer-token --min-expiry`); NoRefresh reports an expired login instead of renewing it.
	OAuth     oauth.Client
	Skew      time.Duration
	NoRefresh bool
}

// newResolver makes the secret resolver of a process: commands from auth.json are cached for its life, those from models.json are reused
// for a few seconds only (auth.Resolver.Burst).
func newResolver() *auth.Resolver { return &auth.Resolver{Burst: 5 * time.Second} }

// NewProviderSetup reads the provider configuration for a working directory. cfg supplies the older `providers` setting (already filtered
// for trust); models.json comes from the agent directory, and from <cwd>/.koine only when trusted: its apiKey and headers may be
// commands, and its baseUrl decides where your key is sent. secrets is shared so that cached commands survive a reload; nil makes one.
func NewProviderSetup(cwd, home string, trusted bool, cfg map[string]llm.ProviderConfig, secrets *auth.Resolver) *ProviderSetup {
	if secrets == nil {
		secrets = newResolver()
	}
	ps := &ProviderSetup{Store: auth.Open(agentdir.Of(home)), Secrets: secrets, Custom: map[string]llm.ProviderConfig{}}
	for k, v := range cfg {
		ps.Custom[k] = v
	}
	read := func(path string) {
		found, warns, err := llm.ReadModelsFile(path)
		if err != nil {
			ps.Warnings = append(ps.Warnings, err.Error())
			return
		}
		ps.Warnings = append(ps.Warnings, warns...)
		for k, v := range found {
			ps.Custom[k] = v // a provider named in models.json replaces the same name from settings, whole
		}
	}
	if dir := agentdir.Of(home); dir != "" {
		read(filepath.Join(dir, "models.json"))
	}
	if proj := filepath.Join(cwd, ".koine", "models.json"); regularFile(proj) {
		if trusted {
			read(proj)
		} else {
			ps.Skipped++
		}
	}
	ps.Router = &llm.Router{Custom: ps.Custom, Stored: ps.stored, StoredOAuth: ps.storedOAuth, Secret: func(v string) (string,
		error) {
		return secrets.Resolve(v, false)
	}}
	return ps
}

func regularFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// stored is the router's view of auth.json: the entry of a provider with its key resolved. An entry of another type (Pi's OAuth logins)
// is reported as unusable; the router falls back to the rest of the precedence list and only mentions it when nothing else works.
func (ps *ProviderSetup) stored(provider string) (string, map[string]string, bool, error) {
	c, ok, err := ps.Store.Get(provider)
	if err != nil || !ok {
		return "", nil, false, err
	}
	if c.Type == auth.TypeOAuth {
		return "", nil, false, nil // storedOAuth handles it
	}
	if c.Type != auth.TypeAPIKey {
		return "", nil, true, fmt.Errorf("auth.json holds an entry of type %q for %s, which Koine does not understand; "+
			"run `koine auth login %s` to replace it", c.Type, provider, provider)
	}
	if c.Key == "" {
		return "", c.Env, true, nil
	}
	key, err := ps.Secrets.Resolve(c.Key, true)
	if err != nil {
		return "", nil, true, fmt.Errorf("the key in auth.json: %w", err)
	}
	return key, c.Env, true, nil
}

// storedOAuth is the router's view of a subscription login in auth.json. An expired access token is renewed with the refresh token and the
// entry rewritten; a login for a provider Koine has no flow for (Pi stores some Koine cannot use) is reported as unusable.
func (ps *ProviderSetup) storedOAuth(provider string) (llm.OAuthCred, bool, error) {
	tok, ok, err := ps.Store.GetOAuth(provider)
	if err != nil || !ok {
		return llm.OAuthCred{}, false, err
	}
	flow, has := oauth.Lookup(provider, ps.OAuth)
	if !has {
		return llm.OAuthCred{}, true, fmt.Errorf("auth.json holds an OAuth login for %s, which Koine cannot use (it can sign in to: %s); "+
			"run `koine auth login %s --api-key` to store a key", provider, strings.Join(oauth.IDs(), ", "), provider)
	}
	now := ps.OAuth.Now
	if now == nil {
		now = time.Now
	}
	if tok.Access == "" {
		return llm.OAuthCred{}, true, fmt.Errorf("the stored %s login has no access token; run `koine auth login %s`", provider, provider)
	}
	if tok.Expired(now(), ps.Skew) {
		if ps.NoRefresh {
			return llm.OAuthCred{}, true, fmt.Errorf("the %s login has expired and --no-refresh was given", provider)
		}
		tok, err = ps.Store.RefreshOAuth(provider, func(t auth.OAuthToken) bool { return t.Expired(now(), ps.Skew) },
			func(t auth.OAuthToken) (auth.OAuthToken, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				return flow.Refresh(ctx, t)
			})
		if err != nil {
			return llm.OAuthCred{}, true, fmt.Errorf("renewing the %s login failed (%w); run `koine auth login %s` to sign in again",
				provider, err, provider)
		}
	}
	return llm.OAuthCred{Access: tok.Access, Extra: tok.Extra}, true, nil
}

// buildCatalog is the built-in catalog, the models of models.json, then the user's model-catalog.json, which therefore has the last word
// on windows and prices.
func buildCatalog(home string, custom map[string]llm.ProviderConfig) *llm.Catalog {
	c := llm.DefaultCatalog()
	c.AddProviders(custom)
	if home != "" {
		_ = c.Merge(filepath.Join(agentdir.Of(home), "model-catalog.json"))
	}
	return c
}

// applyRuntimeKey makes --api-key the credential of the provider it was given for (the first model's), above everything else.
func (a *App) applyRuntimeKey() {
	if a.apiKey != "" && a.apiKeyProv != "" {
		a.router.RuntimeKeys = map[string]string{a.apiKeyProv: a.apiKey}
	}
}

// LoadProviderSetup is NewProviderSetup for a command that has no App: it reads the settings like a session in cwd would, honouring the
// trust of the folder (KOINE_TRUST or the trust store), so `koine auth check` answers what a session there would find.
func LoadProviderSetup(cwd, home string) *ProviderSetup {
	trusted := trust.FromEnv() || trust.Open(home).IsTrusted(cwd)
	user, proj, _ := LoadConfigScoped(cwd, home)
	cfg := user
	if trusted {
		mergeConfig(&cfg, proj)
	}
	return NewProviderSetup(cwd, home, trusted, cfg.Providers, nil)
}

// getenv is the environment the MCP configuration expands against. --api-key no longer pretends to be a variable (see Router.RuntimeKeys).
func (a *App) getenv(k string) string { return os.Getenv(k) }
