package llm

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Stream implements Streamer with Chat Completions' server-sent events.
func (o *OpenAICompat) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	url, headers, payload, err := o.request(req, true)
	if err != nil {
		return nil, err
	}
	st, err := openStream(ctx, o.HTTP, o.label(), url, headers, payload, o.Retry)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	if !st.sse() {
		data, err := st.readAll()
		if err != nil {
			return nil, err
		}
		return parseOpenAI(o.label(), data)
	}
	return readOpenAIStream(o.label(), st, fn)
}

type oaCallAcc struct {
	id, name string
	args     strings.Builder
}

// openAIAcc builds a response from Chat Completions chunks.
type openAIAcc struct {
	label    string
	text     strings.Builder
	thinking strings.Builder
	calls    []*oaCallAcc // by the chunk's tool call index
	stop     string
	usage    oaUsage
	done     bool // [DONE] or a finish reason arrived
	fn       func(StreamEvent)
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaUsage        `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (c *openAIAcc) chunk(data []byte) error {
	if string(data) == "[DONE]" {
		c.done = true
		return nil
	}
	var k oaChunk
	if err := json.Unmarshal(data, &k); err != nil {
		return fmt.Errorf("%s: bad stream chunk: %w", c.label, err)
	}
	if len(k.Error) > 0 && string(k.Error) != "null" {
		return fmt.Errorf("%s: stream error: %s", c.label, truncate(string(k.Error), 500))
	}
	if k.Usage != nil {
		c.usage = *k.Usage
	}
	if len(k.Choices) == 0 {
		return nil
	}
	ch := k.Choices[0]
	d := ch.Delta
	if r := cmp.Or(d.ReasoningContent, d.Reasoning); r != "" {
		c.thinking.WriteString(r)
		c.fn(StreamEvent{Type: EvThinkingDelta, Delta: r})
	}
	if d.Content != "" {
		c.text.WriteString(d.Content)
		c.fn(StreamEvent{Type: EvTextDelta, Delta: d.Content})
	}
	for n, tc := range d.ToolCalls {
		i := n
		if tc.Index != nil {
			i = *tc.Index
		}
		if i < 0 || i > 1024 {
			return fmt.Errorf("%s: stream chunk with tool call index %d", c.label, i)
		}
		for len(c.calls) <= i {
			c.calls = append(c.calls, &oaCallAcc{})
		}
		call := c.calls[i]
		started := call.name == "" && tc.Function.Name != ""
		call.id, call.name = cmp.Or(tc.ID, call.id), cmp.Or(call.name, tc.Function.Name)
		if call.id == "" && call.name != "" {
			call.id = fmt.Sprintf("call_%d", i)
		}
		if started {
			c.fn(StreamEvent{Type: EvToolStart, ID: call.id, Name: call.name})
		}
		if tc.Function.Arguments != "" {
			call.args.WriteString(tc.Function.Arguments)
			c.fn(StreamEvent{Type: EvToolDelta, ID: call.id, Name: call.name, Delta: tc.Function.Arguments})
		}
	}
	if ch.FinishReason != "" {
		c.stop, c.done = ch.FinishReason, true
	}
	return nil
}

func (c *openAIAcc) response(complete bool) *Response {
	resp := &Response{StopReason: c.stop, Usage: c.usage.usage()}
	if c.thinking.Len() > 0 {
		resp.Content = append(resp.Content, Block{Type: "thinking", Thinking: c.thinking.String()})
	}
	if c.text.Len() > 0 {
		resp.Content = append(resp.Content, Block{Type: "text", Text: c.text.String()})
	}
	for _, call := range c.calls {
		if complete && call.name != "" {
			args := call.args.String()
			if args == "" {
				args = "{}"
			}
			resp.Content = append(resp.Content, Block{Type: "tool_use", ID: call.id, Name: call.name, Input: oaToolInput(args)})
		}
	}
	return resp
}

// readOpenAIStream reads the chunks of one streamed Chat Completions response.
func readOpenAIStream(label string, r io.Reader, fn func(StreamEvent)) (*Response, error) {
	c := &openAIAcc{label: label, fn: fn}
	err := readSSE(r, c.chunk)
	if err == nil && !c.done {
		err = errors.New(label + ": the stream ended before the message was complete")
	}
	if err != nil {
		return c.response(false), err
	}
	return c.response(true), nil
}
