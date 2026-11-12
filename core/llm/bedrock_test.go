package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var fakeAWSCreds = func() (AWSCredentials, error) {
	return AWSCredentials{AccessKeyID: "AKIDFAKE", SecretAccessKey: "fake-secret-not-real", SessionToken: "fake-session-token"}, nil
}

// bedrockServer answers every request with status and body, recording the request. It also checks the SigV4 signature by signing the
// request it received again.
type bedrockServer struct {
	*httptest.Server
	path, rawPath, auth, token, accept string
	body                               map[string]any
	signatureOK                        bool
}

func newBedrockServer(t *testing.T, status int, contentType string, reply []byte) *bedrockServer {
	t.Helper()
	s := &bedrockServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.body = nil
		_ = json.Unmarshal(raw, &s.body)
		s.path, s.rawPath, s.accept = r.URL.Path, r.URL.EscapedPath(), r.Header.Get("accept")
		s.auth, s.token = r.Header.Get("authorization"), r.Header.Get("x-amz-security-token")
		if strings.HasPrefix(s.auth, "AWS4-HMAC-SHA256") {
			at, _ := time.Parse("20060102T150405Z", r.Header.Get("x-amz-date"))
			again, _ := SigV4{Creds: AWSCredentials{AccessKeyID: "AKIDFAKE", SecretAccessKey: "fake-secret-not-real", SessionToken: "fake-session-token"},
				Region: "us-east-1", Service: "bedrock"}.Sign("POST", "http://"+r.Host+r.URL.EscapedPath(),
				map[string]string{"content-type": r.Header.Get("content-type")}, raw, at)
			s.signatureOK = again["authorization"] == s.auth
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(reply)
	}))
	t.Cleanup(s.Close)
	return s
}

func bedrockFor(s *bedrockServer) *Bedrock {
	return &Bedrock{BaseURL: s.URL, Region: "us-east-1", Creds: fakeAWSCreds, Retry: RetryPolicy{Disabled: true},
		Now: func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }}
}

const converseJSON = `{"output":{"message":{"role":"assistant","content":[
  {"reasoningContent":{"reasoningText":{"text":"hmm","signature":"sig1"}}},
  {"reasoningContent":{"redactedContent":"b64redacted"}},
  {"text":"hello"},
  {"toolUse":{"toolUseId":"tu_1","name":"read","input":{"path":"a.go"}}}]}},
  "stopReason":"tool_use","usage":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":3,"cacheWriteInputTokens":2}}`

