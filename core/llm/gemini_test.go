package llm

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

const geminiJSON = `{"candidates":[{"content":{"role":"model","parts":[
 {"text":"pondering","thought":true},{"text":"Hello "},{"text":"there"},
 {"functionCall":{"name":"ls","args":{"path":"."}}}]},"finishReason":"STOP"}],
 "usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":5,"cachedContentTokenCount":30}}`

func TestGeminiRequestAndResponse(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "application/json", geminiJSON)
	g := &Gemini{Name: "google", APIKey: "fake-key", BaseURL: srv.URL + "/v1beta", Retry: noRetry, Headers: map[string]string{"x-team": "t"}}
	resp, err := g.Complete(context.Background(), Request{Model: "models/gemini-2.5-pro", System: "be brief", Messages: toolRound(), MaxTokens: 300,
		Thinking: &Thinking{Level: "medium"}, Tools: []ToolDef{{Name: "ls", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	r := (*seen)[0]
	if r.Path != "/v1beta/models/gemini-2.5-pro:generateContent" || r.Header.Get("x-goog-api-key") != "fake-key" || r.Header.Get("x-team") != "t" ||
		r.Header.Get("authorization") != "" {
		t.Errorf("request: %s %v", r.Path, r.Header)
	}
	b := r.Body
	if si := b["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any); si["text"] != "be brief" {
		t.Errorf("system: %v", si)
	}
	gen := b["generationConfig"].(map[string]any)
	tc := gen["thinkingConfig"].(map[string]any)
	if gen["maxOutputTokens"] != float64(300) || tc["includeThoughts"] != true || tc["thinkingBudget"] != float64(8192) {
		t.Errorf("generationConfig: %v", gen)
	}
	decl := b["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if decl["name"] != "ls" || decl["parametersJsonSchema"] == nil {
		t.Errorf("tools: %v", decl)
	}
	contents := b["contents"].([]any)
	var roles []string
	for _, c := range contents {
		roles = append(roles, c.(map[string]any)["role"].(string))
	}
	// user, model (thinking not replayed), user (function response), user (the tool's image), user text
	if strings.Join(roles, ",") != "user,model,user,user,user" {
		t.Errorf("roles: %v", roles)
	}
	model := contents[1].(map[string]any)["parts"].([]any)
	if len(model) != 2 || model[1].(map[string]any)["functionCall"].(map[string]any)["name"] != "ls" {
		t.Errorf("model parts: %v", model)
	}
	fr := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "ls" || fr["response"].(map[string]any)["output"] != "a.txt" {
		t.Errorf("function response: %v", fr)
	}
	img := contents[3].(map[string]any)["parts"].([]any)[1].(map[string]any)["inlineData"].(map[string]any)
	if img["mimeType"] != "image/png" || img["data"] != "AAAA" {
		t.Errorf("image: %v", img)
	}

	if len(resp.Content) != 3 || resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "pondering" || resp.Content[1].Text != "Hello there" {
		t.Fatalf("content: %+v", resp.Content)
	}
	call := resp.Content[2]
	if !regexp.MustCompile(`^call_[0-9a-f]{12}$`).MatchString(call.ID) || call.Name != "ls" || string(call.Input) != `{"path":"."}` {
		t.Errorf("call: %+v", call)
	}
	if resp.StopReason != "tool_calls" || resp.Usage != (Usage{Input: 70, Output: 25, CacheRead: 30}) {
		t.Errorf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
}

func TestGeminiThinkingOffBearerKeyAndStopReasons(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"cut"}]},"finishReason":"MAX_TOKENS"}]}`)
	g := &Gemini{APIKey: "fake-key", Bearer: true, BaseURL: srv.URL, Retry: noRetry}
	resp, err := g.Complete(context.Background(), Request{Model: "gemini-x", Thinking: &Thinking{Level: "off"}, Messages: toolRound()[:1]})
	if err != nil || resp.StopReason != "length" {
		t.Fatalf("%+v %v", resp, err)
	}
	r := (*seen)[0]
	if r.Header.Get("authorization") != "Bearer fake-key" || r.Header.Get("x-goog-api-key") != "" {
		t.Errorf("headers: %v", r.Header)
	}
	if tc := r.Body["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any); tc["thinkingBudget"] != float64(0) {
		t.Errorf("off: %v", tc)
	}
	if geminiBudget(&Thinking{Level: "max"}) != 24576 || geminiBudget(&Thinking{Level: "low", Budget: 77}) != 77 {
		t.Error("budgets")
	}
	srv2, _ := fakeAPI(t, 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"SAFETY"}]}`)
	if resp, _ := (&Gemini{BaseURL: srv2.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"}); resp.StopReason != "safety" {
		t.Errorf("stop: %q", resp.StopReason)
	}
}

