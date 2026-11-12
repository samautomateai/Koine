package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Bedrock speaks Amazon Bedrock's Converse and ConverseStream APIs (`bedrock-converse-stream` in models.json). A request is signed with
// AWS Signature Version 4 (SigV4) using keys from Creds, or carries a Bedrock API key as a bearer token; neither needs an AWS SDK.
//
// Limits: written from AWS's documentation and tested against fake servers only. Thinking is requested for Anthropic Claude models only
// (through additionalModelRequestFields), and cache points are written for Claude models (or when ForceCache is set).
type Bedrock struct {
	Name       string // for error messages; "amazon-bedrock" when empty
	BaseURL    string // the runtime endpoint, for example https://bedrock-runtime.us-east-1.amazonaws.com
	Region     string // the signing region
	Bearer     string // a Bedrock API key (AWS_BEARER_TOKEN_BEDROCK): sent as a bearer token instead of signing
	Creds      func() (AWSCredentials, error)
	SkipAuth   bool // AWS_BEDROCK_SKIP_AUTH: send no credentials at all (for a proxy that adds its own)
	ForceCache bool // AWS_BEDROCK_FORCE_CACHE: write cache points even when the model id is not a recognisable Claude name
	Headers    map[string]string
	HTTP       *http.Client
	Retry      RetryPolicy
	Now        func() time.Time // for tests; time.Now when nil
}

func (b *Bedrock) label() string {
	if b.Name == "" {
		return "amazon-bedrock"
	}
	return b.Name
}

func isClaudeID(model string) bool { return strings.Contains(strings.ToLower(model), "claude") }

func (b *Bedrock) caches(model string) bool { return b.ForceCache || isClaudeID(model) }

// bedrockFormat maps an image media type to Converse's format name.
func bedrockFormat(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return "png"
}

func bedrockImage(b Block) map[string]any {
	return map[string]any{"image": map[string]any{"format": bedrockFormat(b.MimeType()), "source": map[string]any{"bytes": b.Base64()}}}
}

var cachePoint = map[string]any{"cachePoint": map[string]any{"type": "default"}}

// toBedrockMessages converts the history to Converse messages. Empty text is dropped, a thinking block is replayed only with its
// signature (and not at all when dropThinking says thinking is off), neighbouring messages of one role are merged because Converse needs
// the roles to alternate, and the last user message ends with a cache point when cache is set.
func toBedrockMessages(in []Message, dropThinking, cache bool) []map[string]any {
	var out []map[string]any
	for _, m := range in {
		var parts []map[string]any
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					parts = append(parts, map[string]any{"text": b.Text})
				}
			case "image":
				parts = append(parts, bedrockImage(b))
			case "thinking":
				switch {
				case dropThinking || b.Signature == "":
				case b.Redacted:
					parts = append(parts, map[string]any{"reasoningContent": map[string]any{"redactedContent": b.Signature}})
				default:
					parts = append(parts, map[string]any{"reasoningContent": map[string]any{
						"reasoningText": map[string]any{"text": b.Thinking, "signature": b.Signature}}})
				}
			case "tool_use":
				var input any = map[string]any{}
				if len(b.Input) > 0 && json.Valid(b.Input) {
					input = b.Input
				}
				parts = append(parts, map[string]any{"toolUse": map[string]any{"toolUseId": b.ID, "name": b.Name, "input": input}})
			case "tool_result":
				text := b.Content
				if text == "" {
					text = "(no output)"
				}
				content := []map[string]any{{"text": text}}
				for _, im := range b.Images {
					content = append(content, bedrockImage(im))
				}
				res := map[string]any{"toolUseId": b.ToolUseID, "content": content}
				if b.IsError {
					res["status"] = "error"
				}
				parts = append(parts, map[string]any{"toolResult": res})
			}
		}
		if len(parts) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1]["role"] == m.Role {
			out[n-1]["content"] = append(out[n-1]["content"].([]map[string]any), parts...)
			continue
		}
		out = append(out, map[string]any{"role": m.Role, "content": parts})
	}
	if n := len(out); cache && n > 0 && out[n-1]["role"] == "user" {
		out[n-1]["content"] = append(out[n-1]["content"].([]map[string]any), cachePoint)
	}
	return out
}

// bedrockThinking returns additionalModelRequestFields for a Claude model and the max tokens to send, and whether earlier thinking is
// dropped. Models the catalog does not know arrive with Mode "effort"; for Claude that means a token budget.
func bedrockThinking(t *Thinking, limit int, explicit bool) (fields map[string]any, newMax int, drop bool) {
	if t != nil && t.Mode == ModeEffort && t.Level != "off" {
		c := *t
		c.Mode = ModeBudget
		c.Budget = geminiBudget(t) // the per-level default when no budget was resolved
		t = &c
	}
	th, effort, newMax, drop := anthropicThinking(t, limit, explicit)
	if th == nil {
		return nil, newMax, drop
	}
	fields = map[string]any{"thinking": th}
	if effort != "" {
		fields["output_config"] = map[string]any{"effort": effort}
	}
	return fields, newMax, drop
}

