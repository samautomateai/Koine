package llm

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// OpenAICompat speaks the Chat Completions API with tool calling. That covers
// OpenAI itself and most other providers: OpenRouter, Groq, DeepSeek, Mistral,
// xAI, Ollama, LM Studio, vLLM, …
type OpenAICompat struct {
	Name    string // for error messages
	APIKey  string // optional (local servers)
	BaseURL string // e.g. https://api.openai.com/v1
	Headers map[string]string
	Flags   OpenAIFlags    // the compat flags of models.json that change the request
	Extra   map[string]any // samplingParams of models.json: fields merged into every request body
	HTTP    *http.Client
	Retry   RetryPolicy
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content"` // a string, or an array of parts when the message carries images
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"` // tool messages, for servers with requiresToolResultName
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func sp(s string) *string { return &s }

// oaImage is an image part: chat completions take images as data URLs.
func oaImage(b Block) map[string]any {
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + b.MimeType() + ";base64," + b.Base64()}}
}

// toOpenAI converts provider-neutral messages: tool_use blocks become tool_calls on the assistant message, and each tool_result becomes
// its own "tool" message. Tool messages cannot carry images, so the images of a tool result follow as a user message, as Pi does. A user
// message with images has an array of text and image_url parts instead of a string.
func toOpenAI(system string, msgs []Message) []oaMessage {
	return toOpenAIWith(system, msgs, OpenAIFlags{})
}

// toOpenAIWith is toOpenAI for a server with the quirks fl describes.
func toOpenAIWith(system string, msgs []Message, fl OpenAIFlags) []oaMessage {
	var out []oaMessage
	names := map[string]string{} // tool call id -> tool name, for requiresToolResultName
	if system != "" {
		out = append(out, oaMessage{Role: "system", Content: sp(system)})
	}
	for _, m := range msgs {
		var text []string
		var parts []map[string]any
		var calls []oaToolCall
		var results []oaMessage
		var fromTools []Block
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					text = append(text, b.Text)
					parts = append(parts, map[string]any{"type": "text", "text": b.Text})
				}
			case "image":
				parts = append(parts, oaImage(b))
			case "tool_use":
				names[b.ID] = b.Name
				c := oaToolCall{ID: b.ID, Type: "function"}
				c.Function.Name = b.Name
				c.Function.Arguments = string(b.Input)
				if c.Function.Arguments == "" {
					c.Function.Arguments = "{}"
				}
				calls = append(calls, c)
			case "tool_result":
				content := b.Content
				if b.IsError {
					content = "Error: " + content
				}
				tm := oaMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: sp(content)}
				if fl.ToolResultName {
					tm.Name = names[b.ToolUseID]
				}
				results = append(results, tm)
				fromTools = append(fromTools, b.Images...)
			}
		}
		out = append(out, results...)
		if len(fromTools) > 0 {
			ps := []map[string]any{{"type": "text", "text": "Attached image(s) from tool result:"}}
			for _, im := range fromTools {
				ps = append(ps, oaImage(im))
			}
			out = append(out, oaMessage{Role: "user", Content: ps})
		}
		if len(parts) == 0 && len(calls) == 0 {
			continue
		}
		msg := oaMessage{Role: m.Role, ToolCalls: calls}
		switch {
		case len(parts) > len(text):
			msg.Content = parts
		case len(text) > 0:
			msg.Content = sp(strings.Join(text, "\n"))
		case fl.NonNullAssistant && m.Role == "assistant":
			msg.Content = sp("")
		}
		out = append(out, msg)
	}
	return out
}

func (o *OpenAICompat) label() string {
	if o.Name == "" {
		return "openai"
	}
	return o.Name
}

