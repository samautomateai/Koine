package llm

import (
	"regexp"
	"strings"

	"koine/core/auth/oauth"
)

// OAuthCred is the part of a subscription login that requests need: the access token and the entry's extra fields.
type OAuthCred struct {
	Access string
	Extra  map[string]string
}

// claudeCodeIdentity is the first system block Anthropic expects on requests made with a Claude subscription token (Pi sends the same).
const claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

// codexBase is the ChatGPT backend that serves Codex models with a subscription login.
const codexBase = "https://chatgpt.com/backend-api/codex"

// copilotDefaultBase serves individual accounts when the token names no proxy endpoint.
const copilotDefaultBase = "https://api.individual.githubcopilot.com"

var proxyEP = regexp.MustCompile(`proxy-ep=([^;]+)`)

// copilotBase derives the API address from a Copilot token, which carries `proxy-ep=proxy.individual.githubcopilot.com;...`, else from
// the enterprise host of the login.
func copilotBase(token string, extra map[string]string) string {
	if m := proxyEP.FindStringSubmatch(token); m != nil {
		return "https://" + strings.Replace(m[1], "proxy.", "api.", 1)
	}
	if d := extra["enterpriseUrl"]; d != "" {
		return "https://copilot-api." + d
	}
	return copilotDefaultBase
}

// oauthHeaders are the extra request headers a subscription login needs, by provider.
func oauthHeaders(provider string, c Credential) map[string]string {
	switch provider {
	case "openai-codex":
		h := map[string]string{"OpenAI-Beta": "responses=experimental", "originator": "koine"}
		if id := c.Extra["accountId"]; id != "" {
			h["chatgpt-account-id"] = id
		}
		return h
	case "github-copilot":
		h := map[string]string{}
		for k, v := range oauth.CopilotHeaders {
			h[k] = v
		}
		return h
	}
	return nil
}

// copilotDynamic adds the headers Copilot bills by: X-Initiator says whether the user or the agent loop caused the request, and vision
// requests are marked.
func copilotDynamic(req Request) map[string]string {
	h := map[string]string{"Openai-Intent": "conversation-edits", "X-Initiator": "user"}
	if n := len(req.Messages); n > 0 && req.Messages[n-1].Role != "user" {
		h["X-Initiator"] = "agent"
	} else if n > 0 {
		for _, b := range req.Messages[n-1].Content {
			if b.Type == "tool_result" { // tool results travel in a user message but are the agent's own loop
				h["X-Initiator"] = "agent"
			}
		}
	}
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == "image" || (b.Type == "tool_result" && len(b.Images) > 0) {
				h["Copilot-Vision-Request"] = "true"
			}
		}
	}
	return h
}
