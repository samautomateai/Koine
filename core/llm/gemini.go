package llm

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Gemini speaks Google's Gemini API (generativelanguage.googleapis.com, `google-generative-ai` in models.json) with an API key. Vertex AI
// (`google-vertex`) is the same protocol at another address, with an API key or a Google access token: set ModelPath and Token (vertex.go).
//
// Thought signatures, which newer Gemini models want echoed back, are kept on the blocks they arrive with (thinking, text and function
// calls) and sent back unchanged by the same model; Gemini 3 models get Google's documented placeholder on a call that has none. The wire
// format was written from Google's documentation and is tested against fake servers only.
type Gemini struct {
	Name    string // for error messages; "google" when empty
	APIKey  string
	Bearer  bool // send the key as "Authorization: Bearer" (authHeader in models.json) instead of x-goog-api-key
	BaseURL string
	// ModelPath is what comes before the model id in the URL: "models/" (default) or Vertex's "projects/…/publishers/google/models/".
	ModelPath string
	// Token, when set, supplies a bearer token for each request (Vertex AI access tokens); it replaces the API key.
	Token   func(ctx context.Context) (string, error)
	Headers map[string]string
	HTTP    *http.Client
	Retry   RetryPolicy
}

func (g *Gemini) label() string {
	if g.Name == "" {
		return "google"
	}
	return g.Name
}

func newCallID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}

// geminiBudget is the thinking budget for a level: the explicit budget, else Koine's per-level default (24576 is the most every Gemini
// 2.5 model accepts).
func geminiBudget(t *Thinking) int {
	if t.Budget > 0 {
		return t.Budget
	}
	if b, ok := defaultBudgets[t.Level]; ok {
		return b
	}
	return 24576
}

// toGeminiContents converts messages to Gemini's contents. A function response must name its function, which the history only says in
// the call, so names are collected from the tool_use blocks. Thinking is sent back only when it carries a signature. model decides the
// placeholder signature for Gemini 3 (see geminiSkipSignature).
func toGeminiContents(msgs []Message, model string) []map[string]any {
	var out []map[string]any
	names := map[string]string{}
	for _, m := range msgs {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		var fromTools []Block
		firstCall := true
		for _, b := range m.Content {
			switch b.Type {
			case "thinking":
				if b.Thinking != "" && validThoughtSignature(b.Signature) {
					parts = append(parts, map[string]any{"text": b.Thinking, "thought": true, "thoughtSignature": b.Signature})
				}
			case "text":
				if b.Text != "" {
					part := map[string]any{"text": b.Text}
					if validThoughtSignature(b.ThoughtSignature) {
						part["thoughtSignature"] = b.ThoughtSignature
					}
					parts = append(parts, part)
				}
			case "image":
				parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": b.MimeType(), "data": b.Base64()}})
			case "tool_use":
				names[b.ID] = b.Name
				args := json.RawMessage(orEmptyObject(string(b.Input)))
				part := map[string]any{"functionCall": map[string]any{"name": b.Name, "args": args}}
				switch {
				case validThoughtSignature(b.ThoughtSignature):
					part["thoughtSignature"] = b.ThoughtSignature
				case firstCall && isGemini3(model):
					part["thoughtSignature"] = geminiSkipSignature
				}
				firstCall = false
				parts = append(parts, part)
			case "tool_result":
				resp := map[string]any{"output": b.Content}
				if b.IsError {
					resp = map[string]any{"error": b.Content}
				}
				name := names[b.ToolUseID]
				if name == "" {
					name = "tool"
				}
				parts = append(parts, map[string]any{"functionResponse": map[string]any{"name": name, "response": resp}})
				fromTools = append(fromTools, b.Images...)
			}
		}
		if len(parts) > 0 {
			out = append(out, map[string]any{"role": role, "parts": parts})
		}
		if len(fromTools) > 0 {
			ps := []map[string]any{{"text": "Attached image(s) from tool result:"}}
			for _, im := range fromTools {
				ps = append(ps, map[string]any{"inlineData": map[string]any{"mimeType": im.MimeType(), "data": im.Base64()}})
			}
			out = append(out, map[string]any{"role": "user", "parts": ps})
		}
	}
	return out
}

// geminiSkipSignature is the placeholder Google documents for a function call whose real signature is not available (history from another
// model or an older session); Gemini 3 refuses a step's first call without a signature, and this makes it skip the check.
const geminiSkipSignature = "skip_thought_signature_validator"

