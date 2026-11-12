package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

// Anthropic talks to the Messages API.
type Anthropic struct {
	Name    string // for error messages; "anthropic" when empty
	APIKey  string
	Bearer  bool // send the key as "Authorization: Bearer" (ANTHROPIC_AUTH_TOKEN, authHeader) instead of x-api-key
	BaseURL string
	Headers map[string]string // extra request headers from models.json
	HTTP    *http.Client
	Retry   RetryPolicy
}

func (a *Anthropic) label() string {
	if a.Name == "" {
		return "anthropic"
	}
	return a.Name
}

func NewAnthropicFromEnv() (*Anthropic, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is not set")
	}
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return &Anthropic{APIKey: key, BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 10 * time.Minute}}, nil
}

var ephemeral = &CacheControl{Type: "ephemeral"}

// defaultMaxTokens is used when the request names no limit.
const defaultMaxTokens = 8192

// anthropicThinking turns a resolved Thinking into the request's thinking object and effort. newMax is the max_tokens to send: thinking
// tokens count against it, so a budget needs room. drop says thinking is disabled, so earlier thinking blocks are not sent back.
func anthropicThinking(t *Thinking, limit int, explicitMax bool) (th map[string]any, effort string, newMax int, drop bool) {
	newMax = limit
	if t == nil || t.Level == "" {
		return nil, "", limit, false
	}
	if t.Level == "off" {
		switch t.Off {
		case "omit":
			return nil, "", limit, false
		case "", "disabled":
			return map[string]any{"type": "disabled"}, "", limit, true
		}
		return map[string]any{"type": t.Off}, "", limit, false
	}
	if t.Mode == ModeBudget {
		b := max(t.Budget, minBudget)
		if limit <= b {
			switch {
			case !explicitMax:
				newMax = b + defaultMaxTokens
			case limit-1024 >= minBudget:
				b = limit - 1024
			default:
				return nil, "", limit, false // too little room to think
			}
		}
		return map[string]any{"type": "enabled", "budget_tokens": b}, "", newMax, false
	}
	effort = t.Level
	if effort == "minimal" {
		effort = "low" // Anthropic's effort starts at low
	}
	return map[string]any{"type": "adaptive"}, effort, limit, false
}

// anthropicMessages converts the history to the wire form: empty text is dropped, thinking blocks are replayed with their signatures
// (blocks without one cannot be replayed and are dropped, as are all of them when thinking is disabled), and the last block of the last
// user message is marked as a cache breakpoint.
func anthropicMessages(in []Message, dropThinking bool) []Message {
	msgs := make([]Message, 0, len(in))
	for _, m := range in {
		var bs []Block
		for _, b := range m.Content {
			switch {
			case b.Type == "text" && b.Text == "":
				continue
			case b.Type == "thinking":
				if dropThinking || b.Signature == "" {
					continue
				}
				if b.Redacted {
					b = Block{Type: "redacted_thinking", Data: b.Signature}
				} else {
					b = Block{Type: "thinking", Thinking: b.Thinking, Signature: b.Signature}
				}
			case b.Type == "tool_use" && len(b.Input) == 0:
				b.Input = json.RawMessage("{}")
			}
			bs = append(bs, b)
		}
		if len(bs) > 0 {
			msgs = append(msgs, Message{Role: m.Role, Content: bs})
		}
	}
	if n := len(msgs); n > 0 && msgs[n-1].Role == "user" {
		last := msgs[n-1].Content
		last[len(last)-1].CacheControl = ephemeral
	}
	return msgs
}

// anthropicBody builds the Messages API request. Prompt caching marks three breakpoints: the system prompt, the last tool and the last
// user message (the API allows four).
func anthropicBody(req Request) map[string]any {
	limit, explicit := req.MaxTokens, req.MaxTokens > 0
	if limit == 0 {
		limit = defaultMaxTokens
	}
	th, effort, limit, drop := anthropicThinking(req.Thinking, limit, explicit)
	body := map[string]any{"model": req.Model, "max_tokens": limit, "messages": anthropicMessages(req.Messages, drop)}
	if th != nil {
		body["thinking"] = th
	}
	if effort != "" {
		body["output_config"] = map[string]any{"effort": effort}
	}
	if req.System != "" {
		body["system"] = []Block{{Type: "text", Text: req.System, CacheControl: ephemeral}}
	}
	if len(req.Tools) > 0 {
		tools := append([]ToolDef(nil), req.Tools...)
		tools[len(tools)-1].CacheControl = ephemeral
		body["tools"] = tools
	}
	return body
}

func (a *Anthropic) request(req Request, stream bool) (url string, headers map[string]string, payload []byte, err error) {
	body := anthropicBody(req)
	if stream {
		body["stream"] = true
	}
	payload, err = json.Marshal(body)
	headers = map[string]string{"anthropic-version": "2023-06-01"}
	for k, v := range a.Headers {
		headers[k] = v
	}
	if a.Bearer {
		headers["authorization"] = "Bearer " + a.APIKey
	} else if a.APIKey != "" {
		headers["x-api-key"] = a.APIKey
	}
	return a.BaseURL + "/v1/messages", headers, payload, err
}

func (a *Anthropic) Complete(ctx context.Context, req Request) (*Response, error) {
	url, headers, payload, err := a.request(req, false)
	if err != nil {
		return nil, err
	}
	data, err := postJSON(ctx, a.HTTP, a.label(), url, headers, payload, a.Retry)
	if err != nil {
		return nil, err
	}
	return parseAnthropic(data)
}

// anthropicUsage is the usage object of a message and of the stream events that carry parts of it.
type anthropicUsage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

func (u anthropicUsage) usage() Usage {
	return Usage(u)
}

// parseAnthropic reads a complete (not streamed) Messages API answer.
func parseAnthropic(data []byte) (*Response, error) {
	var out struct {
		Content    []Block        `json:"content"`
		StopReason string         `json:"stop_reason"`
		Usage      anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	for i, b := range out.Content {
		if b.Type == "redacted_thinking" { // kept as a thinking block whose payload is its signature
			out.Content[i] = Block{Type: "thinking", Redacted: true, Signature: b.Data}
		}
	}
	return &Response{Content: out.Content, StopReason: out.StopReason, Usage: out.Usage.usage()}, nil
}
