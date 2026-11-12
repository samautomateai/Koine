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

	// ThoughtSignature is Gemini's replay token on a text or tool_use block (Pi: textSignature on text, thoughtSignature on a tool call). It
	// is never sent to other APIs, so it has no JSON name here.
	ThoughtSignature string `json:"-"`

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
	TTL  string `json:"ttl,omitempty"` // "1h" for long retention; empty is the five-minute default
}

// CacheHints are the per-request inputs of provider-side prompt caching.
type CacheHints struct {
	// Key identifies the conversation to providers that route cache hits by it (OpenAI's prompt_cache_key); the session id.
	Key string
	// Long asks for the provider's extended cache lifetime (Anthropic's one-hour ttl, OpenAI's 24 hours). Providers that have none ignore it.
	Long bool
}

type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
	// Usage is set on assistant messages by the agent (never sent to a provider); sessions store it.
	Usage *Usage `json:"-"`
	Cost  *Cost  `json:"-"` // dollars, when the model's price was known
	// ThinkingLevel is the level the response was requested with, on assistant messages (sessions store it).
	ThinkingLevel string `json:"-"`
	// StopReason is "aborted" on an assistant message that was cut short by the user. Such a message is kept and saved but never sent
	// to a provider.
	StopReason string `json:"-"`
	// Origin names the model that wrote an assistant message, as the user selected it ("provider/model" or a bare model id). It is empty
	// when unknown. TransformMessages uses it to see which thinking blocks the next model cannot replay.
	Origin string `json:"-"`
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
	Cache     CacheHints
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
