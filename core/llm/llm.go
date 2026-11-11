// Package llm holds provider-neutral message types and the Anthropic and OpenAI-compatible providers.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Block is one piece of a message. Type is text, image, thinking, tool_use or tool_result.
type Block struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// A thinking block carries the model's reasoning. Signature is the provider's replay token and must be sent back unchanged;
	// Redacted marks a block whose reasoning is hidden and whose encrypted payload is in Signature.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Redacted  bool   `json:"-"`
	Data      string `json:"data,omitempty"` // the wire name of a redacted block's payload; never set on blocks in the history

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// An image block carries its base64 data in Source, in Anthropic's wire shape. A tool_result can carry images next to its text (Images).
	Source *ImageSource `json:"source,omitempty"`
	Images []Block      `json:"-"`

	CacheControl *CacheControl `json:"cache_control,omitempty"` // set by the Anthropic provider on the blocks it marks for caching
}

// CacheControl marks a prompt-caching breakpoint.
type CacheControl struct {
	Type string `json:"type"`
}

type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
	// Usage is set on assistant messages by the agent (never sent to a provider); sessions store it.
	Usage *Usage `json:"-"`
	Cost  *Cost  `json:"-"` // dollars, when the model's price was known
	// ThinkingLevel is the level the response was requested with, on assistant messages (sessions store it).
	ThinkingLevel string `json:"-"`
}

type ToolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
	Thinking  *Thinking // nil = send no thinking parameters
}

type Response struct {
	Content    []Block
	StopReason string
	Usage      Usage // zero when the provider reported none
}

type Provider interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

// Missing is a provider that always errors with a helpful message, so the
// frontends can start (and extensions load) without credentials.
type Missing struct{ Reason string }

func (m Missing) Complete(context.Context, Request) (*Response, error) {
	return nil, errors.New(m.Reason)
}
