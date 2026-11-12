package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seenReq is a request that reached a fake server. All provider tests in this package use fake servers: the wire formats are written from
// the vendors' documentation and are not verified against the live APIs.
type seenReq struct {
	Path, Query string
	Header      http.Header
	Body        map[string]any
}

// fakeAPI answers every request with the given status, content type and body, and records the requests.
func fakeAPI(t *testing.T, status int, contentType, body string) (*httptest.Server, *[]seenReq) {
	t.Helper()
	var seen []seenReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s := seenReq{Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header}
		_ = json.Unmarshal(b, &s.Body)
		seen = append(seen, s)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

var noRetry = RetryPolicy{Disabled: true}

func toolRound() []Message {
	return []Message{
		{Role: "user", Content: []Block{{Type: "text", Text: "list files"}}},
		{Role: "assistant", Content: []Block{{Type: "thinking", Thinking: "hmm"}, {Type: "text", Text: "Looking."},
			{Type: "tool_use", ID: "call_1", Name: "ls", Input: json.RawMessage(`{"path":"."}`)}}},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "call_1", Content: "a.txt", Images: []Block{
			{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: "AAAA"}}}}}},
		{Role: "user", Content: []Block{{Type: "text", Text: "thanks"}}},
	}
}

const responsesJSON = `{"status":"completed","output":[
 {"type":"reasoning","summary":[{"text":"first"},{"text":"second"}]},
 {"type":"message","content":[{"type":"output_text","text":"Hello"}]},
 {"type":"function_call","id":"fc_9","call_id":"call_9","name":"ls","arguments":"{\"path\":\".\"}"}],
 "usage":{"input_tokens":120,"output_tokens":30,"input_tokens_details":{"cached_tokens":20}}}`

