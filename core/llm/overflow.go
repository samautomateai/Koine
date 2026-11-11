package llm

import (
	"errors"
	"strings"
)

// overflowHints are phrases that mean "the conversation does not fit the model's context window", whoever says them.
var overflowHints = []string{"prompt is too long", "context_length_exceeded", "maximum context length", "context window", "too many tokens",
	"request too large", "input is too long", "exceeds the maximum", "context length"}

// providerOverflow adds the wording particular to a provider (matched in lower case against the error body). They come from the providers'
// own error messages as Koine's authors know them; Pi's list (utils/overflow.ts) has not been compared line by line.
var providerOverflow = map[string][]string{
	"anthropic":  {"prompt is too long", "exceed context limit", "request_too_large"},
	"openai":     {"maximum context length", "context_length_exceeded", "reduce the length of the messages", "input exceeds the context window"},
	"openrouter": {"maximum context length", "context length", "too long for this model"},
	"groq":       {"reduce the length of the messages", "context_length_exceeded"},
	"deepseek":   {"maximum context length", "context length"},
	"mistral":    {"too large for model", "maximum context length"},
	"xai":        {"maximum prompt length", "maximum context length"},
	"together":   {"input validation error", "maximum context length", "inputs tokens + max_new_tokens must be"},
	"ollama":     {"prompt too long", "exceeds the context", "truncating input prompt"},
	"lmstudio":   {"greater than the context length", "context length", "n_ctx"},
	"google":     {"input token count", "exceeds the maximum number of tokens"},
	"llamacpp":   {"exceeds the available context size", "n_ctx"},
}

// IsOverflow reports whether err says the conversation does not fit the model's context window. A provider error is checked against that
// provider's own wording as well as the common phrases; HTTP 413 always counts; any other error is matched on its text.
func IsOverflow(err error) bool {
	if err == nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		if he.Status == 413 {
			return true
		}
		body := strings.ToLower(he.Body)
		if containsAny(body, overflowHints) {
			return true
		}
		if own, known := providerOverflow[he.Provider]; known {
			return containsAny(body, own)
		}
		for _, own := range providerOverflow { // a provider declared in the config: any known wording
			if containsAny(body, own) {
				return true
			}
		}
		return false
	}
	s := strings.ToLower(err.Error())
	return containsAny(s, overflowHints) || strings.Contains(s, "http 413")
}

func containsAny(s string, subs []string) bool {
	for _, h := range subs {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}
