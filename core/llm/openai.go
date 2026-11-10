package llm

import (
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
	HTTP    *http.Client
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
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

// toOpenAI converts provider-neutral messages: tool_use blocks become tool_calls
// on the assistant message, and each tool_result becomes its own "tool" message.
func toOpenAI(system string, msgs []Message) []oaMessage {
	var out []oaMessage
	if system != "" {
		out = append(out, oaMessage{Role: "system", Content: sp(system)})
	}
	for _, m := range msgs {
		var text []string
		var calls []oaToolCall
		var results []oaMessage
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					text = append(text, b.Text)
				}
			case "tool_use":
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
				results = append(results, oaMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: sp(content)})
			}
		}
		out = append(out, results...)
		if len(text) == 0 && len(calls) == 0 {
			continue
		}
		msg := oaMessage{Role: m.Role, ToolCalls: calls}
		if len(text) > 0 {
			msg.Content = sp(strings.Join(text, "\n"))
		}
		out = append(out, msg)
	}
	return out
}

func (o *OpenAICompat) Complete(ctx context.Context, req Request) (*Response, error) {
	body := map[string]any{"model": req.Model, "messages": toOpenAI(req.System, req.Messages)}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.InputSchema}})
		}
		body["tools"] = tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if o.APIKey != "" {
		headers["authorization"] = "Bearer " + o.APIKey
	}
	label := o.Name
	if label == "" {
		label = "openai"
	}
	data, err := postJSON(ctx, o.HTTP, label, strings.TrimRight(o.BaseURL, "/")+"/chat/completions", headers, payload)
	if err != nil {
		return nil, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   *string      `json:"content"`
				ToolCalls []oaToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, errors.New(label + ": response had no choices")
	}
	ch := out.Choices[0]
	resp := &Response{StopReason: ch.FinishReason}
	if ch.Message.Content != nil && *ch.Message.Content != "" {
		resp.Content = append(resp.Content, Block{Type: "text", Text: *ch.Message.Content})
	}
	for _, c := range ch.Message.ToolCalls {
		in := json.RawMessage(c.Function.Arguments)
		if !json.Valid(in) { // some local models emit broken JSON; surface it to the model, don't crash
			in = json.RawMessage("{}")
		}
		resp.Content = append(resp.Content, Block{Type: "tool_use", ID: c.ID, Name: c.Function.Name, Input: in})
	}
	return resp, nil
}
