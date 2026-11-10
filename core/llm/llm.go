// Package llm holds provider-neutral message types and the Anthropic provider.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Block struct {
	Type string `json:"type"` // text | tool_use | tool_result
	Text string `json:"text,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
}

type Response struct {
	Content    []Block
	StopReason string
}

type Provider interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

// Anthropic talks to the Messages API.
type Anthropic struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
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

func (a *Anthropic) Complete(ctx context.Context, req Request) (*Response, error) {
	max := req.MaxTokens
	if max == 0 {
		max = 8192
	}
	msgs := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		var bs []Block
		for _, b := range m.Content {
			if b.Type == "text" && b.Text == "" {
				continue
			}
			if b.Type == "tool_use" && len(b.Input) == 0 {
				b.Input = json.RawMessage("{}")
			}
			bs = append(bs, b)
		}
		if len(bs) > 0 {
			msgs = append(msgs, Message{Role: m.Role, Content: bs})
		}
	}
	body := map[string]any{"model": req.Model, "max_tokens": max, "messages": msgs}
	if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	data, err := postJSON(ctx, a.HTTP, "anthropic", a.BaseURL+"/v1/messages", map[string]string{
		"x-api-key": a.APIKey, "anthropic-version": "2023-06-01",
	}, payload)
	if err != nil {
		return nil, err
	}
	var out struct {
		Content    []Block `json:"content"`
		StopReason string  `json:"stop_reason"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &Response{Content: out.Content, StopReason: out.StopReason}, nil
}

// postJSON POSTs payload and returns the body of a 200 response. Network errors,
// 429 and 5xx are retried (up to 4 attempts, quadratic backoff); other statuses fail at once.
func postJSON(ctx context.Context, client *http.Client, label, url string, headers map[string]string, payload []byte) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * time.Second):
			}
		}
		hr, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		hr.Header.Set("content-type", "application/json")
		for k, v := range headers {
			hr.Header.Set(k, v)
		}
		resp, err := client.Do(hr)
		if err != nil {
			lastErr = err
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s: HTTP %d: %s", label, resp.StatusCode, truncate(string(data), 300))
			continue
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("%s: HTTP %d: %s", label, resp.StatusCode, truncate(string(data), 500))
		}
		return data, nil
	}
	return nil, lastErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Missing is a provider that always errors with a helpful message, so the
// frontends can start (and extensions load) without credentials.
type Missing struct{ Reason string }

func (m Missing) Complete(context.Context, Request) (*Response, error) {
	return nil, errors.New(m.Reason)
}