func TestBedrockCompleteSignsAndParses(t *testing.T) {
	srv := newBedrockServer(t, 200, "application/json", []byte(converseJSON))
	b := bedrockFor(srv)
	resp, err := b.Complete(context.Background(), Request{Model: "us.anthropic.claude-sonnet-5-5-v1:0", System: "be brief",
		Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}},
		Tools:    []ToolDef{{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if srv.rawPath != "/model/us.anthropic.claude-sonnet-5-5-v1%3A0/converse" {
		t.Errorf("path = %q", srv.rawPath)
	}
	if !strings.Contains(srv.auth, "Credential=AKIDFAKE/20260304/us-east-1/bedrock/aws4_request") ||
		!strings.Contains(srv.auth, "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token") || !srv.signatureOK {
		t.Errorf("authorization = %q (signature ok: %v)", srv.auth, srv.signatureOK)
	}
	if srv.token != "fake-session-token" {
		t.Errorf("session token header = %q", srv.token)
	}
	if got := resp.StopReason; got != "tool_use" {
		t.Errorf("stop = %q", got)
	}
	if resp.Usage != (Usage{Input: 10, Output: 5, CacheRead: 3, CacheWrite: 2}) {
		t.Errorf("usage = %+v", resp.Usage)
	}
	want := []Block{{Type: "thinking", Thinking: "hmm", Signature: "sig1"}, {Type: "thinking", Redacted: true, Signature: "b64redacted"},
		{Type: "text", Text: "hello"}, {Type: "tool_use", ID: "tu_1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)}}
	if len(resp.Content) != len(want) {
		t.Fatalf("content = %+v", resp.Content)
	}
	for i, w := range want {
		g := resp.Content[i]
		if g.Type != w.Type || g.Text != w.Text || g.Thinking != w.Thinking || g.Signature != w.Signature || g.Redacted != w.Redacted ||
			g.ID != w.ID || g.Name != w.Name || string(g.Input) != string(w.Input) {
			t.Errorf("block %d = %+v, want %+v", i, g, w)
		}
	}
	// request body: system, cache points (a Claude model), tool spec, inference config
	body, _ := json.Marshal(srv.body)
	for _, frag := range []string{`"system":[{"text":"be brief"},{"cachePoint":{"type":"default"}}]`, `"toolSpec":{"description":"read a file"`,
		`"inputSchema":{"json":{"type":"object"}}`, `"inferenceConfig":{"maxTokens":8192}`,
		`"content":[{"text":"hi"},{"cachePoint":{"type":"default"}}],"role":"user"`} {
		if !strings.Contains(string(body), frag) {
			t.Errorf("request body lacks %s\n%s", frag, body)
		}
	}
}

func TestBedrockBearerSkipAuthAndNoCredentials(t *testing.T) {
	srv := newBedrockServer(t, 200, "application/json", []byte(`{"output":{"message":{"content":[{"text":"ok"}]}},"stopReason":"end_turn"}`))
	req := Request{Model: "amazon.nova-pro-v1:0", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}}}
	b := bedrockFor(srv)
	b.Bearer = "fake-bedrock-bearer"
	if _, err := b.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if srv.auth != "Bearer fake-bedrock-bearer" || srv.token != "" {
		t.Errorf("bearer: auth %q token %q", srv.auth, srv.token)
	}
	if strings.Contains(srv.rawPath, "cachePoint") || strings.Contains(mustJSON(srv.body), "cachePoint") {
		t.Error("a non-Claude model should get no cache points")
	}
	b = bedrockFor(srv)
	b.SkipAuth, b.ForceCache = true, true
	if _, err := b.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if srv.auth != "" || !strings.Contains(mustJSON(srv.body), "cachePoint") {
		t.Errorf("skip auth: auth %q, force cache body %s", srv.auth, mustJSON(srv.body))
	}
	b = bedrockFor(srv)
	b.Creds = nil
	if _, err := b.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "no AWS credentials") {
		t.Errorf("nil credentials: %v", err)
	}
	b.Creds = func() (AWSCredentials, error) {
		return AWSCredentials{}, errors.New("no AWS credentials: set AWS_BEARER_TOKEN_BEDROCK")
	}
	if _, err := b.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "amazon-bedrock: no AWS credentials") {
		t.Errorf("credential error: %v", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestBedrockMessagesMergeRolesAndConvertBlocks(t *testing.T) {
	img := ImageBlock("image/jpeg", "QUJD")
	msgs := []Message{
		{Role: "user", Content: []Block{{Type: "text", Text: "a"}}},
		{Role: "user", Content: []Block{{Type: "text", Text: "  "}, img}},
		{Role: "assistant", Content: []Block{{Type: "thinking", Thinking: "t", Signature: "s"}, {Type: "thinking", Thinking: "unsigned"},
			{Type: "thinking", Redacted: true, Signature: "red"}, {Type: "tool_use", ID: "c1", Name: "bash"}}},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "c1", IsError: true, Images: []Block{ImageBlock("image/webp", "WFla")}}}},
		{Role: "assistant", Content: nil},
	}
	out := toBedrockMessages(msgs, false, true)
	got := mustJSON(out)
	for _, frag := range []string{`{"text":"a"},{"image":{"format":"jpeg","source":{"bytes":"QUJD"}}}`,
		`{"reasoningContent":{"reasoningText":{"signature":"s","text":"t"}}}`, `{"reasoningContent":{"redactedContent":"red"}}`,
		`"toolUse":{"input":{},"name":"bash","toolUseId":"c1"}`, `"status":"error"`, `{"text":"(no output)"},{"image":{"format":"webp"`,
		`"cachePoint"`} {
		if !strings.Contains(got, frag) {
			t.Errorf("messages lack %s\n%s", frag, got)
		}
	}
	if strings.Contains(got, "unsigned") {
		t.Error("an unsigned thinking block cannot be replayed")
	}
	if len(out) != 3 || out[0]["role"] != "user" || out[1]["role"] != "assistant" || out[2]["role"] != "user" {
		t.Errorf("roles should alternate after merging: %s", got)
	}
	if got := mustJSON(toBedrockMessages(msgs, true, false)); strings.Contains(got, "reasoningContent") || strings.Contains(got, "cachePoint") {
		t.Errorf("thinking dropped and no cache: %s", got)
	}
	for mime, want := range map[string]string{"image/png": "png", "image/gif": "gif", "image/jpg": "jpeg", "": "png"} {
		if bedrockFormat(mime) != want {
			t.Errorf("format of %q", mime)
		}
	}
}

