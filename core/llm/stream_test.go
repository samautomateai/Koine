package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sseServer answers with a text/event-stream made of the given raw lines. Tests use fake servers only: these wire formats are not
// verified against the live APIs.
func sseServer(t *testing.T, status int, body string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			b := make([]byte, 1<<20)
			n, _ := r.Body.Read(b)
			*seen = append(*seen, string(b[:n]))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ev(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }

const anthropicBodyOK = `: comment
` + `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":11,"cache_read_input_tokens":3,"output_tokens":1}}}

` + `data: {"type":"ping"}

` + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

` + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}

` + `data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}

` + `data: {"type":"content_block_stop","index":0}

` + `data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

` + `data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hel"}}

` + `data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}

` + `data: {"type":"content_block_stop","index":1}

` + `data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu1","name":"read"}}

` + `data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

` + `data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"a\"}"}}

` + `data: {"type":"content_block_stop","index":2}

` + `data: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"enc"}}

` + `data: {"type":"content_block_stop","index":3}

` + `data: {"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"tu2","name":"ls"}}

` + `data: {"type":"content_block_stop","index":4}

` + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}

` + `data: {"type":"message_stop"}

`

func TestAnthropicStreamBuildsTheResponseAndReportsDeltas(t *testing.T) {
	var seen []string
	srv := sseServer(t, 200, anthropicBodyOK, &seen)
	a := &Anthropic{APIKey: "k", BaseURL: srv.URL, HTTP: &http.Client{Timeout: time.Minute}}
	var got []StreamEvent
	resp, err := Stream(context.Background(), a, Request{Model: "m", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "x"}}}}},
		func(e StreamEvent) { got = append(got, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen[0], `"stream":true`) {
		t.Errorf("request did not ask for a stream: %s", seen[0])
	}
	var kinds []string
	for _, e := range got {
		kinds = append(kinds, e.Type+":"+e.Delta+e.ID)
	}
	want := "thinking_delta:hm,text_delta:Hel,text_delta:lo,tool_start:tu1,tool_delta:{\"path\":tu1,tool_delta:\"a\"}tu1,tool_start:tu2"
	if strings.Join(kinds, ",") != want {
		t.Errorf("events:\n got %s\nwant %s", strings.Join(kinds, ","), want)
	}
	c := resp.Content
	if len(c) != 5 || c[0].Thinking != "hm" || c[0].Signature != "sig" || c[1].Text != "Hello" || c[2].Name != "read" ||
		string(c[2].Input) != `{"path":"a"}` || !c[3].Redacted || c[3].Signature != "enc" || string(c[4].Input) != `{}` {
		t.Errorf("content: %+v", c)
	}
	if resp.StopReason != "tool_use" || resp.Usage != (Usage{Input: 11, Output: 42, CacheRead: 3}) {
		t.Errorf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
}

func TestAnthropicStreamErrorsAndPartialContent(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"error event", ev("m", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"par"}}`) +
			ev("e", `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`), "overloaded_error: busy"},
		{"cut off", ev("m", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"par"}}`), "ended before"},
		{"bad json", "data: {nope\n\n", "bad stream event"},
		{"bad index", ev("m", `{"type":"content_block_start","index":99999,"content_block":{"type":"text"}}`), "block index"},
	}
	for _, tc := range cases {
		srv := sseServer(t, 200, tc.body, nil)
		a := &Anthropic{APIKey: "k", BaseURL: srv.URL}
		resp, err := a.Stream(context.Background(), Request{Model: "m"}, func(StreamEvent) {})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v", tc.name, err)
		}
		if tc.name == "error event" && (resp == nil || len(resp.Content) != 1 || resp.Content[0].Text != "par") {
			t.Errorf("%s: partial content: %+v", tc.name, resp)
		}
	}
}

func TestStreamFallsBackToJSONBodiesAndToCompleteOnlyProviders(t *testing.T) {
	// a proxy that answers a streaming request with plain JSON still works
	js := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"plain"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer js.Close()
	resp, err := (&Anthropic{BaseURL: js.URL}).Stream(context.Background(), Request{}, nil2)
	if err != nil || resp.Content[0].Text != "plain" || resp.Usage.Output != 3 {
		t.Fatalf("anthropic json: %+v %v", resp, err)
	}
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"plain"},"finish_reason":"stop"}]}`))
	}))
	defer oa.Close()
	resp, err = (&OpenAICompat{BaseURL: oa.URL}).Stream(context.Background(), Request{}, nil2)
	if err != nil || resp.Content[0].Text != "plain" {
		t.Fatalf("openai json: %+v %v", resp, err)
	}
	// a provider without Stream is called with Complete
	resp, err = Stream(context.Background(), completeOnly{}, Request{}, nil)
	if err != nil || resp.Content[0].Text != "whole" {
		t.Fatalf("complete only: %+v %v", resp, err)
	}
	if _, err := Stream(context.Background(), Missing{Reason: "no key"}, Request{}, nil); err == nil || err.Error() != "no key" {
		t.Errorf("missing: %v", err)
	}
}