func TestResponsesSendsAStatelessRequestAndParsesTheOutput(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "application/json", responsesJSON)
	p := &Responses{Name: "openai", APIKey: "fake-key", BaseURL: srv.URL + "/v1/", Retry: noRetry, Headers: map[string]string{"x-team": "t1"},
		Extra: map[string]any{"temperature": 0.2}}
	resp, err := p.Complete(context.Background(), Request{Model: "gpt-5", System: "be brief", Messages: toolRound(), MaxTokens: 500,
		Thinking: &Thinking{Level: "high"}, Tools: []ToolDef{{Name: "ls", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	r := (*seen)[0]
	if r.Path != "/v1/responses" || r.Header.Get("authorization") != "Bearer fake-key" || r.Header.Get("x-team") != "t1" {
		t.Errorf("request: %s %v", r.Path, r.Header)
	}
	b := r.Body
	if b["model"] != "gpt-5" || b["store"] != false || b["instructions"] != "be brief" || b["max_output_tokens"] != float64(500) ||
		b["temperature"] != 0.2 {
		t.Errorf("body: %v", b)
	}
	if rs := b["reasoning"].(map[string]any); rs["effort"] != "high" || rs["summary"] != "auto" {
		t.Errorf("reasoning: %v", rs)
	}
	tool := b["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "ls" || tool["strict"] != false {
		t.Errorf("tool: %v", tool)
	}
	in := b["input"].([]any)
	var kinds []string
	for _, it := range in {
		m := it.(map[string]any)
		k, _ := m["type"].(string)
		if k == "" {
			k = m["role"].(string)
		}
		kinds = append(kinds, k)
	}
	// user text, assistant text (thinking is not replayed), the call, its output, the tool image as a user message, then the new user text
	if got := strings.Join(kinds, ","); got != "user,assistant,function_call,function_call_output,user,user" {
		t.Errorf("input items: %s", got)
	}
	call := in[2].(map[string]any)
	if call["call_id"] != "call_1" || call["arguments"] != `{"path":"."}` || call["id"] != nil {
		t.Errorf("call item: %v", call)
	}
	img := in[4].(map[string]any)["content"].([]any)[1].(map[string]any)
	if img["type"] != "input_image" || img["image_url"] != "data:image/png;base64,AAAA" {
		t.Errorf("image: %v", img)
	}

	if len(resp.Content) != 3 || resp.Content[0].Thinking != "first\n\nsecond" || resp.Content[1].Text != "Hello" ||
		resp.Content[2].ID != "call_9" || resp.Content[2].Name != "ls" || string(resp.Content[2].Input) != `{"path":"."}` {
		t.Errorf("content: %+v", resp.Content)
	}
	if resp.StopReason != "tool_calls" || resp.Usage != (Usage{Input: 100, Output: 30, CacheRead: 20}) {
		t.Errorf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
}

func TestResponsesErrorsAndIncompleteAnswers(t *testing.T) {
	for body, want := range map[string]string{
		`{"status":"failed","error":{"code":"server_error","message":"boom"}}`: "server_error boom",
		`{"status":"completed","error":{"message":"odd"}}`:                     "odd",
		`not json`: "invalid character",
	} {
		srv, _ := fakeAPI(t, 200, "application/json", body)
		_, err := (&Responses{BaseURL: srv.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", body, err)
		}
	}
	srv, _ := fakeAPI(t, 200, "application/json",
		`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},`+
			`"output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`)
	resp, err := (&Responses{BaseURL: srv.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"})
	if err != nil || resp.StopReason != "length" || resp.Content[0].Text != "no" {
		t.Errorf("incomplete: %+v %v", resp, err)
	}
	srv2, _ := fakeAPI(t, 200, "application/json", `{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`)
	if resp, _ := (&Responses{BaseURL: srv2.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"}); resp.StopReason != "content_filter" {
		t.Errorf("reason: %+v", resp)
	}
	// an HTTP error carries the friendly message
	srv3, _ := fakeAPI(t, 401, "application/json", `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	_, err = (&Responses{Name: "openai", BaseURL: srv3.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "openai: HTTP 401: Incorrect API key provided") || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("401: %v", err)
	}
}

const responsesSSE = `data: {"type":"response.created","response":{}}

data: {"type":"response.reasoning_summary_part.added"}

data: {"type":"response.reasoning_summary_text.delta","delta":"think"}

data: {"type":"response.reasoning_summary_part.added"}

data: {"type":"response.reasoning_summary_text.delta","delta":"more"}

data: {"type":"response.output_text.delta","delta":"Hel"}

data: {"type":"response.output_text.delta","delta":"lo"}

data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"ls"}}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\""}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":":\".\"}"}

data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5},` +
	`"output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}` +
	`,{"type":"function_call","id":"fc_1","call_id":"call_1","name":"ls","arguments":"{\"path\":\".\"}"}]}}

data: [DONE]

`

func TestResponsesStreamReportsDeltasAndReturnsTheFinalResponse(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "text/event-stream", responsesSSE)
	p := &Responses{BaseURL: srv.URL, Retry: noRetry}
	var evs []string
	resp, err := Stream(context.Background(), p, Request{Model: "m"}, func(e StreamEvent) { evs = append(evs, e.Type+":"+e.ID+e.Delta+e.Name) })
	if err != nil {
		t.Fatal(err)
	}
	if (*seen)[0].Body["stream"] != true {
		t.Error("stream flag")
	}
	want := "thinking_delta:think,thinking_delta:\n\n,thinking_delta:more,text_delta:Hel,text_delta:lo,tool_start:call_1ls," +
		"tool_delta:call_1{\"path\",tool_delta:call_1:\".\"}"
	if got := strings.Join(evs, ","); got != want {
		t.Errorf("events:\n%q\nwant\n%q", got, want)
	}
	if len(resp.Content) != 2 || resp.Content[0].Text != "Hello" || resp.Content[1].Name != "ls" || resp.Usage.Output != 5 {
		t.Errorf("response: %+v", resp)
	}
}

func TestResponsesStreamErrorsKeepWhatArrived(t *testing.T) {
	partialText := `data: {"type":"response.output_text.delta","delta":"par"}` + "\n\n"
	cases := map[string]string{
		"failed":    partialText + `data: {"type":"response.failed","response":{"status":"failed","error":{"message":"overloaded"}}}` + "\n\n",
		"failed 2":  `data: {"type":"response.failed","response":{}}` + "\n\n",
		"error":     partialText + `data: {"type":"error","code":"rate","message":"slow down"}` + "\n\n",
		"truncated": `data: {"type":"response.output_text.delta","delta":"par"}` + "\n\n",
		"bad json":  "data: {oops\n\n",
	}
	wants := map[string]string{"failed": "overloaded", "failed 2": "failed", "error": "rate slow down", "truncated": "before the response was complete",
		"bad json": "bad stream event"}
	for name, body := range cases {
		srv, _ := fakeAPI(t, 200, "text/event-stream", body)
		resp, err := (&Responses{BaseURL: srv.URL, Retry: noRetry}).Stream(context.Background(), Request{Model: "m"}, func(StreamEvent) {})
		if err == nil || !strings.Contains(err.Error(), wants[name]) {
			t.Errorf("%s: %v", name, err)
		}
		if name != "bad json" && name != "failed 2" && (resp == nil || len(resp.Content) != 1 || resp.Content[0].Text != "par") {
			t.Errorf("%s: partial %+v", name, resp)
		}
	}
	// a server that answers a streaming request with plain JSON still works
	srv, _ := fakeAPI(t, 200, "application/json", responsesJSON)
	plain := &Responses{BaseURL: srv.URL, Retry: noRetry}
	if resp, err := plain.Stream(context.Background(), Request{Model: "m"}, nil); err != nil || len(resp.Content) != 3 {
		t.Errorf("json fallback: %+v %v", resp, err)
	}
	srv2, _ := fakeAPI(t, 500, "application/json", `{"error":"down"}`)
	if _, err := (&Responses{BaseURL: srv2.URL, Retry: noRetry}).Stream(context.Background(), Request{Model: "m"}, nil); err == nil ||
		!strings.Contains(err.Error(), "HTTP 500: down") {
		t.Errorf("HTTP error: %v", err)
	}
}

func TestAzureUsesTheResourceDeploymentsKeyHeaderAndApiVersion(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "application/json", responsesJSON)
	vars := map[string]string{"AZURE_OPENAI_API_KEY": "fake-azure", "AZURE_OPENAI_BASE_URL": srv.URL + "/openai/v1",
		"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-5.4=my-gpt, other = dep2,junk", "AZURE_OPENAI_API_VERSION": "2025-04-01 preview"}
	r := &Router{Getenv: env(vars), Retry: noRetry}
	resp, err := r.Complete(context.Background(), Request{Model: "azure/gpt-5.4", Messages: toolRound()[:1]})
	if err != nil || len(resp.Content) != 3 {
		t.Fatalf("%+v %v", resp, err)
	}
	q := (*seen)[0]
	if q.Path != "/openai/v1/responses" || q.Query != "api-version=2025-04-01+preview" || q.Header.Get("api-key") != "fake-azure" ||
		q.Header.Get("authorization") != "" || q.Body["model"] != "my-gpt" {
		t.Errorf("azure request: %+v", q)
	}
	// the alias Pi used before, and a model with no deployment mapping, which is sent under its own name
	if _, err := r.Complete(context.Background(), Request{Model: "azure-openai-responses/unmapped"}); err != nil || (*seen)[1].Body["model"] != "unmapped" {
		t.Errorf("alias: %v %v", err, (*seen)[1].Body["model"])
	}
	delete(vars, "AZURE_OPENAI_API_VERSION")
	_, _ = r.Complete(context.Background(), Request{Model: "azure/gpt-5.4"})
	if (*seen)[2].Query != "api-version=v1" {
		t.Errorf("default version: %q", (*seen)[2].Query)
	}

	// the endpoint from the resource name, from models.json, and the error when there is none
	for name, c := range map[string]struct {
		vars   map[string]string
		custom map[string]ProviderConfig
		want   string
	}{
		"resource": {map[string]string{"AZURE_OPENAI_API_KEY": "k", "AZURE_OPENAI_RESOURCE_NAME": "contoso"}, nil,
			"https://contoso.openai.azure.com/openai/v1"},
		"models": {map[string]string{"AZURE_OPENAI_API_KEY": "k"}, map[string]ProviderConfig{"azure": {BaseURL: "https://x.example/openai/v1"}},
			"https://x.example/openai/v1"},
		"env wins": {map[string]string{"AZURE_OPENAI_API_KEY": "k", "AZURE_OPENAI_BASE_URL": "https://env.example/v1"},
			map[string]ProviderConfig{"azure": {BaseURL: "https://x.example/openai/v1"}}, "https://env.example/v1"},
	} {
		p, _, err := (&Router{Getenv: env(c.vars), Custom: c.custom}).Resolve("azure/m")
		if rp, ok := p.(*Responses); err != nil || !ok || rp.BaseURL != c.want {
			t.Errorf("%s: %T %v %v", name, p, err, p)
		}
	}
	p, _, _ := (&Router{Getenv: env(map[string]string{"AZURE_OPENAI_API_KEY": "k"})}).Resolve("azure/m")
	if m, ok := p.(Missing); !ok || !strings.Contains(m.Reason, "AZURE_OPENAI_BASE_URL") {
		t.Errorf("no endpoint: %T %v", p, p)
	}
}