func TestBedrockThinkingForClaudeOnly(t *testing.T) {
	srv := newBedrockServer(t, 200, "application/json", []byte(`{"output":{"message":{"content":[]}}}`))
	b := bedrockFor(srv)
	user := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}}
	for _, c := range []struct {
		model    string
		th       *Thinking
		contains string
	}{
		{"anthropic.claude-x", &Thinking{Level: "high", Mode: ModeBudget, Budget: 4096}, `"thinking":{"budget_tokens":4096,"type":"enabled"}`},
		{"anthropic.claude-x", &Thinking{Level: "low", Mode: ModeEffort}, `"thinking":{"budget_tokens":2048,"type":"enabled"}`},
		{"anthropic.claude-x", &Thinking{Level: "medium", Mode: ModeAdaptive}, `"output_config":{"effort":"medium"}`},
		{"amazon.nova-pro", &Thinking{Level: "high", Mode: ModeBudget, Budget: 4096}, `"inferenceConfig"`},
	} {
		if _, err := b.Complete(context.Background(), Request{Model: c.model, Messages: user, Thinking: c.th, MaxTokens: 20000}); err != nil {
			t.Fatal(err)
		}
		got := mustJSON(srv.body)
		if !strings.Contains(got, c.contains) || (strings.HasPrefix(c.model, "amazon") && strings.Contains(got, "thinking")) {
			t.Errorf("%s %+v: %s", c.model, c.th, got)
		}
	}
}

func bedrockFrames() []byte {
	var b []byte
	for _, f := range [][2]string{
		{"messageStart", `{"role":"assistant"}`},
		{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"think "}}}`},
		{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"more","signature":"sg"}}}`},
		{"contentBlockStop", `{"contentBlockIndex":0}`},
		{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"Hel"}}`},
		{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"lo"}}`},
		{"contentBlockStop", `{"contentBlockIndex":1}`},
		{"contentBlockStart", `{"contentBlockIndex":2,"start":{"toolUse":{"toolUseId":"tu9","name":"bash"}}}`},
		{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"{\"cmd\":"}}}`},
		{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"\"ls\"}"}}}`},
		{"contentBlockStop", `{"contentBlockIndex":2}`},
		{"contentBlockDelta", `{"contentBlockIndex":3,"delta":{"reasoningContent":{"redactedContent":"cmVk"}}}`},
		{"messageStop", `{"stopReason":"tool_use"}`},
		{"metadata", `{"usage":{"inputTokens":7,"outputTokens":3,"cacheReadInputTokens":100,"cacheWriteInputTokens":0}}`},
	} {
		b = append(b, eventFrame(f[0], f[1])...)
	}
	return b
}

