package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Stream implements Streamer with ConverseStream, whose answer is a sequence of AWS event stream frames (eventstream.go).
func (b *Bedrock) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	u, headers, payload, err := b.request(req, true)
	if err != nil {
		return nil, err
	}
	headers["accept"] = "application/vnd.amazon.eventstream"
	st, err := openStream(ctx, b.HTTP, b.label(), u, headers, payload, b.Retry)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	if !strings.HasPrefix(strings.ToLower(st.resp.Header.Get("Content-Type")), "application/vnd.amazon.eventstream") {
		data, err := st.readAll() // a proxy may answer with the plain Converse JSON
		if err != nil {
			return nil, err
		}
		return parseBedrock(b.label(), data)
	}
	return readBedrockStream(b.label(), st, fn)
}

// bedrockAcc builds a response from ConverseStream events.
type bedrockAcc struct {
	label  string
	blocks []Block
	args   []strings.Builder // tool arguments by block index
	stop   string
	done   bool
	usage  bedrockUsage
	fn     func(StreamEvent)
}

func (c *bedrockAcc) grow(i int) {
	for len(c.blocks) <= i {
		c.blocks = append(c.blocks, Block{})
		c.args = append(c.args, strings.Builder{})
	}
}

// partial is what survives an interrupted stream: text and thinking, never a half-written tool call.
func (c *bedrockAcc) partial() *Response {
	r := &Response{Usage: c.usage.usage()}
	for _, b := range c.blocks {
		if (b.Type == "text" && b.Text != "") || (b.Type == "thinking" && !b.Redacted && b.Thinking != "") {
			b.Signature = ""
			r.Content = append(r.Content, b)
		}
	}
	return r
}

type bedrockEvent struct {
	Index int `json:"contentBlockIndex"`
	Start *struct {
		ToolUse *struct {
			ID   string `json:"toolUseId"`
			Name string `json:"name"`
		} `json:"toolUse"`
	} `json:"start"`
	Delta *struct {
		Text    string `json:"text"`
		ToolUse *struct {
			Input string `json:"input"`
		} `json:"toolUse"`
		Reasoning *struct {
			Text      string `json:"text"`
			Signature string `json:"signature"`
			Redacted  string `json:"redactedContent"`
		} `json:"reasoningContent"`
	} `json:"delta"`
	StopReason string       `json:"stopReason"`
	Usage      bedrockUsage `json:"usage"`
	Message    string       `json:"message"`
}

func (c *bedrockAcc) event(kind string, payload []byte) error {
	var e bedrockEvent
	if err := json.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("%s: bad stream event: %w", c.label, err)
	}
	if e.Index < 0 || e.Index > 1024 {
		return fmt.Errorf("%s: stream event with block index %d", c.label, e.Index)
	}
	switch kind {
	case "contentBlockStart":
		c.grow(e.Index)
		if e.Start != nil && e.Start.ToolUse != nil {
			t := e.Start.ToolUse
			c.blocks[e.Index] = Block{Type: "tool_use", ID: t.ID, Name: t.Name}
			c.fn(StreamEvent{Type: EvToolStart, ID: t.ID, Name: t.Name})
		}
	case "contentBlockDelta":
		c.grow(e.Index)
		b, d := &c.blocks[e.Index], e.Delta
		switch {
		case d == nil:
		case d.ToolUse != nil:
			_, _ = c.args[e.Index].WriteString(d.ToolUse.Input)
			c.fn(StreamEvent{Type: EvToolDelta, ID: b.ID, Name: b.Name, Delta: d.ToolUse.Input})
		case d.Reasoning != nil:
			r := d.Reasoning
			b.Type = "thinking"
			if r.Redacted != "" {
				b.Redacted, b.Signature = true, r.Redacted
			}
			b.Signature += r.Signature
			if r.Text != "" {
				b.Thinking += r.Text
				c.fn(StreamEvent{Type: EvThinkingDelta, Delta: r.Text})
			}
		case d.Text != "":
			b.Type = "text"
			b.Text += d.Text
			c.fn(StreamEvent{Type: EvTextDelta, Delta: d.Text})
		}
	case "contentBlockStop":
		c.grow(e.Index)
		if b := &c.blocks[e.Index]; b.Type == "tool_use" {
			in := json.RawMessage(c.args[e.Index].String())
			if len(in) == 0 || !json.Valid(in) { // a call cut off by the token limit: let the model see an empty call
				in = json.RawMessage("{}")
			}
			b.Input = in
		}
	case "messageStop":
		c.stop, c.done = e.StopReason, true
	case "metadata":
		c.usage = e.Usage
	}
	return nil
}

// readBedrockStream reads the frames of one ConverseStream answer. An exception frame (throttling, validation, the model failing midway)
// ends it with an error that names the exception.
func readBedrockStream(label string, r io.Reader, fn func(StreamEvent)) (*Response, error) {
	if fn == nil {
		fn = func(StreamEvent) {}
	}
	c := &bedrockAcc{label: label, fn: fn}
	var err error
	for err == nil {
		var m *eventMessage
		if m, err = readEventMessage(r); err != nil {
			break
		}
		if m.Headers[":message-type"] == "exception" || m.Headers[":exception-type"] != "" {
			msg := ErrorMessage(string(m.Payload))
			err = fmt.Errorf("%s: %s: %s", label, m.Headers[":exception-type"], msg)
			break
		}
		err = c.event(m.Headers[":event-type"], m.Payload)
	}
	if errors.Is(err, io.EOF) {
		err = nil
		if !c.done {
			err = fmt.Errorf("%s: the stream ended before the message was complete", label)
		}
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
