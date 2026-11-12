package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// recorder is an http.RoundTripper that remembers requests and answers with a fixed body.
type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
	resp string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.body = append(r.body, string(b))
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(r.resp)),
		Request: req}, nil
}

func (r *recorder) last(t *testing.T) (*http.Request, string) {
	t.Helper()
	if len(r.reqs) == 0 {
		t.Fatal("no request was made")
	}
	return r.reqs[len(r.reqs)-1], r.body[len(r.body)-1]
}

func loggedIn(access string, extra map[string]string) func(string) (OAuthCred, bool, error) {
	return func(string) (OAuthCred, bool, error) { return OAuthCred{Access: access, Extra: extra}, true, nil }
}

const chatReply = `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`

func TestAnthropicSubscriptionTokensGoAsBearerWithClaudeCodeIdentity(t *testing.T) {
	rec := &recorder{resp: `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`}
	r := &Router{HTTP: &http.Client{Transport: rec}, StoredOAuth: loggedIn("fake-oauth-access", nil), Getenv: func(string) string { return "" }}
	if _, err := r.Complete(context.Background(), Request{Model: "claude-sonnet-4-5", System: "Be brief.",
		Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}}}); err != nil {
		t.Fatal(err)
	}
	req, body := rec.last(t)
	if req.Header.Get("Authorization") != "Bearer fake-oauth-access" || req.Header.Get("X-Api-Key") != "" ||
		!strings.Contains(req.Header.Get("Anthropic-Beta"), "oauth-2025-04-20") || req.Header.Get("X-App") != "cli" {
		t.Errorf("headers %v", req.Header)
	}
	var b struct {
		System []struct{ Text string } `json:"system"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	if len(b.System) != 2 || b.System[0].Text != claudeCodeIdentity || b.System[1].Text != "Be brief." {
		t.Errorf("system %s", body)
	}
	// without a system prompt the identity still goes first
	if _, err := r.Complete(context.Background(), Request{Model: "claude-sonnet-4-5", Messages: []Message{{Role: "user",
		Content: []Block{{Type: "text", Text: "x"}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, body := rec.last(t); !strings.Contains(body, claudeCodeIdentity) {
		t.Errorf("identity missing: %s", body)
	}
}

func TestCodexLoginsCarryTheAccountHeaders(t *testing.T) {
	rec := &recorder{resp: `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{}}`}
	r := &Router{HTTP: &http.Client{Transport: rec}, StoredOAuth: loggedIn("codex-access", map[string]string{"accountId": "acct-9"}),
		Getenv: func(string) string { return "" }}
	if _, err := r.Complete(context.Background(), Request{Model: "openai-codex/gpt-5.1-codex",
		Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}}}); err != nil {
		t.Fatal(err)
	}
	req, _ := rec.last(t)
	if req.URL.String() != "https://chatgpt.com/backend-api/codex/responses" || req.Header.Get("Authorization") != "Bearer codex-access" ||
		req.Header.Get("Chatgpt-Account-Id") != "acct-9" || req.Header.Get("Openai-Beta") != "responses=experimental" ||
		req.Header.Get("Originator") != "koine" {
		t.Errorf("%s %v", req.URL, req.Header)
	}
}

func TestCopilotBaseAndHeadersFollowTheTokenAndTheRequest(t *testing.T) {
	rec := &recorder{resp: chatReply}
	token := "tid=1;exp=2;proxy-ep=proxy.business.githubcopilot.com;x=y"
	r := &Router{HTTP: &http.Client{Transport: rec}, StoredOAuth: loggedIn(token, nil), Getenv: func(string) string { return "" }}
	user := Message{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}
	if _, err := r.Complete(context.Background(), Request{Model: "github-copilot/gpt-4o", Messages: []Message{user}}); err != nil {
		t.Fatal(err)
	}
	req, _ := rec.last(t)
	if req.URL.String() != "https://api.business.githubcopilot.com/chat/completions" || req.Header.Get("X-Initiator") != "user" ||
		req.Header.Get("Copilot-Integration-Id") != "vscode-chat" || req.Header.Get("Authorization") != "Bearer "+token {
		t.Errorf("%s %v", req.URL, req.Header)
	}
	asst := Message{Role: "assistant", Content: []Block{{Type: "tool_use", ID: "c1", Name: "t", Input: json.RawMessage(`{}`)}}}
	res := Message{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "c1", Content: "done"}}}
	if _, err := r.Complete(context.Background(), Request{Model: "github-copilot/gpt-4o", Messages: []Message{user, asst, res}}); err != nil {
		t.Fatal(err)
	}
	if req, _ := rec.last(t); req.Header.Get("X-Initiator") != "agent" {
		t.Errorf("a tool-result turn is the agent's: %v", req.Header)
	}
	// fallbacks for the host
	if got := copilotBase("opaque", nil); got != copilotDefaultBase {
		t.Error(got)
	}
	if got := copilotBase("opaque", map[string]string{"enterpriseUrl": "ghe.example.com"}); got != "https://copilot-api.ghe.example.com" {
		t.Error(got)
	}
}

func TestCopilotDynamicHeaders(t *testing.T) {
	img := Message{Role: "user", Content: []Block{ImageBlock("image/png", "AAAA")}}
	if h := copilotDynamic(Request{Messages: []Message{img}}); h["Copilot-Vision-Request"] != "true" || h["X-Initiator"] != "user" {
		t.Errorf("%v", h)
	}
	asst := Message{Role: "assistant", Content: []Block{{Type: "text", Text: "x"}}}
	if h := copilotDynamic(Request{Messages: []Message{asst}}); h["X-Initiator"] != "agent" || h["Copilot-Vision-Request"] != "" {
		t.Errorf("%v", h)
	}
	withImg := Message{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "1", Images: []Block{ImageBlock("image/png", "AAAA")}}}}
	if h := copilotDynamic(Request{Messages: []Message{withImg}}); h["Copilot-Vision-Request"] != "true" || h["X-Initiator"] != "agent" {
		t.Errorf("%v", h)
	}
	if h := copilotDynamic(Request{}); h["X-Initiator"] != "user" {
		t.Errorf("%v", h)
	}
}

func TestSubscriptionProvidersExplainTheMissingLoginAndFallThroughOnErrors(t *testing.T) {
	r := &Router{Getenv: func(string) string { return "" }}
	_, err := r.Credential("openai-codex")
	var ce *CredentialError
	if !errors.As(err, &ce) || !ce.Missing || !strings.Contains(err.Error(), "not signed in to openai-codex") || !strings.Contains(err.Error(),
		"koine auth login openai-codex") {
		t.Errorf("%v", err)
	}
	if _, err := r.Credential("anthropic"); err == nil || !strings.Contains(err.Error(), "sign in with your account") {
		t.Errorf("anthropic hint: %v", err)
	}
	if _, err := r.Credential("groq"); err == nil || strings.Contains(err.Error(), "sign in with your account") {
		t.Errorf("groq hint: %v", err)
	}
	// an unusable login is reported only when nothing else works
	bad := &Router{Getenv: func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "fake-env"
		}
		return ""
	}, StoredOAuth: func(string) (OAuthCred, bool, error) { return OAuthCred{}, true, errors.New("expired") }}
	if c, err := bad.Credential("anthropic"); err != nil || c.Key != "fake-env" || c.OAuth {
		t.Errorf("%+v %v", c, err)
	}
	bad.Getenv = func(string) string { return "" }
	if _, err := bad.Credential("anthropic"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("%v", err)
	}
	// --api-key still beats a login
	bad.RuntimeKeys = map[string]string{"anthropic": "fake-flag"}
	bad.StoredOAuth = loggedIn("x", nil)
	if c, _ := bad.Credential("anthropic"); c.Key != "fake-flag" || c.OAuth {
		t.Errorf("%+v", c)
	}
	// a models.json baseUrl for Copilot is the user's choice
	rec := &recorder{resp: chatReply}
	custom := &Router{HTTP: &http.Client{Transport: rec}, StoredOAuth: loggedIn("tid=1;proxy-ep=proxy.individual.githubcopilot.com", nil),
		Custom: map[string]ProviderConfig{"github-copilot": {BaseURL: "https://proxy.example/v1"}}}
	if _, err := custom.Complete(context.Background(), Request{Model: "github-copilot/m", Messages: []Message{{Role: "user",
		Content: []Block{{Type: "text", Text: "x"}}}}}); err != nil {
		t.Fatal(err)
	}
	if req, _ := rec.last(t); req.URL.Host != "proxy.example" {
		t.Errorf("%s", req.URL)
	}
}