func (o *OpenAICompat) request(req Request, stream bool) (url string, headers map[string]string, payload []byte, err error) {
	msgs := toOpenAIWith(req.System, req.Messages, o.Flags)
	body := map[string]any{}
	for k, v := range o.Extra { // samplingParams first, so nothing here can be overridden by them
		body[k] = v
	}
	body["model"], body["messages"] = req.Model, msgs
	effort := openAIEffort(req.Thinking)
	if effort != "" && !o.Flags.NoReasoningEffort {
		body["reasoning_effort"] = effort
	}
	if req.MaxTokens > 0 {
		field := o.Flags.MaxTokensField
		if field == "" {
			field = "max_tokens"
			if effort != "" { // reasoning models reject max_tokens
				field = "max_completion_tokens"
			}
		}
		body[field] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.InputSchema}})
		}
		body["tools"] = tools
	} else if o.Flags.ToolsWithHistory && hasToolHistory(req.Messages) {
		body["tools"] = []any{}
	}
	if stream {
		body["stream"] = true
		if !o.Flags.NoStreamUsage {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	payload, err = json.Marshal(body)
	headers = map[string]string{}
	for k, v := range o.Headers {
		headers[k] = v
	}
	if o.APIKey != "" {
		headers["authorization"] = "Bearer " + o.APIKey
	}
	return strings.TrimRight(o.BaseURL, "/") + "/chat/completions", headers, payload, err
}

func (o *OpenAICompat) Complete(ctx context.Context, req Request) (*Response, error) {
	url, headers, payload, err := o.request(req, false)
	if err != nil {
		return nil, err
	}
	data, err := postJSON(ctx, o.HTTP, o.label(), url, headers, payload, o.Retry)
	if err != nil {
		return nil, err
	}
	return parseOpenAI(o.label(), data)
}

// oaUsage is Chat Completions' usage object. OpenAI counts cached tokens inside prompt_tokens; Koine counts them apart.
type oaUsage struct {
	Prompt  int `json:"prompt_tokens"`
	Output  int `json:"completion_tokens"`
	Details struct {
		Cached int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u oaUsage) usage() Usage {
	if u.Prompt == 0 && u.Output == 0 {
		return Usage{}
	}
	return Usage{Input: max(u.Prompt-u.Details.Cached, 0), Output: u.Output, CacheRead: u.Details.Cached}
}

// oaToolInput is the arguments of a call: some local models emit broken JSON, which is surfaced to the model as an empty call.
func oaToolInput(args string) json.RawMessage {
	in := json.RawMessage(args)
	if !json.Valid(in) {
		return json.RawMessage("{}")
	}
	return in
}

// parseOpenAI reads a complete (not streamed) Chat Completions answer.
func parseOpenAI(label string, data []byte) (*Response, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content          *string      `json:"content"`
				ReasoningContent string       `json:"reasoning_content"` // DeepSeek, Mistral, vLLM and others
				Reasoning        string       `json:"reasoning"`         // OpenRouter, Ollama
				ToolCalls        []oaToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage oaUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, errors.New(label + ": response had no choices")
	}
	ch := out.Choices[0]
	resp := &Response{StopReason: ch.FinishReason, Usage: out.Usage.usage()}
	if r := cmp.Or(ch.Message.ReasoningContent, ch.Message.Reasoning); r != "" {
		resp.Content = append(resp.Content, Block{Type: "thinking", Thinking: r}) // no signature: shown and kept, never sent back
	}
	if ch.Message.Content != nil && *ch.Message.Content != "" {
		resp.Content = append(resp.Content, Block{Type: "text", Text: *ch.Message.Content})
	}
	for _, c := range ch.Message.ToolCalls {
		resp.Content = append(resp.Content, Block{Type: "tool_use", ID: c.ID, Name: c.Function.Name, Input: oaToolInput(c.Function.Arguments)})
	}
	return resp, nil
}

// openAIEffort is the reasoning_effort for a resolved Thinking. "off" sends nothing, and "max" is OpenAI's "xhigh".
func openAIEffort(t *Thinking) string {
	switch {
	case t == nil || t.Level == "off":
		return ""
	case t.Level == "max":
		return "xhigh"
	}
	return t.Level
}
