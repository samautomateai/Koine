package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"sort"
	"strings"
)

// ProviderConfig is one provider of models.json (Pi's schema, https://pi.dev/docs/latest/models-configuration) or of the older
// `providers` setting. A key that names a built-in provider changes it (a bare baseUrl routes it through a proxy); any other key adds a
// provider, which needs a baseUrl and, unless it speaks Chat Completions, an api.
type ProviderConfig struct {
	Type           string                     `json:"type,omitempty"` // Koine's older spelling of api: "openai" (default) | "anthropic"
	Name           string                     `json:"name,omitempty"`
	API            string                     `json:"api,omitempty"`
	BaseURL        string                     `json:"baseUrl,omitempty"`
	APIKey         string                     `json:"apiKey,omitempty"`    // literal, "$VAR" template or "!command" (auth.Resolver)
	APIKeyEnv      string                     `json:"apiKeyEnv,omitempty"` // Koine's older spelling: the name of the variable that holds the key
	Headers        map[string]string          `json:"headers,omitempty"`
	AuthHeader     bool                       `json:"authHeader,omitempty"` // send the key as Authorization: Bearer
	Compat         map[string]json.RawMessage `json:"compat,omitempty"`
	Models         []ModelConfig              `json:"models,omitempty"`
	ModelOverrides map[string]ModelConfig     `json:"modelOverrides,omitempty"`
}

// Router picks a provider from the model id, like pi-go: a known prefix ("openrouter/anthropic/claude-3.5"), a recognisable bare name
// ("claude-…", "gpt-…", "gemini-…"), or a provider from config. Unknown prefixes are an error; it never guesses.
//
// A provider's API key comes from, in order: RuntimeKeys (--api-key), Stored (auth.json), the provider's apiKey in Custom (models.json),
// then its environment variables.
type Router struct {
	Custom map[string]ProviderConfig
	HTTP   *http.Client
	Retry  RetryPolicy         // applied to every provider the router builds
	Getenv func(string) string // defaults to os.Getenv

	RuntimeKeys map[string]string // provider id -> key given for this run only
	// Stored returns the auth.json entry of a provider, with its key already resolved. found is false when there is none; env holds the
	// entry's `env` object, which wins over the process environment for this provider. An error means the entry is unusable.
	Stored func(provider string) (key string, env map[string]string, found bool, err error)
	// Secret resolves a value from models.json (apiKey, headers); nil uses values as written.
	Secret func(value string) (string, error)
}

// Credential says which API key a provider will use and where it came from.
type Credential struct {
	Key    string
	Source string            // "--api-key", "auth.json", "models.json", "env:NAME" or "" (none needed)
	Bearer bool              // the key is a bearer token (ANTHROPIC_AUTH_TOKEN), not an API key
	Env    map[string]string // the auth.json entry's env object
}

// CredentialError explains why a provider has no usable credential. Missing means none was found anywhere; otherwise one was found
// and could not be used (a command failed, a variable is unset, the entry is an OAuth login).
type CredentialError struct {
	Provider string
	Missing  bool
	Reason   string
}

func (e *CredentialError) Error() string { return e.Reason }

func (r *Router) env(k string) string {
	if r.Getenv != nil {
		return r.Getenv(k)
	}
	return os.Getenv(k)
}

// envFor reads a provider setting: the auth.json entry's env object first, then the process environment.
func (r *Router) envFor(entry map[string]string, k string) string {
	if v := entry[k]; v != "" {
		return v
	}
	return r.env(k)
}

func (r *Router) secret(v string) (string, error) {
	if r.Secret == nil {
		return v, nil
	}
	return r.Secret(v)
}

// split separates "provider/model", inferring the provider of a bare id. The provider is a custom one if the config has it, else a
// built-in (by id or alias).
func (r *Router) split(model string) (provider, id string, err error) {
	prefix, rest, hasSlash := strings.Cut(model, "/")
	if !hasSlash {
		if prefix = inferProvider(model); prefix == "" {
			return "", "", fmt.Errorf("cannot infer a provider for model %q: use a prefix (e.g. openrouter/%s, ollama/%s) "+
				"or declare a provider in config; known: %s", model, model, model, r.known())
		}
		return prefix, model, nil
	}
	if _, ok := r.Custom[prefix]; ok {
		return prefix, rest, nil
	}
	if _, ok := BuiltinProvider(prefix); ok {
		return CanonicalProvider(prefix), rest, nil
	}
	return "", "", fmt.Errorf("unknown provider prefix %q in model %q; known: %s", prefix, model, r.known())
}

// ProviderOf returns the provider id and the model id that a model string names.
func (r *Router) ProviderOf(model string) (provider, id string, err error) { return r.split(model) }

