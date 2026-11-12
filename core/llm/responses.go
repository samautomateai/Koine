package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Responses speaks OpenAI's Responses API (POST /responses). Azure OpenAI uses the same protocol with another address and key header. Every
// request is stateless (store: false); reasoning items are not sent back, so a conversation continues from its visible messages and tool
// calls only.
type Responses struct {
	Name        string // for error messages
	APIKey      string
	BaseURL     string            // up to and including /v1; "/responses" is added
	KeyHeader   string            // "" = "Authorization: Bearer <key>"; Azure uses "api-key"
	Query       string            // appended to the URL, e.g. "api-version=v1" for Azure
	Deployments map[string]string // model id -> deployment name (Azure)
	Headers     map[string]string
	Extra       map[string]any // samplingParams of models.json
	HTTP        *http.Client
	Retry       RetryPolicy
}

func (p *Responses) label() string {
	if p.Name == "" {
		return "openai"
	}
	return p.Name
}

// toResponsesInput converts messages to the API's input items. Function calls are sent with their call_id only (not the item id), which
// the API accepts when store is false and no reasoning items are replayed.
func toResponsesInput(msgs []Message) []map[string]any {
	var in []map[string]any
	for _, m := range msgs {
		var texts []string
		var parts, outputs []map[string]any
		var fromTools []Block
		flush := func() {
			if len(texts) > 0 {
				in = append(in, map[string]any{"role": "assistant", "content": strings.Join(texts, "\n")})
				texts = nil
			}
		}
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if b.Text == "" {
					continue
				}
				if m.Role == "assistant" {
					texts = append(texts, b.Text)
				} else {
					parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
				}
			case "image":
				parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:" + b.MimeType() + ";base64," + b.Base64()})
			case "tool_use":
				flush()
				args := string(b.Input)
				if args == "" {
					args = "{}"
				}
				in = append(in, map[string]any{"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": args})
			case "tool_result":
				out := b.Content
				if b.IsError {
					out = "Error: " + out
				}
				outputs = append(outputs, map[string]any{"type": "function_call_output", "call_id": b.ToolUseID, "output": out})
				fromTools = append(fromTools, b.Images...)
			}
		}
		flush()
		in = append(in, outputs...)
		if len(fromTools) > 0 {
			ps := []map[string]any{{"type": "input_text", "text": "Attached image(s) from tool result:"}}
			for _, im := range fromTools {
				ps = append(ps, map[string]any{"type": "input_image", "image_url": "data:" + im.MimeType() + ";base64," + im.Base64()})
			}
			in = append(in, map[string]any{"role": "user", "content": ps})
		}
		if len(parts) > 0 {
			in = append(in, map[string]any{"role": m.Role, "content": parts})
		}
	}
	return in
}

func (p *Responses) request(req Request, stream bool) (string, map[string]string, []byte, error) {
	model := req.Model
	if d := p.Deployments[model]; d != "" {
		model = d
	}
	body := map[string]any{}
	for k, v := range p.Extra {
		body[k] = v
	}
	body["model"], body["input"], body["store"] = model, toResponsesInput(req.Messages), false
	if req.System != "" {
		body["instructions"] = req.System
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if effort := openAIEffort(req.Thinking); effort != "" {
		body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools { // strict defaults to true in this API, and Koine's schemas are not strict-mode schemas
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.InputSchema,
				"strict": false})
		}
		body["tools"] = tools
	}
	if stream {
		body["stream"] = true
	}
	payload, err := json.Marshal(body)
	headers := map[string]string{}
	for k, v := range p.Headers {
		headers[k] = v
	}
	switch {
	case p.APIKey == "":
	case p.KeyHeader != "":
		headers[p.KeyHeader] = p.APIKey
	default:
		headers["authorization"] = "Bearer " + p.APIKey
	}
	u := strings.TrimRight(p.BaseURL, "/") + "/responses"
	if p.Query != "" {
		u += "?" + p.Query
	}
	return u, headers, payload, err
}

// Complete implements Provider.
func (p *Responses) Complete(ctx context.Context, req Request) (*Response, error) {
	u, headers, payload, err := p.request(req, false)
	if err != nil {
		return nil, err
	}
	data, err := postJSON(ctx, p.HTTP, p.label(), u, headers, payload, p.Retry)
	if err != nil {
		return nil, err
	}
	return parseResponses(p.label(), data)
}

type respItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type respObject struct {
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []respItem `json:"output"`
	Usage  struct {
		Input   int `json:"input_tokens"`
		Output  int `json:"output_tokens"`
		Details struct {
			Cached int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
}

// parseResponses reads a complete Responses API answer (a non-streamed body, or the response object of a final stream event).
func parseResponses(label string, data []byte) (*Response, error) {
	var o respObject
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if o.Status == "failed" || (o.Error != nil && o.Error.Message != "") {
		msg := "the response failed"
		if o.Error != nil {
			msg = strings.TrimSpace(o.Error.Code + " " + o.Error.Message)
		}
		return nil, fmt.Errorf("%s: %s", label, msg)
	}
	resp := &Response{StopReason: "stop"}
	if u := o.Usage; u.Input > 0 || u.Output > 0 {
		resp.Usage = Usage{Input: max(u.Input-u.Details.Cached, 0), Output: u.Output, CacheRead: u.Details.Cached}
	}
	for _, it := range o.Output {
		switch it.Type {
		case "reasoning":
			var sum []string
			for _, s := range it.Summary {
				sum = append(sum, s.Text)
			}
			if t := strings.Join(sum, "\n\n"); t != "" {
				resp.Content = append(resp.Content, Block{Type: "thinking", Thinking: t}) // no signature: shown and kept, never sent back
			}
		case "message":
			for _, c := range it.Content {
				if t := c.Text + c.Refusal; t != "" {
					resp.Content = append(resp.Content, Block{Type: "text", Text: t})
				}
			}
		case "function_call":
			id := it.CallID
			if id == "" {
				id = it.ID
			}
			resp.Content = append(resp.Content, Block{Type: "tool_use", ID: id, Name: it.Name, Input: oaToolInput(orEmptyObject(it.Arguments))})
			resp.StopReason = "tool_calls"
		}
	}
	if o.Status == "incomplete" {
		resp.StopReason = "length"
		if o.IncompleteDetails != nil && o.IncompleteDetails.Reason != "" && o.IncompleteDetails.Reason != "max_output_tokens" {
			resp.StopReason = o.IncompleteDetails.Reason
		}
	}
	return resp, nil
}

func orEmptyObject(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

// azure builds the Azure OpenAI provider. The endpoint is AZURE_OPENAI_BASE_URL, else built from AZURE_OPENAI_RESOURCE_NAME, else the
// provider's baseUrl in models.json (Pi: the variables take priority over it); AZURE_OPENAI_DEPLOYMENT_NAME_MAP maps model ids to
// deployment names and AZURE_OPENAI_API_VERSION (default v1) is sent as api-version.
func (r *Router) azure(s setup, c Credential, headers map[string]string) (Provider, error) {
	base := r.envFor(c.Env, "AZURE_OPENAI_BASE_URL")
	if base == "" {
		if res := r.envFor(c.Env, "AZURE_OPENAI_RESOURCE_NAME"); res != "" {
			base = "https://" + res + ".openai.azure.com/openai/v1"
		}
	}
	if base == "" {
		base = s.base
	}
	if base == "" {
		return nil, errors.New(s.name + ": set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME (or baseUrl in models.json)")
	}
	version := r.envFor(c.Env, "AZURE_OPENAI_API_VERSION")
	if version == "" {
		version = "v1"
	}
	deployments := map[string]string{}
	for _, pair := range strings.Split(r.envFor(c.Env, "AZURE_OPENAI_DEPLOYMENT_NAME_MAP"), ",") {
		if model, dep, ok := strings.Cut(strings.TrimSpace(pair), "="); ok && model != "" && dep != "" {
			deployments[strings.TrimSpace(model)] = strings.TrimSpace(dep)
		}
	}
	return &Responses{Name: s.name, APIKey: c.Key, BaseURL: base, KeyHeader: "api-key", Query: "api-version=" + url.QueryEscape(version),
		Deployments: deployments, Headers: headers, Extra: s.sampling, HTTP: r.HTTP, Retry: r.Retry}, nil
}
