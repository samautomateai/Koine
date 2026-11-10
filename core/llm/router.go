package llm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// ProviderConfig declares a custom OpenAI-compatible (default) or Anthropic endpoint.
type ProviderConfig struct {
	Type      string `json:"type,omitempty"` // "openai" (default) | "anthropic"
	BaseURL   string `json:"baseUrl"`
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
}

type builtin struct{ base, keyEnv string }

var builtins = map[string]builtin{
	"openai":     {"https://api.openai.com/v1", "OPENAI_API_KEY"},
	"openrouter": {"https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
	"groq":       {"https://api.groq.com/openai/v1", "GROQ_API_KEY"},
	"deepseek":   {"https://api.deepseek.com/v1", "DEEPSEEK_API_KEY"},
	"mistral":    {"https://api.mistral.ai/v1", "MISTRAL_API_KEY"},
	"xai":        {"https://api.x.ai/v1", "XAI_API_KEY"},
	"together":   {"https://api.together.xyz/v1", "TOGETHER_API_KEY"},
	"ollama":     {"http://localhost:11434/v1", ""},
	"lmstudio":   {"http://localhost:1234/v1", ""},
}

// Router picks a provider from the model id, like pi-go: a known prefix
// ("openrouter/anthropic/claude-3.5"), a recognisable bare name ("claude-…",
// "gpt-…", "o3…"), or a custom provider from config. Unknown prefixes are an
// error — it never guesses.
type Router struct {
	Custom map[string]ProviderConfig
	HTTP   *http.Client
	Getenv func(string) string // defaults to os.Getenv
}

func (r *Router) env(k string) string {
	if r.Getenv != nil {
		return r.Getenv(k)
	}
	return os.Getenv(k)
}

func isOpenAIName(m string) bool {
	for _, p := range []string{"gpt-", "chatgpt-", "o1", "o3", "o4"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// Resolve returns the provider and the model id to send to it.
func (r *Router) Resolve(model string) (Provider, string, error) {
	prefix, rest, hasSlash := strings.Cut(model, "/")
	if !hasSlash {
		switch {
		case strings.HasPrefix(model, "claude"):
			prefix, rest = "anthropic", model
		case isOpenAIName(model):
			prefix, rest = "openai", model
		default:
			return nil, "", fmt.Errorf("cannot infer a provider for model %q: use a prefix (e.g. openrouter/%s, ollama/%s) "+
				"or declare a provider in config; known: %s", model, model, model, r.known())
		}
	}
	if prefix == "anthropic" {
		return r.anthropic(ProviderConfig{BaseURL: r.env("ANTHROPIC_BASE_URL"), APIKeyEnv: "ANTHROPIC_API_KEY"}), rest, nil
	}
	if pc, ok := r.Custom[prefix]; ok {
		if pc.Type == "anthropic" {
			return r.anthropic(pc), rest, nil
		}
		return r.openai(prefix, pc), rest, nil
	}
	if b, ok := builtins[prefix]; ok {
		base := b.base
		if prefix == "openai" && r.env("OPENAI_BASE_URL") != "" {
			base = r.env("OPENAI_BASE_URL")
		}
		return r.openai(prefix, ProviderConfig{BaseURL: base, APIKeyEnv: b.keyEnv}), rest, nil
	}
	return nil, "", fmt.Errorf("unknown provider prefix %q in model %q; known: %s", prefix, model, r.known())
}

func (r *Router) known() string {
	set := map[string]bool{"anthropic": true}
	for k := range builtins {
		set[k] = true
	}
	for k := range r.Custom {
		set[k] = true
	}
	var names []string
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (r *Router) anthropic(pc ProviderConfig) Provider {
	key := r.env(pc.APIKeyEnv)
	if key == "" {
		return Missing{Reason: pc.APIKeyEnv + " is not set — export it to start chatting"}
	}
	base := strings.TrimRight(pc.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return &Anthropic{APIKey: key, BaseURL: base, HTTP: r.HTTP}
}

func (r *Router) openai(name string, pc ProviderConfig) Provider {
	key := ""
	if pc.APIKeyEnv != "" {
		if key = r.env(pc.APIKeyEnv); key == "" {
			return Missing{Reason: pc.APIKeyEnv + " is not set — export it to use " + name + " models"}
		}
	}
	return &OpenAICompat{Name: name, APIKey: key, BaseURL: pc.BaseURL, HTTP: r.HTTP}
}

// Complete implements Provider by resolving req.Model per call.
func (r *Router) Complete(ctx context.Context, req Request) (*Response, error) {
	p, model, err := r.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	req.Model = model
	return p.Complete(ctx, req)
}