func (b *Bedrock) body(req Request) ([]byte, error) {
	limit, explicit := req.MaxTokens, req.MaxTokens > 0
	if limit == 0 {
		limit = defaultMaxTokens
	}
	var extra map[string]any
	drop := false
	if isClaudeID(req.Model) {
		extra, limit, drop = bedrockThinking(req.Thinking, limit, explicit)
	}
	cache := b.caches(req.Model)
	body := map[string]any{"messages": toBedrockMessages(req.Messages, drop, cache), "inferenceConfig": map[string]any{"maxTokens": limit}}
	if req.System != "" {
		sys := []map[string]any{{"text": req.System}}
		if cache {
			sys = append(sys, cachePoint)
		}
		body["system"] = sys
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"toolSpec": map[string]any{"name": t.Name, "description": t.Description,
				"inputSchema": map[string]any{"json": t.InputSchema}}})
		}
		if cache {
			tools = append(tools, cachePoint)
		}
		body["toolConfig"] = map[string]any{"tools": tools}
	}
	if extra != nil {
		body["additionalModelRequestFields"] = extra
	}
	return json.Marshal(body)
}

// request builds the URL, headers and body of one call, signed or with the bearer token. The model id (which may contain ":" or, for an
// inference-profile ARN, "/") is one path segment, so it is percent-encoded.
func (b *Bedrock) request(req Request, stream bool) (string, map[string]string, []byte, error) {
	payload, err := b.body(req)
	if err != nil {
		return "", nil, nil, err
	}
	op := "/converse"
	if stream {
		op = "/converse-stream"
	}
	u := strings.TrimRight(b.BaseURL, "/") + "/model/" + awsEscape(req.Model, false) + op
	headers := map[string]string{"content-type": "application/json"}
	for k, v := range b.Headers {
		headers[strings.ToLower(k)] = v
	}
	switch {
	case b.SkipAuth:
	case b.Bearer != "":
		headers["authorization"] = "Bearer " + b.Bearer
	default:
		if b.Creds == nil {
			return "", nil, nil, errors.New(b.label() + ": no AWS credentials")
		}
		creds, err := b.Creds()
		if err != nil {
			return "", nil, nil, fmt.Errorf("%s: %w", b.label(), err)
		}
		now := time.Now
		if b.Now != nil {
			now = b.Now
		}
		signed, err := SigV4{Creds: creds, Region: b.Region, Service: "bedrock"}.Sign("POST", u, map[string]string{"content-type": "application/json"},
			payload, now())
		if err != nil {
			return "", nil, nil, fmt.Errorf("%s: %w", b.label(), err)
		}
		for k, v := range signed {
			headers[k] = v
		}
	}
	return u, headers, payload, nil
}

// Complete implements Provider with the Converse operation.
func (b *Bedrock) Complete(ctx context.Context, req Request) (*Response, error) {
	u, headers, payload, err := b.request(req, false)
	if err != nil {
		return nil, err
	}
	data, err := postJSON(ctx, b.HTTP, b.label(), u, headers, payload, b.Retry)
	if err != nil {
		return nil, err
	}
	return parseBedrock(b.label(), data)
}

// bedrockUsage is the usage object of a Converse response and of the stream's metadata event.
type bedrockUsage struct {
	Input      int `json:"inputTokens"`
	Output     int `json:"outputTokens"`
	CacheRead  int `json:"cacheReadInputTokens"`
	CacheWrite int `json:"cacheWriteInputTokens"`
}

// usage follows Koine's convention that Input excludes cached tokens; Bedrock's inputTokens already does.
func (u bedrockUsage) usage() Usage {
	return Usage(u)
}

// parseBedrock reads a Converse response.
func parseBedrock(label string, data []byte) (*Response, error) {
	var out struct {
		Output struct {
			Message struct {
				Content []struct {
					Text    string `json:"text"`
					ToolUse *struct {
						ID    string          `json:"toolUseId"`
						Name  string          `json:"name"`
						Input json.RawMessage `json:"input"`
					} `json:"toolUse"`
					Reasoning *struct {
						Text *struct {
							Text      string `json:"text"`
							Signature string `json:"signature"`
						} `json:"reasoningText"`
						Redacted string `json:"redactedContent"`
					} `json:"reasoningContent"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string       `json:"stopReason"`
		Usage      bedrockUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	resp := &Response{StopReason: out.StopReason, Usage: out.Usage.usage()}
	for _, c := range out.Output.Message.Content {
		switch {
		case c.ToolUse != nil:
			in := c.ToolUse.Input
			if len(in) == 0 {
				in = json.RawMessage("{}")
			}
			resp.Content = append(resp.Content, Block{Type: "tool_use", ID: c.ToolUse.ID, Name: c.ToolUse.Name, Input: in})
		case c.Reasoning != nil && c.Reasoning.Text != nil:
			resp.Content = append(resp.Content, Block{Type: "thinking", Thinking: c.Reasoning.Text.Text, Signature: c.Reasoning.Text.Signature})
		case c.Reasoning != nil && c.Reasoning.Redacted != "":
			resp.Content = append(resp.Content, Block{Type: "thinking", Redacted: true, Signature: c.Reasoning.Redacted})
		case c.Text != "":
			resp.Content = append(resp.Content, Block{Type: "text", Text: c.Text})
		}
	}
	return resp, nil
}
