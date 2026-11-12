package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// sigA and sigB are base64, as Gemini's signatures are.
const (
	sigA = "c2lnbmF0dXJlLWE="
	sigB = "c2lnbmF0dXJlLWI="
)

const geminiSignedJSON = `{"candidates":[{"content":{"role":"model","parts":[
 {"text":"pondering","thought":true},{"text":"Hello "},{"text":"there","thoughtSignature":"` + sigA + `"},{"text":" more"},
 {"functionCall":{"name":"ls","args":{}},"thoughtSignature":"` + sigB + `"},
 {"functionCall":{"name":"pwd","args":{}}},{"text":"","thoughtSignature":"` + sigA + `"}]},"finishReason":"STOP"}]}`

func TestGeminiKeepsThoughtSignaturesOnTextAndCalls(t *testing.T) {
	resp, err := parseGemini("google", []byte(geminiSignedJSON))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, b := range resp.Content {
		kinds = append(kinds, b.Type+":"+b.Text+b.Thinking+"|"+b.ThoughtSignature+b.Signature)
	}
	// the closing empty part's signature belongs to the block before it, a call, which cannot take it: it is dropped
	want := []string{"thinking:pondering|", "text:Hello there|" + sigA, "text: more|", "tool_use:|" + sigB, "tool_use:|"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("blocks:\n got %v\nwant %v", kinds, want)
	}
	// a signed part is never merged with what follows it, and a signature that arrives alone is attached to the block it ends
	resp, err = parseGemini("google", []byte(`{"candidates":[{"content":{"parts":[{"text":"a","thought":true},{"text":"","thoughtSignature":"`+sigA+
		`"},{"text":"b"},{"text":"","thoughtSignature":"`+sigB+`"}]}}]}`))
	if err != nil || len(resp.Content) != 2 || resp.Content[0].Signature != sigA || resp.Content[1].ThoughtSignature != sigB {
		t.Errorf("signatures alone: %+v %v", resp.Content, err)
	}
	// a signature with nothing to attach to is ignored
	if resp, err = parseGemini("google", []byte(`{"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"`+sigA+`"}]}}]}`)); err != nil ||
		len(resp.Content) != 0 {
		t.Errorf("orphan signature: %+v %v", resp, err)
	}
}

func TestGeminiStreamKeepsThoughtSignatures(t *testing.T) {
	sse := "data: " + `{"candidates":[{"content":{"parts":[{"text":"Hi","thoughtSignature":"` + sigA + `"}]}}]}` + "\n\n" +
		"data: " + `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"ls","args":{}},"thoughtSignature":"` + sigB +
		`"}]},"finishReason":"STOP"}]}` + "\n\n"
	resp, err := readGeminiStream("google", strings.NewReader(sse), func(StreamEvent) {})
	if err != nil || len(resp.Content) != 2 || resp.Content[0].ThoughtSignature != sigA || resp.Content[1].ThoughtSignature != sigB {
		t.Fatalf("%+v %v", resp, err)
	}
}

func replayedParts(t *testing.T, model string, msgs []Message) []map[string]any {
	t.Helper()
	srv, seen := fakeAPI(t, 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	g := &Gemini{APIKey: "k", BaseURL: srv.URL, Retry: noRetry}
	if _, err := g.Complete(context.Background(), Request{Model: model, Messages: msgs}); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, c := range (*seen)[0].Body["contents"].([]any) {
		if c.(map[string]any)["role"] == "model" {
			for _, p := range c.(map[string]any)["parts"].([]any) {
				out = append(out, p.(map[string]any))
			}
		}
	}
	return out
}

func TestGeminiReplaysThoughtSignaturesAndFillsInForGemini3(t *testing.T) {
	history := []Message{
		{Role: "user", Content: []Block{{Type: "text", Text: "go"}}},
		{Role: "assistant", Content: []Block{
			{Type: "thinking", Thinking: "plan", Signature: sigA},
			{Type: "thinking", Thinking: "unsigned thoughts"},
			{Type: "text", Text: "Looking", ThoughtSignature: sigB},
			{Type: "text", Text: "bad", ThoughtSignature: `{"id":"msg_1"}`}, // another provider's format: not base64, never sent
			{Type: "tool_use", ID: "c1", Name: "ls", Input: json.RawMessage(`{}`)},
			{Type: "tool_use", ID: "c2", Name: "pwd", Input: json.RawMessage(`{}`), ThoughtSignature: sigA}}},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "c1", Content: "a"}, {Type: "tool_result", ToolUseID: "c2", Content: "/"}}},
	}
	parts := replayedParts(t, "gemini-2.5-pro", history)
	if len(parts) != 5 || parts[0]["thought"] != true || parts[0]["thoughtSignature"] != sigA || parts[0]["text"] != "plan" ||
		parts[1]["thoughtSignature"] != sigB || parts[2]["thoughtSignature"] != nil || parts[3]["thoughtSignature"] != nil || parts[4]["thoughtSignature"] != sigA {
		t.Fatalf("gemini 2.5: %v", parts)
	}
	parts = replayedParts(t, "gemini-3-pro-preview", history)
	if parts[3]["thoughtSignature"] != "skip_thought_signature_validator" || parts[4]["thoughtSignature"] != sigA {
		t.Errorf("gemini 3 needs a signature on the first call of a step: %v", parts)
	}
}

func TestTransformDropsThoughtSignaturesOfAnotherModel(t *testing.T) {
	in := []Message{
		{Role: "user", Content: []Block{{Type: "text", Text: "go"}}},
		{Role: "assistant", Origin: "google/gemini-2.5-pro", Content: []Block{{Type: "text", Text: "hi", ThoughtSignature: sigA},
			{Type: "tool_use", ID: "c1", Name: "ls", Input: json.RawMessage(`{}`), ThoughtSignature: sigB}}},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "c1", Content: "a"}}},
	}
	out := TransformMessages(in, Target{API: APIGemini, SameOrigin: func(o string) bool { return o == "google/gemini-3-pro" }})
	for _, b := range out[1].Content {
		if b.ThoughtSignature != "" {
			t.Errorf("a signature of another model survived: %+v", b)
		}
	}
	if in[1].Content[0].ThoughtSignature != sigA {
		t.Error("the input must not change")
	}
	out = TransformMessages(in, Target{API: APIGemini, SameOrigin: func(string) bool { return true }})
	if out[1].Content[0].ThoughtSignature != sigA || out[1].Content[1].ThoughtSignature != sigB {
		t.Errorf("the same model keeps its signatures: %+v", out[1].Content)
	}
}