func TestGeminiBlockedPromptsAndErrors(t *testing.T) {
	for body, want := range map[string]string{
		`{"promptFeedback":{"blockReason":"SAFETY"}}`:                     "blocked (SAFETY)",
		`{"error":{"status":"INVALID_ARGUMENT","message":"bad request"}}`: "INVALID_ARGUMENT bad request",
		`nonsense`:  "invalid character",
		`[nonsense`: "invalid character",
	} {
		srv, _ := fakeAPI(t, 200, "application/json", body)
		if _, err := (&Gemini{BaseURL: srv.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"}); err == nil ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", body, err)
		}
	}
	srv, _ := fakeAPI(t, 403, "application/json", `[{"error":{"code":403,"message":"API key not valid","status":"PERMISSION_DENIED"}}]`)
	_, err := (&Gemini{Name: "google", BaseURL: srv.URL, Retry: noRetry}).Complete(context.Background(), Request{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "API key not valid (PERMISSION_DENIED)") || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("403: %v", err)
	}
	// a streaming request answered with a JSON array of chunks (no SSE) is read chunk by chunk
	chunks := `[{"candidates":[{"content":{"parts":[{"text":"a"}]}}]},{"candidates":[{"content":{"parts":[{"text":"b"}]},"finishReason":"STOP"}]}]`
	srv2, _ := fakeAPI(t, 200, "application/json", chunks)
	resp, err := (&Gemini{BaseURL: srv2.URL, Retry: noRetry}).Stream(context.Background(), Request{Model: "m"}, nil)
	if err != nil || resp.Content[0].Text != "ab" {
		t.Errorf("array: %+v %v", resp, err)
	}
}

func TestGeminiStreamReportsDeltasAndKeepsPartialText(t *testing.T) {
	body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"ponder","thought":true}]}}]}` + "\n\n" +
		"data: " + `{"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}` + "\n\n" +
		"data: " + `{"candidates":[{"content":{"parts":[{"text":"lo"},{"functionCall":{"id":"fc1","name":"ls","args":{"a":1}}}]},"finishReason":"STOP"}],` +
		`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4}}` + "\n\n"
	srv, seen := fakeAPI(t, 200, "text/event-stream", body)
	var evs []string
	resp, err := Stream(context.Background(), &Gemini{BaseURL: srv.URL, Retry: noRetry}, Request{Model: "gemini-x"},
		func(e StreamEvent) { evs = append(evs, e.Type+":"+e.ID+e.Delta) })
	if err != nil {
		t.Fatal(err)
	}
	if (*seen)[0].Path != "/models/gemini-x:streamGenerateContent" || (*seen)[0].Query != "alt=sse" {
		t.Errorf("url: %s?%s", (*seen)[0].Path, (*seen)[0].Query)
	}
	if got := strings.Join(evs, ","); got != `thinking_delta:ponder,text_delta:Hel,text_delta:lo,tool_start:fc1,tool_delta:fc1{"a":1}` {
		t.Errorf("events: %s", got)
	}
	if len(resp.Content) != 3 || resp.Content[1].Text != "Hello" || resp.Content[2].ID != "fc1" || resp.Usage.Input != 10 || resp.Usage.Output != 4 {
		t.Errorf("response: %+v", resp)
	}

	// an error in the middle keeps the text, and drops the tool call that came with it
	bad := "data: " + `{"candidates":[{"content":{"parts":[{"text":"par"},{"functionCall":{"name":"ls"}}]}}]}` + "\n\n" +
		"data: " + `{"error":{"status":"UNAVAILABLE","message":"overloaded"}}` + "\n\n"
	srv2, _ := fakeAPI(t, 200, "text/event-stream", bad)
	resp, err = (&Gemini{BaseURL: srv2.URL, Retry: noRetry}).Stream(context.Background(), Request{Model: "m"}, func(StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "UNAVAILABLE overloaded") || len(resp.Content) != 1 || resp.Content[0].Text != "par" {
		t.Errorf("midstream error: %+v %v", resp, err)
	}
	for body, want := range map[string]string{"": "held no data", "data: {oops\n\n": "bad stream chunk"} {
		srv3, _ := fakeAPI(t, 200, "text/event-stream", body)
		if _, err := (&Gemini{BaseURL: srv3.URL, Retry: noRetry}).Stream(context.Background(), Request{Model: "m"}, nil); err == nil ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", body, err)
		}
	}
}
