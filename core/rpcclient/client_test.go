package rpcclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fake is a scripted server on the far end of a pair of pipes.
type fake struct {
	in  *bufio.Reader // commands the client wrote
	out *io.PipeWriter
}

func pair(t *testing.T) (*Client, *fake) {
	t.Helper()
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	c := NewClient(cr, cw)
	t.Cleanup(func() { _ = sw.Close(); _ = cw.Close() })
	return c, &fake{in: bufio.NewReader(sr), out: sw}
}

func (f *fake) read(t *testing.T) map[string]any {
	t.Helper()
	line, err := f.in.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("command %q: %v", line, err)
	}
	return m
}

func (f *fake) send(s string) { _, _ = io.WriteString(f.out, s+"\n") }

func TestClientCorrelatesResponsesAndKeepsEvents(t *testing.T) {
	c, f := pair(t)
	type res struct {
		d   string
		err error
	}
	got := make(chan res, 1)
	go func() {
		d, err := c.Prompt(context.Background(), "hi", "steer")
		got <- res{d, err}
	}()
	cmd := f.read(t)
	if cmd["type"] != "prompt" || cmd["message"] != "hi" || cmd["streamingBehavior"] != "steer" || cmd["id"] == "" {
		t.Fatalf("command: %v", cmd)
	}
	f.send(`{"type":"agent_start"}`)
	f.send(`not json at all`)
	f.send(`{"type":"response","id":"other","command":"x","success":true}`) // nobody waits for it: surfaces as an event
	f.send(`{"type":"response","id":"` + cmd["id"].(string) + `","command":"prompt","success":true,"data":{"disposition":"started"}}`)
	r := <-got
	if r.err != nil || r.d != "started" {
		t.Fatalf("prompt: %v %v", r.d, r.err)
	}
	if e := <-c.Events(); e.Type != "agent_start" {
		t.Errorf("first event %s", e.Type)
	}
	if e := <-c.Events(); e.Type != "response" {
		t.Errorf("unmatched response should surface: %s", e.Type)
	}
}

func TestCommandErrorsAndHelpers(t *testing.T) {
	c, f := pair(t)
	go func() {
		for {
			line, err := f.in.ReadBytes('\n')
			if err != nil {
				return
			}
			var cmd map[string]any
			_ = json.Unmarshal(line, &cmd)
			id, _ := cmd["id"].(string)
			switch cmd["type"] {
			case "abort":
				f.send(`{"type":"response","id":"` + id + `","command":"abort","success":false,"error":"nothing running"}`)
			case "get_state":
				f.send(`{"type":"response","id":"` + id + `","command":"get_state","success":true,` +
					`"data":{"model":{"id":"m","name":"M","provider":"p"},"isStreaming":true,"messageCount":3,"sessionFile":"/s.jsonl"}}`)
			case "get_commands":
				f.send(`{"type":"response","id":"` + id + `","command":"get_commands","success":true,` +
					`"data":{"commands":[{"name":"skill:x","source":"skill"}]}}`)
			case "prompt":
				f.send(`{"type":"response","id":"` + id + `","command":"prompt","success":true,"data":7}`) // not an object
			default:
				f.send(`{"type":"response","id":"` + id + `","command":"` + cmd["type"].(string) + `","success":true}`)
			}
		}
	}()
	ctx := context.Background()
	var ce *CommandError
	if err := c.Abort(ctx); !errors.As(err, &ce) || ce.Message != "nothing running" || !strings.Contains(err.Error(), "abort") {
		t.Errorf("abort: %v", err)
	}
	st, err := c.GetState(ctx)
	if err != nil || st.Model == nil || st.Model.ID != "m" || !st.IsStreaming || st.MessageCount != 3 || st.SessionFile != "/s.jsonl" {
		t.Errorf("state: %+v %v", st, err)
	}
	cmds, err := c.GetCommands(ctx)
	if err != nil || len(cmds) != 1 || cmds[0].Name != "skill:x" {
		t.Errorf("commands: %v %v", cmds, err)
	}
	if err := c.Steer(ctx, "s"); err != nil {
		t.Error(err)
	}
	if err := c.FollowUp(ctx, "f"); err != nil {
		t.Error(err)
	}
	if _, err := c.Prompt(ctx, "x", ""); err == nil {
		t.Error("a prompt answer without a disposition object should fail")
	}
}

func TestRespondUIWritesAResponseLine(t *testing.T) {
	c, f := pair(t)
	go func() { _ = c.RespondUI("ui-1", map[string]any{"confirmed": true}) }()
	m := f.read(t)
	if m["type"] != "extension_ui_response" || m["id"] != "ui-1" || m["confirmed"] != true {
		t.Errorf("%v", m)
	}
}

func TestContextCancelAndClosedConnection(t *testing.T) {
	c, f := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := c.Do(ctx, "get_state", nil); errc <- err }()
	f.read(t)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}

	// a command in flight when the server's output ends fails with ErrClosed, later ones too
	go func() { _, err := c.Do(context.Background(), "get_state", nil); errc <- err }()
	f.read(t)
	_ = f.out.Close()
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Errorf("in flight: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed")
	}
	if _, err := c.Do(context.Background(), "get_state", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("after close: %v", err)
	}
	if c.Err() != nil {
		t.Errorf("a clean end has no error: %v", c.Err())
	}
	for range c.Events() { // closed
	}
}

func TestReadErrorIsReportedAndCloseStopsDelivery(t *testing.T) {
	cr, sw := io.Pipe()
	_, cw := io.Pipe()
	c := NewClient(cr, cw)
	if c.Err() != nil {
		t.Error("open connection has no error")
	}
	boom := errors.New("boom")
	_ = sw.CloseWithError(boom)
	<-c.Done()
	if !errors.Is(c.Err(), boom) {
		t.Errorf("err %v", c.Err())
	}

	// Close while the consumer is not reading must not leave the reader blocked
	cr2, sw2 := io.Pipe()
	_, cw2 := io.Pipe()
	c2 := NewClient(cr2, cw2)
	go func() {
		for i := 0; i < 400; i++ { // more than the buffer holds
			if _, err := io.WriteString(sw2, `{"type":"x"}`+"\n"); err != nil {
				return
			}
		}
		_ = sw2.Close()
	}()
	time.Sleep(50 * time.Millisecond)
	_ = c2.Close()
	select {
	case <-c2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reader stayed blocked after Close")
	}
}
