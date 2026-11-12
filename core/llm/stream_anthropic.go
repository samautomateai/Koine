package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Stream implements Streamer with the Messages API's server-sent events.
func (a *Anthropic) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	url, headers, payload, err := a.request(req, true)
	if err != nil {
		return nil, err
	}
	st, err := openStream(ctx, a.HTTP, a.label(), url, headers, payload, a.Retry)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	if !st.sse() {
		data, err := st.readAll()
		if err != nil {
			return nil, err
		}
		return parseAnthropic(data)
	}
	return readAnthropicStream(st, fn)
}

// anthropicAcc builds a response from stream events.
type anthropicAcc struct {
	blocks []Block
	args   []strings.Builder // tool arguments by block index
	done   bool              // message_stop or a stop reason arrived
	stop   string
	usage  anthropicUsage
	fn     func(StreamEvent)
}

func (c *anthropicAcc) grow(i int) {
	for len(c.blocks) <= i {
		c.blocks = append(c.blocks, Block{})
		c.args = append(c.args, strings.Builder{})
	}
}

// partial is what survives an interrupted stream: the text and thinking received, never a half-written tool call.
func (c *anthropicAcc) partial() *Response {
	r := &Response{Usage: c.usage.usage()}
	for _, b := range c.blocks {
		if (b.Type == "text" && b.Text != "") || (b.Type == "thinking" && !b.Redacted && b.Thinking != "") {
			b.Signature = ""
			r.Content = append(r.Content, b)
		}
	}
	return r
}

type anthropicEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Data string `json:"data"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *anthropicAcc) event(data []byte) error {
	var e anthropicEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("anthropic: bad stream event: %w", err)
	}
	if e.Index < 0 || e.Index > 1024 {
		return fmt.Errorf("anthropic: stream event with block index %d", e.Index)
	}
	switch e.Type {
	case "error":
		return fmt.Errorf("anthropic: stream error: %s: %s", e.Error.Type, e.Error.Message)
	case "message_start":
		c.usage = e.Message.Usage
	case "content_block_start":
		c.grow(e.Index)
		cb := e.ContentBlock
		switch cb.Type {
		case "redacted_thinking":
			c.blocks[e.Index] = Block{Type: "thinking", Redacted: true, Signature: cb.Data}
		case "tool_use":
			c.blocks[e.Index] = Block{Type: "tool_use", ID: cb.ID, Name: cb.Name}
			c.fn(StreamEvent{Type: EvToolStart, ID: cb.ID, Name: cb.Name})
		default:
			c.blocks[e.Index] = Block{Type: cb.Type, Text: cb.Text}
		}
	case "content_block_delta":
		c.grow(e.Index)
		b := &c.blocks[e.Index]
		d := e.Delta
		switch d.Type {
		case "text_delta":
			b.Text += d.Text
			c.fn(StreamEvent{Type: EvTextDelta, Delta: d.Text})
		case "thinking_delta":
			b.Thinking += d.Thinking
			c.fn(StreamEvent{Type: EvThinkingDelta, Delta: d.Thinking})
		case "signature_delta":
			b.Signature += d.Signature
		case "input_json_delta":
			_, _ = c.args[e.Index].WriteString(d.PartialJSON)
			c.fn(StreamEvent{Type: EvToolDelta, ID: b.ID, Name: b.Name, Delta: d.PartialJSON})
		}
	case "content_block_stop":
		c.grow(e.Index)
		if b := &c.blocks[e.Index]; b.Type == "tool_use" {
			in := json.RawMessage(c.args[e.Index].String())
			if len(in) == 0 || !json.Valid(in) { // a call cut off by the token limit: let the model see an empty call, don't crash
				in = json.RawMessage("{}")
			}
			b.Input = in
		}
	case "message_delta":
		c.stop, c.done = e.Delta.StopReason, true
		if e.Usage.Output > 0 {
			c.usage.Output = e.Usage.Output
		}
	case "message_stop":
		c.done = true
	}
	return nil
}

// readAnthropicStream reads the events of one streamed Messages API response.
func readAnthropicStream(r io.Reader, fn func(StreamEvent)) (*Response, error) {
	c := &anthropicAcc{fn: fn}
	err := readSSE(r, c.event)
	if err == nil && !c.done {
		err = errors.New("anthropic: the stream ended before the message was complete")
	}
	if err != nil {
		return c.partial(), err
	}
	resp := &Response{StopReason: c.stop, Usage: c.usage.usage()}
	for _, b := range c.blocks {
		if b.Type != "" {
			resp.Content = append(resp.Content, b)
		}
	}
	return resp, nil
}
