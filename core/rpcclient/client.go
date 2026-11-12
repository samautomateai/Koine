// Package rpcclient is the client side of `koine --mode rpc` (REQ-RPC-001, SPEC section 12). Frontends that are separate Go modules (the
// Bubble Tea terminal UI and the Wails desktop app, REQ-FE-002, REQ-FE-003) use it to drive the agent as a child process instead of importing
// the core's packages, so they depend only on the wire protocol. It imports nothing from Koine and nothing outside the standard library.
package rpcclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Event is one line the server wrote that is not the answer to a command: an agent event, an extension_ui_request or, from Start, a
// synthetic "stderr" record that carries one line of the server's standard error (notices and warnings go there).
type Event struct {
	Type string
	Raw  json.RawMessage
}

// Decode unmarshals the whole record into v.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Raw, v) }

// CommandError is a command the server answered with success false.
type CommandError struct{ Command, Message string }

func (e *CommandError) Error() string { return e.Command + ": " + e.Message }

// ErrClosed is returned by commands sent after the server's output ended or the client was closed.
var ErrClosed = errors.New("rpc: connection closed")

type response struct {
	ID      json.RawMessage `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// Client sends commands and receives events over one pair of streams. Methods are safe for concurrent use. The consumer must drain Events:
// the reader hands responses and events over in the order they were written, so a full Events channel holds back the next response.
type Client struct {
	w      io.WriteCloser
	wmu    sync.Mutex
	mu     sync.Mutex
	seq    int
	wait   map[string]chan response
	events chan Event
	done   chan struct{}
	stop   chan struct{}
	once   sync.Once
	extra  sync.WaitGroup // other writers to events (Start's stderr reader); events closes after them
	err    error          // why the reader ended; read after done is closed
}

// NewClient starts reading r (the server's standard output) and writes commands to w (its standard input).
func NewClient(r io.Reader, w io.WriteCloser) *Client { return newClient(r, w, 0) }

// newClient also reserves extra event writers that the caller will Done, before the reader can finish.
func newClient(r io.Reader, w io.WriteCloser, extra int) *Client {
	c := &Client{w: w, wait: map[string]chan response{}, events: make(chan Event, 256), done: make(chan struct{}), stop: make(chan struct{})}
	c.extra.Add(extra)
	go c.read(r)
	return c
}

// Events delivers the server's events until its output ends, then closes.
func (c *Client) Events() <-chan Event { return c.events }

// Err says why the connection ended; it is nil while the connection is open and for a clean end of input.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Done is closed when the server's output has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) read(r io.Reader) {
	defer func() {
		close(c.done)
		c.extra.Wait()
		close(c.events)
		c.mu.Lock()
		for id, ch := range c.wait {
			close(ch)
			delete(c.wait, id)
		}
		c.mu.Unlock()
	}()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n') // strict JSONL, like the server: records end at LF only
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.err = err
			}
			return
		}
	}
}

func (c *Client) dispatch(line []byte) {
	var head struct{ Type string }
	if json.Unmarshal(line, &head) != nil || head.Type == "" {
		return // not a record; the server writes only records to stdout
	}
	if head.Type == "response" {
		var r response
		if json.Unmarshal(line, &r) == nil {
			var id string
			if json.Unmarshal(r.ID, &id) == nil && id != "" {
				c.mu.Lock()
				ch, ok := c.wait[id]
				delete(c.wait, id)
				c.mu.Unlock()
				if ok {
					ch <- r // buffered, never blocks
					return
				}
			}
		}
	}
	// events, and responses nobody waits for (a parse failure has no id)
	c.emit(Event{Type: head.Type, Raw: append(json.RawMessage(nil), line...)})
}

func (c *Client) emit(e Event) {
	select {
	case c.events <- e:
	case <-c.stop:
	}
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	_, err = c.w.Write(append(b, '\n'))
	return err
}

// Do sends a command and waits for its response, returning the response's data. A command the server refused is a *CommandError.
func (c *Client) Do(ctx context.Context, typ string, fields map[string]any) (json.RawMessage, error) {
	msg := map[string]any{}
	for k, v := range fields {
		msg[k] = v
	}
	msg["type"] = typ
	c.mu.Lock()
	c.seq++
	id := "c" + strconv.Itoa(c.seq)
	ch := make(chan response, 1)
	c.wait[id] = ch
	c.mu.Unlock()
	msg["id"] = id
	if err := c.write(msg); err != nil {
		c.forget(id)
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		if !r.Success {
			return nil, &CommandError{Command: typ, Message: r.Error}
		}
		return r.Data, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.wait, id)
	c.mu.Unlock()
}

// Prompt sends a user message. streamingBehavior ("steer" or "followUp") is needed only when the agent is already running. The result is
// the disposition: started, queued or handled (an extension command ran).
func (c *Client) Prompt(ctx context.Context, message, streamingBehavior string) (string, error) {
	f := map[string]any{"message": message}
	if streamingBehavior != "" {
		f["streamingBehavior"] = streamingBehavior
	}
	data, err := c.Do(ctx, "prompt", f)
	if err != nil {
		return "", err
	}
	var d struct{ Disposition string }
	if err := json.Unmarshal(data, &d); err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	return d.Disposition, nil
}

// Steer queues a message for delivery after the current turn's tool calls.
func (c *Client) Steer(ctx context.Context, message string) error {
	_, err := c.Do(ctx, "steer", map[string]any{"message": message})
	return err
}

// FollowUp queues a message for when the agent has no more work.
func (c *Client) FollowUp(ctx context.Context, message string) error {
	_, err := c.Do(ctx, "follow_up", map[string]any{"message": message})
	return err
}

// Abort interrupts the run in progress.
func (c *Client) Abort(ctx context.Context) error {
	_, err := c.Do(ctx, "abort", nil)
	return err
}

// State is the part of get_state that frontends show.
type State struct {
	Model         *Model `json:"model"` // nil until a model is set
	ThinkingLevel string `json:"thinkingLevel"`
	IsStreaming   bool   `json:"isStreaming"`
	SessionFile   string `json:"sessionFile"`
	SessionName   string `json:"sessionName"`
	MessageCount  int    `json:"messageCount"`
}

// Model is the identity of a model in get_state.
type Model struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// GetState asks for the session state.
func (c *Client) GetState(ctx context.Context) (State, error) {
	var s State
	data, err := c.Do(ctx, "get_state", nil)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

// Command is one entry of get_commands.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// GetCommands lists the slash commands (extension commands, prompt templates and skills).
func (c *Client) GetCommands(ctx context.Context) ([]Command, error) {
	data, err := c.Do(ctx, "get_commands", nil)
	if err != nil {
		return nil, err
	}
	var d struct{ Commands []Command }
	err = json.Unmarshal(data, &d)
	return d.Commands, err
}

// RespondUI answers an extension_ui_request dialog: pass "value", "confirmed" or "cancelled" in fields. It expects no response.
func (c *Client) RespondUI(id string, fields map[string]any) error {
	msg := map[string]any{}
	for k, v := range fields {
		msg[k] = v
	}
	msg["type"], msg["id"] = "extension_ui_response", id
	return c.write(msg)
}

// Close ends the conversation: the server finishes the commands in flight and exits at the end of its input.
func (c *Client) Close() error {
	c.once.Do(func() { close(c.stop) })
	return c.w.Close()
}
