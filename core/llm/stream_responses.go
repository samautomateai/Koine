package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Stream implements Streamer with the Responses API's server-sent events.
func (p *Responses) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	u, headers, payload, err := p.request(req, true)
	if err != nil {
		return nil, err
	}
	st, err := openStream(ctx, p.HTTP, p.label(), u, headers, payload, p.Retry)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	if !st.sse() {
		data, err := st.readAll()
		if err != nil {
			return nil, err
		}
		return parseResponses(p.label(), data)
	}
	return readResponsesStream(p.label(), st, fn)
}

// respEvent is the part of a stream event Koine reads.
type respEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	ItemID   string          `json:"item_id"`
	Item     respItem        `json:"item"`
	Response json.RawMessage `json:"response"`
	Message  string          `json:"message"`
	Code     string          `json:"code"`
}

// responsesAcc follows a stream. Increments go to the callback as they come; the final response is the one the API sends in
// response.completed (or .incomplete), which carries every item and the usage, so nothing is assembled twice.
type responsesAcc struct {
	label    string
	text     strings.Builder
	thinking strings.Builder
	callIDs  map[string]string // output item id -> call id
	final    *Response
	fn       func(StreamEvent)
}

func (c *responsesAcc) event(data []byte) error {
	var e respEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("%s: bad stream event: %w", c.label, err)
	}
	switch e.Type {
	case "response.output_text.delta":
		c.text.WriteString(e.Delta)
		c.fn(StreamEvent{Type: EvTextDelta, Delta: e.Delta})
	case "response.reasoning_summary_text.delta":
		c.thinking.WriteString(e.Delta)
		c.fn(StreamEvent{Type: EvThinkingDelta, Delta: e.Delta})
	case "response.reasoning_summary_part.added":
		if c.thinking.Len() > 0 {
			c.thinking.WriteString("\n\n")
			c.fn(StreamEvent{Type: EvThinkingDelta, Delta: "\n\n"})
		}
	case "response.output_item.added":
		if e.Item.Type == "function_call" {
			id := e.Item.CallID
			if id == "" {
				id = e.Item.ID
			}
			c.callIDs[e.Item.ID] = id
			c.fn(StreamEvent{Type: EvToolStart, ID: id, Name: e.Item.Name})
		}
	case "response.function_call_arguments.delta":
		c.fn(StreamEvent{Type: EvToolDelta, ID: c.callIDs[e.ItemID], Delta: e.Delta})
	case "response.completed", "response.incomplete":
		resp, err := parseResponses(c.label, e.Response)
		if err != nil {
			return err
		}
		c.final = resp
	case "response.failed":
		if _, err := parseResponses(c.label, e.Response); err != nil {
			return err
		}
		return errors.New(c.label + ": the response failed")
	case "error":
		return fmt.Errorf("%s: stream error: %s", c.label, strings.TrimSpace(e.Code+" "+e.Message))
	}
	return nil
}

// partial is what survives an interrupted stream: the text and thinking received.
func (c *responsesAcc) partial() *Response {
	r := &Response{}
	if c.thinking.Len() > 0 {
		r.Content = append(r.Content, Block{Type: "thinking", Thinking: c.thinking.String()})
	}
	if c.text.Len() > 0 {
		r.Content = append(r.Content, Block{Type: "text", Text: c.text.String()})
	}
	return r
}

// readResponsesStream reads the events of one streamed Responses API answer.
func readResponsesStream(label string, r io.Reader, fn func(StreamEvent)) (*Response, error) {
	c := &responsesAcc{label: label, fn: fn, callIDs: map[string]string{}}
	err := readSSE(r, func(data []byte) error {
		if string(data) == "[DONE]" {
			return nil
		}
		return c.event(data)
	})
	if err == nil && c.final == nil {
		err = errors.New(label + ": the stream ended before the response was complete")
	}
	if err != nil {
		return c.partial(), err
	}
	return c.final, nil
}
