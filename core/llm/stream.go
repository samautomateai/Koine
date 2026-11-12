package llm

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Stream event types.
const (
	EvTextDelta     = "text_delta"     // Delta is more text
	EvThinkingDelta = "thinking_delta" // Delta is more thinking text
	EvToolStart     = "tool_start"     // a tool call began: ID and Name are set
	EvToolDelta     = "tool_delta"     // Delta is more of the call's JSON arguments, ID says which call
)

// StreamEvent is one increment of a response that is still being generated.
type StreamEvent struct {
	Type     string
	Delta    string
	ID, Name string // tool calls only
}

// Streamer is implemented by providers that can deliver a response as it is generated. Stream behaves like Complete and also calls fn
// (if not nil) for each increment. When it fails after output has begun, the returned Response is not nil and holds the text and thinking
// received so far, which is what an aborted turn keeps; tool calls that had not finished are left out.
type Streamer interface {
	Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error)
}

// Stream asks p for a response, streaming it when p can. A provider that cannot stream is called with Complete and fn never fires.
func Stream(ctx context.Context, p Provider, req Request, fn func(StreamEvent)) (*Response, error) {
	if fn == nil {
		fn = func(StreamEvent) {}
	}
	if s, ok := p.(Streamer); ok {
		return s.Stream(ctx, req, fn)
	}
	return p.Complete(ctx, req)
}

// Stream implements Streamer by resolving req.Model per call.
func (r *Router) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	p, req, err := r.prepare(req)
	if err != nil {
		return nil, err
	}
	return Stream(ctx, p, req, fn)
}

var errIdle = errors.New("idle timeout")

// respStream is the open body of a streamed response. Every read that returns data restarts the idle timer; when it fires the request is
// cancelled and the reads fail with an error that names the timeout.
type respStream struct {
	resp   *http.Response
	ctx    context.Context
	cancel context.CancelCauseFunc
	timer  *time.Timer
	idle   time.Duration
	label  string
}

func idleError(label string, idle time.Duration) error {
	return fmt.Errorf("%s: no data from the provider for %s (httpIdleTimeoutMs)", label, idle)
}

func (s *respStream) Read(p []byte) (int, error) {
	n, err := s.resp.Body.Read(p)
	if n > 0 && s.timer != nil {
		s.timer.Reset(s.idle)
	}
	if err != nil && !errors.Is(err, io.EOF) && errors.Is(context.Cause(s.ctx), errIdle) {
		err = idleError(s.label, s.idle)
	}
	return n, err
}

func (s *respStream) Close() error {
	if s.timer != nil {
		s.timer.Stop()
	}
	err := s.resp.Body.Close()
	s.cancel(nil)
	return err
}

// sse reports whether the answer is an event stream; a server or proxy may answer with plain JSON instead.
func (s *respStream) sse() bool {
	return strings.HasPrefix(strings.ToLower(s.resp.Header.Get("Content-Type")), "text/event-stream")
}

// openStream POSTs payload and returns the open 200 response, retrying before any output as postJSON does. The caller closes it. The
// client's overall timeout is not applied, because a stream may legitimately last longer; silence is bounded by the policy's idle limit.
func openStream(ctx context.Context, client *http.Client, label, url string, headers map[string]string, payload []byte,
	pol RetryPolicy) (*respStream, error) {
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.Timeout = 0
	idle := pol.idle()
	var st *respStream
	err := retryLoop(ctx, label, pol, func() (int, http.Header, []byte, error) {
		actx, cancel := context.WithCancelCause(ctx)
		var timer *time.Timer
		if idle > 0 {
			timer = time.AfterFunc(idle, func() { cancel(errIdle) })
		}
		stop := func() {
			if timer != nil {
				timer.Stop()
			}
			cancel(nil)
		}
		hr, err := http.NewRequestWithContext(actx, "POST", url, bytes.NewReader(payload))
		if err != nil {
			stop()
			return 0, nil, nil, err
		}
		hr.Header.Set("content-type", "application/json")
		hr.Header.Set("accept", "text/event-stream")
		for k, v := range headers {
			hr.Header.Set(k, v)
		}
		resp, err := c.Do(hr)
		if err != nil {
			if errors.Is(context.Cause(actx), errIdle) {
				err = idleError(label, idle)
			}
			stop()
			return 0, nil, nil, err
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			stop()
			return resp.StatusCode, resp.Header, body, nil
		}
		st = &respStream{resp: resp, ctx: actx, cancel: cancel, timer: timer, idle: idle, label: label}
		if timer != nil {
			timer.Reset(idle)
		}
		return 200, resp.Header, nil, nil
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// readAll reads a body that turned out not to be a stream.
func (s *respStream) readAll() ([]byte, error) { return io.ReadAll(s) }

// readSSE calls fn with the data of each server-sent event. fn must not keep the slice. It returns nil at the end of the stream.
func readSSE(r io.Reader, fn func(data []byte) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var data []byte
	pending := false
	flush := func() error {
		if !pending {
			return nil
		}
		err := fn(data)
		data, pending = data[:0], false
		return err
	}
	for {
		line, err := br.ReadBytes('\n')
		l := bytes.TrimRight(line, "\r\n")
		switch {
		case len(l) == 0 && len(line) > 0:
			if ferr := flush(); ferr != nil {
				return ferr
			}
		case bytes.HasPrefix(l, []byte("data:")):
			rest := bytes.TrimPrefix(bytes.TrimPrefix(l, []byte("data:")), []byte(" "))
			if pending {
				data = append(data, '\n')
			}
			data, pending = append(data, rest...), true
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return flush()
			}
			return err
		}
	}
}