func isGemini3(model string) bool { return strings.Contains(strings.ToLower(model), "gemini-3") }

// validThoughtSignature accepts what Gemini signatures look like (base64). Another provider's token, which can sit in the same field of a
// session file, is refused rather than sent.
func validThoughtSignature(s string) bool {
	if s == "" {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}

func (g *Gemini) request(ctx context.Context, req Request, stream bool) (string, map[string]string, []byte, error) {
	body := map[string]any{"contents": toGeminiContents(req.Messages, req.Model)}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": req.System}}}
	}
	gen := map[string]any{}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if t := req.Thinking; t != nil && t.Level != "" {
		if t.Level == "off" {
			gen["thinkingConfig"] = map[string]any{"thinkingBudget": 0}
		} else {
			gen["thinkingConfig"] = map[string]any{"includeThoughts": true, "thinkingBudget": geminiBudget(t)}
		}
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	if len(req.Tools) > 0 {
		var decls []map[string]any
		for _, t := range req.Tools { // parametersJsonSchema takes the JSON Schema as it is; "parameters" wants Google's OpenAPI subset
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parametersJsonSchema": t.InputSchema})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	payload, err := json.Marshal(body)
	headers := map[string]string{}
	for k, v := range g.Headers {
		headers[k] = v
	}
	switch {
	case g.Token != nil:
		tok, err := g.Token(ctx)
		if err != nil {
			return "", nil, nil, fmt.Errorf("%s: %w", g.label(), err)
		}
		headers["authorization"] = "Bearer " + tok
	case g.APIKey == "":
	case g.Bearer:
		headers["authorization"] = "Bearer " + g.APIKey
	default:
		headers["x-goog-api-key"] = g.APIKey
	}
	prefix := g.ModelPath
	if prefix == "" {
		prefix = "models/"
	}
	u := strings.TrimRight(g.BaseURL, "/") + "/" + prefix + strings.TrimPrefix(req.Model, "models/")
	if stream {
		u += ":streamGenerateContent?alt=sse"
	} else {
		u += ":generateContent"
	}
	return u, headers, payload, err
}

// Complete implements Provider.
func (g *Gemini) Complete(ctx context.Context, req Request) (*Response, error) {
	u, headers, payload, err := g.request(ctx, req, false)
	if err != nil {
		return nil, err
	}
	data, err := postJSON(ctx, g.HTTP, g.label(), u, headers, payload, g.Retry)
	if err != nil {
		return nil, err
	}
	return parseGemini(g.label(), data)
}

// Stream implements Streamer with server-sent events (alt=sse). Each event is a response chunk holding the next parts.
func (g *Gemini) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	u, headers, payload, err := g.request(ctx, req, true)
	if err != nil {
		return nil, err
	}
	st, err := openStream(ctx, g.HTTP, g.label(), u, headers, payload, g.Retry)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	if !st.sse() {
		data, err := st.readAll()
		if err != nil {
			return nil, err
		}
		return parseGemini(g.label(), data)
	}
	return readGeminiStream(g.label(), st, fn)
}

type gemChunk struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text             string `json:"text"`
				Thought          bool   `json:"thought"`
				ThoughtSignature string `json:"thoughtSignature"`
				FunctionCall     *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		Prompt     int `json:"promptTokenCount"`
		Candidates int `json:"candidatesTokenCount"`
		Thoughts   int `json:"thoughtsTokenCount"`
		Cached     int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// geminiAcc builds a response from chunks.
type geminiAcc struct {
	label   string
	blocks  []Block
	usage   Usage
	stop    string
	blocked string
	calls   bool
	fn      func(StreamEvent)
}

func (c *geminiAcc) add(k gemChunk) error {
	if k.Error != nil {
		return fmt.Errorf("%s: %s", c.label, strings.TrimSpace(k.Error.Status+" "+k.Error.Message))
	}
	if k.PromptFeedback != nil && k.PromptFeedback.BlockReason != "" {
		c.blocked = k.PromptFeedback.BlockReason
	}
	if u := k.UsageMetadata; u != nil {
		c.usage = Usage{Input: max(u.Prompt-u.Cached, 0), Output: u.Candidates + u.Thoughts, CacheRead: u.Cached}
	}
	if len(k.Candidates) == 0 {
		return nil
	}
	cand := k.Candidates[0]
	if cand.FinishReason != "" {
		c.stop = cand.FinishReason
	}
	for _, p := range cand.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			fc := p.FunctionCall
			id := fc.ID
			if id == "" {
				id = newCallID()
			}
			args := orEmptyObject(string(fc.Args))
			c.blocks = append(c.blocks, Block{Type: "tool_use", ID: id, Name: fc.Name, Input: oaToolInput(args), ThoughtSignature: p.ThoughtSignature})
			c.calls = true
			c.fn(StreamEvent{Type: EvToolStart, ID: id, Name: fc.Name})
			c.fn(StreamEvent{Type: EvToolDelta, ID: id, Name: fc.Name, Delta: args})
		case p.Text == "" && p.ThoughtSignature != "":
			c.signLast(p.ThoughtSignature) // a signature that arrives alone closes the part before it
		case p.Text != "":
			c.addText(p.Text, p.Thought, p.ThoughtSignature)
		}
	}
	return nil
}