func (r *Router) known() string {
	set := map[string]bool{}
	for _, p := range providerTable {
		set[p.ID] = true
	}
	for k := range r.Custom {
		set[k] = true
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// setup is what the router knows about one provider (and model) before the credential is looked up.
type setup struct {
	name        string
	api         string
	base        string
	headers     map[string]string
	authHeader  bool
	compat      map[string]json.RawMessage
	sampling    map[string]any
	keyEnv      []string
	keyOptional bool
	bearerEnv   string // ANTHROPIC_AUTH_TOKEN for Anthropic
	custom      *ProviderConfig
}

// lookup merges the built-in provider (if any) with its config. modelID may be "" when only the credential is wanted.
func (r *Router) lookup(name, modelID string) (setup, error) {
	info, builtin := BuiltinProvider(name)
	pc, custom := r.Custom[name]
	if !builtin && !custom {
		return setup{}, fmt.Errorf("unknown provider %q; known: %s", name, r.known())
	}
	s := setup{name: name, api: info.API, base: info.BaseURL, keyEnv: info.Env, keyOptional: info.Local || info.Ambient}
	if builtin {
		s.name = info.ID
	}
	if name == "anthropic" {
		s.bearerEnv = "ANTHROPIC_AUTH_TOKEN"
		if v := r.env("ANTHROPIC_BASE_URL"); v != "" {
			s.base = v
		}
	}
	if name == "openai" {
		if v := r.env("OPENAI_BASE_URL"); v != "" {
			s.base = v
		}
	}
	if !custom {
		return s, nil
	}
	s.custom = &pc
	switch {
	case pc.API != "":
		s.api = pc.API
	case pc.Type == "anthropic":
		s.api = APIAnthropic
	case !builtin:
		s.api = APICompletions
	}
	if pc.BaseURL != "" {
		s.base = pc.BaseURL
	}
	if pc.APIKeyEnv != "" {
		s.keyEnv = []string{pc.APIKeyEnv}
	}
	if !builtin && pc.APIKeyEnv == "" {
		s.keyOptional = true // a custom endpoint declared without a key source may not need one (a local gateway)
	}
	s.headers, s.compat, s.authHeader = pc.Headers, pc.Compat, pc.AuthHeader
	for i := range pc.Models {
		m := &pc.Models[i]
		if m.ID != modelID || modelID == "" {
			continue
		}
		if m.API != "" {
			s.api = m.API
		}
		if m.BaseURL != "" {
			s.base = m.BaseURL
		}
		s.headers = mergeStrings(s.headers, m.Headers)
		s.compat = mergeRaw(s.compat, m.Compat)
		s.sampling = m.SamplingParams
	}
	return s, nil
}

func mergeStrings(a, b map[string]string) map[string]string {
	if len(b) == 0 {
		return a
	}
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func mergeRaw(a, b map[string]json.RawMessage) map[string]json.RawMessage {
	if len(b) == 0 {
		return a
	}
	out := map[string]json.RawMessage{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// isOpenAIHost reports whether a provider is OpenAI itself (the built-in "openai" at api.openai.com), the only Chat Completions or
// Responses server known to accept prompt_cache_key and prompt_cache_retention.
func isOpenAIHost(name, base string) bool {
	u, err := neturl.Parse(base)
	return name == "openai" && err == nil && u.Hostname() == "api.openai.com"
}

func loginHint(provider string) string {
	return fmt.Sprintf("run `koine auth login %s` (or /login %s) to store a key", provider, provider)
}

// credential finds the API key of a provider: --api-key, auth.json, models.json, then the environment.
func (r *Router) credential(s setup) (Credential, error) {
	fail := func(missing bool, format string, a ...any) (Credential, error) {
		return Credential{}, &CredentialError{Provider: s.name, Missing: missing, Reason: fmt.Sprintf(format, a...)}
	}
	if k := r.RuntimeKeys[s.name]; k != "" {
		return Credential{Key: k, Source: "--api-key"}, nil
	}
	var entryEnv map[string]string
	var storedErr error // an unusable auth.json entry only matters when nothing further down the list works
	if r.Stored != nil {
		key, env, found, err := r.Stored(s.name)
		storedErr = err
		if found && err == nil {
			entryEnv = env
			if key != "" {
				return Credential{Key: key, Source: "auth.json", Env: env}, nil
			}
		}
	}
	if s.custom != nil && s.custom.APIKey != "" {
		key, err := r.secret(s.custom.APIKey)
		if err == nil {
			return Credential{Key: key, Source: "models.json", Env: entryEnv}, nil
		}
		if storedErr == nil {
			storedErr = fmt.Errorf("apiKey in models.json cannot be resolved: %w", err)
		}
	}
	for _, name := range s.keyEnv {
		if v := r.envFor(entryEnv, name); v != "" {
			return Credential{Key: v, Source: "env:" + name, Env: entryEnv}, nil
		}
	}
	if s.bearerEnv != "" {
		if v := r.env(s.bearerEnv); v != "" {
			return Credential{Key: v, Source: "env:" + s.bearerEnv, Bearer: true, Env: entryEnv}, nil
		}
	}
	if storedErr != nil {
		return fail(false, "%s: the configured credential cannot be used: %v", s.name, storedErr)
	}
	if s.keyOptional {
		return Credential{Env: entryEnv}, nil
	}
	vars := strings.Join(s.keyEnv, " or ")
	if s.bearerEnv != "" {
		vars += " or " + s.bearerEnv
	}
	if vars == "" {
		return fail(true, "no API key for %s: add apiKey to its entry in models.json, or %s", s.name, loginHint(s.name))
	}
	return fail(true, "no API key for %s: set %s, or %s", s.name, vars, loginHint(s.name))
}

// Credential reports which key a provider would use, for `koine auth check` and the like. It does not contact the provider.
func (r *Router) Credential(provider string) (Credential, error) {
	s, err := r.lookup(CanonicalProvider(provider), "")
	if err != nil {
		return Credential{}, err
	}
	c, err := r.credential(s)
	if err == nil && c.Key == "" && (s.api == APIBedrock || s.api == APIVertex) {
		return r.ambientCredential(s, c) // no key: the machine's cloud sign-in must work instead
	}
	return c, err
}

// Resolve returns the provider and the model id to send to it. A provider whose credential is missing or unusable is returned as a
// Missing provider, so that frontends start and the reason appears when a request is made.
func (r *Router) Resolve(model string) (Provider, string, error) {
	p, id, _, err := r.resolve(model)
	return p, id, err
}

// resolve is Resolve that also returns what the router knew about the provider.
func (r *Router) resolve(model string) (Provider, string, setup, error) {
	name, id, err := r.split(model)
	if err != nil {
		return nil, "", setup{}, err
	}
	s, err := r.lookup(name, id)
	if err != nil {
		return nil, "", setup{}, err
	}
	cred, err := r.credential(s)
	if err != nil {
		return Missing{Reason: err.Error()}, id, s, nil
	}
	p, err := r.build(s, cred)
	if err != nil {
		return Missing{Reason: err.Error()}, id, s, nil
	}
	return p, id, s, nil
}

// prepare resolves req.Model and returns the provider with the request as it should be sent there: the model id without its provider
// prefix, and the conversation converted for that provider (TransformMessages). The caller's messages are not changed.
func (r *Router) prepare(req Request) (Provider, Request, error) {
	p, id, s, err := r.resolve(req.Model)
	if err != nil {
		return nil, req, err
	}
	req.Model = id
	req.Messages = TransformMessages(req.Messages, Target{API: s.api, Provider: s.name, SameOrigin: func(origin string) bool {
		op, oid, err := r.split(origin)
		return err == nil && op == s.name && oid == id
	}})
	return p, req, nil
}

// build makes the provider for a setup and credential.
func (r *Router) build(s setup, c Credential) (Provider, error) {
	headers := map[string]string{}
	for k, v := range s.headers {
		val, err := r.secret(v)
		if err != nil {
			return nil, fmt.Errorf("%s: header %q cannot be resolved: %w", s.name, k, err)
		}
		headers[k] = val
	}
	base := strings.TrimRight(s.base, "/")
	switch s.api {
	case APIAnthropic:
		return &Anthropic{Name: s.name, APIKey: c.Key, Bearer: c.Bearer || s.authHeader, BaseURL: base, Headers: headers, HTTP: r.HTTP, Retry: r.Retry}, nil
	case APICompletions:
		flags, _ := parseOpenAIFlags(s.compat)
		return &OpenAICompat{Name: s.name, APIKey: c.Key, BaseURL: base, Headers: headers, Flags: flags, Extra: s.sampling,
			PromptCache: isOpenAIHost(s.name, base), HTTP: r.HTTP, Retry: r.Retry}, nil
	case APIResponses:
		return &Responses{Name: s.name, APIKey: c.Key, BaseURL: base, Headers: headers, Extra: s.sampling, PromptCache: isOpenAIHost(s.name, base),
			HTTP: r.HTTP, Retry: r.Retry}, nil
	case APIAzure:
		return r.azure(s, c, headers)
	case APIGemini:
		return &Gemini{Name: s.name, APIKey: c.Key, Bearer: s.authHeader, BaseURL: base, Headers: headers, HTTP: r.HTTP, Retry: r.Retry}, nil
	case APIBedrock:
		return r.bedrock(s, c, headers)
	case APIVertex:
		return r.vertex(s, c, headers)
	}
	return nil, fmt.Errorf("%s: the api %q is not supported by Koine (supported: %s)", s.name, s.api, strings.Join(APIs, ", "))
}

// Complete implements Provider by resolving req.Model per call.
func (r *Router) Complete(ctx context.Context, req Request) (*Response, error) {
	p, req, err := r.prepare(req)
	if err != nil {
		return nil, err
	}
	return p.Complete(ctx, req)
}