func TestBedrockStreamDecodesEventStream(t *testing.T) {
	srv := newBedrockServer(t, 200, "application/vnd.amazon.eventstream", bedrockFrames())
	var events []StreamEvent
	resp, err := bedrockFor(srv).Stream(context.Background(), Request{Model: "anthropic.claude-x",
		Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "go"}}}}}, func(e StreamEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if srv.rawPath != "/model/anthropic.claude-x/converse-stream" || srv.accept != "application/vnd.amazon.eventstream" || !srv.signatureOK {
		t.Errorf("path %q accept %q signature ok %v", srv.rawPath, srv.accept, srv.signatureOK)
	}
	if resp.StopReason != "tool_use" || resp.Usage != (Usage{Input: 7, Output: 3, CacheRead: 100}) {
		t.Errorf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
	if len(resp.Content) != 4 {
		t.Fatalf("content = %+v", resp.Content)
	}
	if b := resp.Content[0]; b.Type != "thinking" || b.Thinking != "think more" || b.Signature != "sg" {
		t.Errorf("thinking = %+v", b)
	}
	if b := resp.Content[1]; b.Type != "text" || b.Text != "Hello" {
		t.Errorf("text = %+v", b)
	}
	if b := resp.Content[2]; b.Type != "tool_use" || b.ID != "tu9" || string(b.Input) != `{"cmd":"ls"}` {
		t.Errorf("tool = %+v", b)
	}
	if b := resp.Content[3]; !b.Redacted || b.Signature != "cmVk" {
		t.Errorf("redacted = %+v", b)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Type)
	}
	if got := strings.Join(kinds, ","); got != "thinking_delta,thinking_delta,text_delta,text_delta,tool_start,tool_delta,tool_delta" {
		t.Errorf("events = %s", got)
	}
}

func TestBedrockStreamErrors(t *testing.T) {
	user := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "go"}}}}
	run := func(reply []byte) (*Response, error) {
		srv := newBedrockServer(t, 200, "application/vnd.amazon.eventstream", reply)
		return bedrockFor(srv).Stream(context.Background(), Request{Model: "m", Messages: user}, nil)
	}
	partial := append(eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"par"}}`),
		exceptionFrame("throttlingException", `{"message":"Too many requests"}`)...)
	resp, err := run(partial)
	if err == nil || !strings.Contains(err.Error(), "throttlingException: Too many requests") || resp == nil || len(resp.Content) != 1 ||
		resp.Content[0].Text != "par" {
		t.Errorf("exception: %v %+v", err, resp)
	}
	if _, err = run(eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"x"}}`)); err == nil ||
		!strings.Contains(err.Error(), "before the message was complete") {
		t.Errorf("cut stream: %v", err)
	}
	if _, err = run(eventFrame("contentBlockDelta", `not json`)); err == nil || !strings.Contains(err.Error(), "bad stream event") {
		t.Errorf("bad json: %v", err)
	}
	if _, err = run(eventFrame("contentBlockDelta", `{"contentBlockIndex":99999}`)); err == nil || !strings.Contains(err.Error(), "block index") {
		t.Errorf("bad index: %v", err)
	}
	if _, err = run([]byte("garbage-that-is-long-enough-for-a-prelude")); err == nil {
		t.Error("a corrupt frame should fail")
	}
	// a proxy that answers with plain Converse JSON is read as such
	srv := newBedrockServer(t, 200, "application/json", []byte(converseJSON))
	r, err := bedrockFor(srv).Stream(context.Background(), Request{Model: "m", Messages: user}, nil)
	if err != nil || len(r.Content) != 4 {
		t.Errorf("json fallback: %v %+v", err, r)
	}
}

func TestBedrockHTTPErrorsAreReadable(t *testing.T) {
	srv := newBedrockServer(t, 403, "application/json", []byte(`{"message":"The security token included in the request is invalid."}`))
	one := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "x"}}}}
	_, err := bedrockFor(srv).Complete(context.Background(), Request{Model: "m", Messages: one})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403: The security token included in the request is invalid.") {
		t.Errorf("error = %v", err)
	}
	if _, err = bedrockFor(srv).Stream(context.Background(), Request{Model: "m"}, nil); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("stream error = %v", err)
	}
	if _, err := parseBedrock("x", []byte("{")); err == nil {
		t.Error("bad JSON should fail")
	}
}