func nil2(StreamEvent) {}

type completeOnly struct{}

func (completeOnly) Complete(context.Context, Request) (*Response, error) {
	return &Response{Content: []Block{{Type: "text", Text: "whole"}}}, nil
}

func chunk(s string) string { return "data: " + s + "\n\n" }

func TestOpenAIStreamBuildsTextThinkingAndToolCalls(t *testing.T) {
	body := chunk(`{"choices":[{"delta":{"reasoning_content":"th"}}]}`) +
		chunk(`{"choices":[{"delta":{"reasoning":"ink","content":"Hi "}}]}`) +
		chunk(`{"choices":[{"delta":{"content":"there"}}]}`) +
		chunk(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read","arguments":"{\"p"}}]}}]}`) +
		chunk(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\":1}"}},{"index":1,"function":{"name":"ls"}}]}}]}`) +
		chunk(`{"choices":[{"delta":{"tool_calls":[{"index":2,"id":"c3","function":{"name":"bad","arguments":"{oops"}}]}}]}`) +
		chunk(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`) +
		chunk(`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":8}}}`) +
		"data: [DONE]\n\n"
	var seen []string
	srv := sseServer(t, 200, body, &seen)
	o := &OpenAICompat{Name: "grok", BaseURL: srv.URL + "/", APIKey: "k"}
	var types []string
	resp, err := Stream(context.Background(), o, Request{Model: "m"}, func(e StreamEvent) { types = append(types, e.Type) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen[0], `"stream":true`) || !strings.Contains(seen[0], `"include_usage":true`) {
		t.Errorf("request: %s", seen[0])
	}
	want := "thinking_delta,thinking_delta,text_delta,text_delta,tool_start,tool_delta,tool_delta,tool_start,tool_start,tool_delta"
	if got := strings.Join(types, ","); got != want {
		t.Errorf("events: %s", got)
	}
	c := resp.Content
	if len(c) != 5 || c[0].Thinking != "think" || c[1].Text != "Hi there" || c[2].ID != "c1" || string(c[2].Input) != `{"p":1}` ||
		c[3].Name != "ls" || c[3].ID != "call_1" || string(c[3].Input) != `{}` || string(c[4].Input) != `{}` {
		t.Errorf("content: %+v", c)
	}
	if resp.StopReason != "tool_calls" || resp.Usage != (Usage{Input: 12, Output: 5, CacheRead: 8}) {
		t.Errorf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
}

func TestOpenAIStreamErrors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"error chunk": {chunk(`{"choices":[{"delta":{"content":"par"}}]}`) + chunk(`{"error":{"message":"rate"}}`), "stream error"},
		"cut off":     {chunk(`{"choices":[{"delta":{"content":"par"}}]}`), "ended before"},
		"bad chunk":   {"data: {no\n\n", "bad stream chunk"},
		"bad index":   {chunk(`{"choices":[{"delta":{"tool_calls":[{"index":-1,"id":"x","function":{"name":"n"}}]}}]}`), "tool call index"},
	} {
		srv := sseServer(t, 200, tc.body, nil)
		resp, err := (&OpenAICompat{BaseURL: srv.URL}).Stream(context.Background(), Request{}, nil2)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
		if name == "cut off" && (resp == nil || resp.Content[0].Text != "par") {
			t.Errorf("partial: %+v", resp)
		}
	}
	// the finish reason is enough when a server leaves out [DONE]
	srv := sseServer(t, 200, chunk(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`), nil)
	if r, err := (&OpenAICompat{BaseURL: srv.URL}).Stream(context.Background(), Request{}, nil2); err != nil || r.Content[0].Text != "ok" {
		t.Errorf("no DONE: %+v %v", r, err)
	}
}

func TestStreamOpeningIsRetriedAndHTTPErrorsSurface(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("down"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(chunk(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)))
	}))
	defer srv.Close()
	o := &OpenAICompat{BaseURL: srv.URL, Retry: fast}
	notices := 0
	ctx := WithRetryNotify(context.Background(), func(RetryNotice) { notices++ })
	if r, err := o.Stream(ctx, Request{}, nil2); err != nil || r.Content[0].Text != "ok" || n.Load() != 3 || notices != 2 {
		t.Fatalf("retry: %+v %v attempts %d notices %d", r, err, n.Load(), notices)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); _, _ = w.Write([]byte("no")) }))
	defer bad.Close()
	_, err := (&Anthropic{BaseURL: bad.URL, Retry: fast}).Stream(context.Background(), Request{}, nil2)
	var he *HTTPError
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("401: %v (%v)", err, he)
	}
}

// stalling sends one event and then goes quiet until the client gives up.
func stalling(t *testing.T, first string, headerDelay time.Duration) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(headerDelay):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if first != "" {
			_, _ = w.Write([]byte(first))
		}
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamIdleTimeoutAppliesToSilenceNotToTotalTime(t *testing.T) {
	first := chunk(`{"choices":[{"delta":{"content":"par"}}]}`)
	one := RetryPolicy{Disabled: true, Timeout: 150 * time.Millisecond}
	// silence after some output: the error names the timeout and the output so far is returned
	o := &OpenAICompat{BaseURL: stalling(t, first, 0).URL, Retry: one}
	start := time.Now()
	resp, err := o.Stream(context.Background(), Request{}, nil2)
	if err == nil || !strings.Contains(err.Error(), "no data from the provider for 150ms") || time.Since(start) > 3*time.Second {
		t.Fatalf("idle: %v after %s", err, time.Since(start))
	}
	if resp == nil || resp.Content[0].Text != "par" {
		t.Errorf("partial: %+v", resp)
	}
	// silence before the headers
	a := &Anthropic{BaseURL: stalling(t, "", time.Second).URL, Retry: one}
	if _, err := a.Stream(context.Background(), Request{}, nil2); err == nil || !strings.Contains(err.Error(), "no data from the provider") {
		t.Errorf("idle before headers: %v", err)
	}
	// a stream that keeps talking outlives the timeout, and so does one with the limit disabled
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 6; i++ {
			_, _ = fmt.Fprint(w, chunk(`{"choices":[{"delta":{"content":"x"}}]}`))
			w.(http.Flusher).Flush()
			time.Sleep(60 * time.Millisecond)
		}
		_, _ = fmt.Fprint(w, chunk(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer slow.Close()
	for _, pol := range []RetryPolicy{one, {Timeout: -1}} {
		o := &OpenAICompat{BaseURL: slow.URL, Retry: pol, HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
		r, err := o.Stream(context.Background(), Request{}, nil2)
		if err != nil || r.Content[0].Text != "xxxxxx" {
			t.Errorf("steady stream with %+v: %+v %v", pol, r, err)
		}
	}
}

func TestStreamAbortKeepsPartialTextAndDoesNotCallItAnIdleTimeout(t *testing.T) {
	first := ev("m", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		ev("d", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half"}}`)
	a := &Anthropic{BaseURL: stalling(t, first, 0).URL}
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 1)
	go func() {
		<-got
		cancel()
	}()
	resp, err := a.Stream(ctx, Request{}, func(e StreamEvent) { got <- struct{}{} })
	if err == nil || strings.Contains(err.Error(), "no data") || ctx.Err() == nil {
		t.Fatalf("abort: %v", err)
	}
	if resp == nil || len(resp.Content) != 1 || resp.Content[0].Text != "half" {
		t.Errorf("partial: %+v", resp)
	}
}