// addText appends a text or thought part. Parts are merged into the block before them unless that block is signed, because a signature
// belongs to the exact text it came with.
func (c *geminiAcc) addText(text string, thought bool, sig string) {
	typ, ev := "text", EvTextDelta
	if thought {
		typ, ev = "thinking", EvThinkingDelta
	}
	n := len(c.blocks)
	if n > 0 && c.blocks[n-1].Type == typ && c.blocks[n-1].Signature == "" && c.blocks[n-1].ThoughtSignature == "" {
		if thought {
			c.blocks[n-1].Thinking += text
		} else {
			c.blocks[n-1].Text += text
		}
	} else if thought {
		c.blocks = append(c.blocks, Block{Type: "thinking", Thinking: text})
	} else {
		c.blocks = append(c.blocks, Block{Type: "text", Text: text})
	}
	if sig != "" {
		c.signLast(sig)
	}
	c.fn(StreamEvent{Type: ev, Delta: text})
}

// signLast attaches a signature to the last text or thinking block that has none; a call or nothing at all drops it.
func (c *geminiAcc) signLast(sig string) {
	n := len(c.blocks)
	if n == 0 {
		return
	}
	switch b := &c.blocks[n-1]; {
	case b.Type == "thinking" && b.Signature == "":
		b.Signature = sig
	case b.Type == "text" && b.ThoughtSignature == "":
		b.ThoughtSignature = sig
	}
}

func (c *geminiAcc) response() (*Response, error) {
	if len(c.blocks) == 0 && c.blocked != "" {
		return nil, fmt.Errorf("%s: the prompt was blocked (%s)", c.label, c.blocked)
	}
	stop := strings.ToLower(c.stop)
	switch {
	case c.calls:
		stop = "tool_calls"
	case c.stop == "STOP" || c.stop == "":
		stop = "stop"
	case c.stop == "MAX_TOKENS":
		stop = "length"
	}
	return &Response{Content: c.blocks, StopReason: stop, Usage: c.usage}, nil
}

// partial is what survives an interrupted stream: text and thinking, never a tool call.
func (c *geminiAcc) partial() *Response {
	r := &Response{Usage: c.usage}
	for _, b := range c.blocks {
		if b.Type != "tool_use" {
			r.Content = append(r.Content, b)
		}
	}
	return r
}

// parseGemini reads a complete answer. A streaming request that the server answered without SSE gets a JSON array of chunks, which is
// read the same way.
func parseGemini(label string, data []byte) (*Response, error) {
	c := &geminiAcc{label: label, fn: func(StreamEvent) {}}
	var chunks []gemChunk
	if t := strings.TrimSpace(string(data)); strings.HasPrefix(t, "[") {
		if err := json.Unmarshal(data, &chunks); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
	} else {
		var k gemChunk
		if err := json.Unmarshal(data, &k); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		chunks = []gemChunk{k}
	}
	for _, k := range chunks {
		if err := c.add(k); err != nil {
			return nil, err
		}
	}
	return c.response()
}

// readGeminiStream reads the chunks of one streamed answer.
func readGeminiStream(label string, r io.Reader, fn func(StreamEvent)) (*Response, error) {
	c := &geminiAcc{label: label, fn: fn}
	seen := false
	err := readSSE(r, func(data []byte) error {
		var k gemChunk
		if err := json.Unmarshal(data, &k); err != nil {
			return fmt.Errorf("%s: bad stream chunk: %w", label, err)
		}
		seen = true
		return c.add(k)
	})
	if err == nil && !seen {
		err = errors.New(label + ": the stream held no data")
	}
	if err != nil {
		return c.partial(), err
	}
	return c.response()
}