func TestIdlePolicyAndRouterStream(t *testing.T) {
	for _, tc := range []struct {
		p    RetryPolicy
		want time.Duration
	}{{RetryPolicy{}, DefaultIdleTimeout}, {RetryPolicy{Timeout: time.Second}, time.Second}, {RetryPolicy{Timeout: -1}, 0}} {
		if got := tc.p.idle(); got != tc.want {
			t.Errorf("idle(%+v) = %s, want %s", tc.p, got, tc.want)
		}
	}
	srv := sseServer(t, 200, chunk(`{"choices":[{"delta":{"content":"routed"},"finish_reason":"stop"}]}`), nil)
	r := &Router{Custom: map[string]ProviderConfig{"mine": {BaseURL: srv.URL}}}
	resp, err := r.Stream(context.Background(), Request{Model: "mine/m"}, nil2)
	if err != nil || resp.Content[0].Text != "routed" {
		t.Fatalf("router: %+v %v", resp, err)
	}
	if _, err := r.Stream(context.Background(), Request{Model: "nope/x"}, nil2); err == nil {
		t.Error("unknown provider")
	}
	var _ Streamer = r
	b, _ := json.Marshal(StreamEvent{Type: EvTextDelta})
	if len(b) == 0 {
		t.Error("marshal")
	}
}

func TestReadSSEHandlesCRLFMultilineDataAndTrailingEventsWithoutBlankLine(t *testing.T) {
	var got []string
	in := "event: x\r\ndata: one\r\ndata: two\r\n\r\n: keepalive\n\ndata:nospace\n\ndata: last"
	if err := readSSE(strings.NewReader(in), func(d []byte) error { got = append(got, string(d)); return nil }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "one\ntwo|nospace|last" {
		t.Errorf("got %q", got)
	}
	want := fmt.Errorf("stop")
	if err := readSSE(strings.NewReader("data: a\n\ndata: b\n\n"), func([]byte) error { return want }); !errors.Is(err, want) {
		t.Errorf("fn error: %v", err)
	}
}

// A streaming request carries image blocks the same way a plain one does (fake servers only).
func TestStreamingRequestsCarryImages(t *testing.T) {
	req := Request{Model: "m", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "see"}, ImageBlock("image/png", "QUJD")}}}}
	var seen []string
	srv := sseServer(t, 200, "data: {\"type\":\"message_stop\"}\n\n", &seen)
	if _, err := (&Anthropic{APIKey: "k", BaseURL: srv.URL}).Stream(context.Background(), req, func(StreamEvent) {}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], `"type":"image"`) || !strings.Contains(seen[0], `"data":"QUJD"`) {
		t.Errorf("anthropic body: %v", seen)
	}
	seen = nil
	srv2 := sseServer(t, 200, "data: [DONE]\n\n", &seen)
	if _, err := (&OpenAICompat{BaseURL: srv2.URL, APIKey: "k"}).Stream(context.Background(), req, func(StreamEvent) {}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "data:image/png;base64,QUJD") {
		t.Errorf("openai body: %v", seen)
	}
}
