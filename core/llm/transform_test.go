package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func toolUse(id string) Block {
	return Block{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(`{}`)}
}
func toolResult(id string) Block {
	return Block{Type: "tool_result", ToolUseID: id, Content: "ok"}
}
func text(s string) Block { return Block{Type: "text", Text: s} }
func asst(origin string, bs ...Block) Message {
	return Message{Role: "assistant", Content: bs, Origin: origin}
}
func user(bs ...Block) Message { return Message{Role: "user", Content: bs} }

func anthropicTarget(same string) Target {
	return Target{API: APIAnthropic, Provider: "anthropic", SameOrigin: func(o string) bool { return o == same }}
}

func TestTransformConvertsThinkingOfOtherModels(t *testing.T) {
	hist := []Message{user(text("hi")),
		asst("openai/gpt-x", Block{Type: "thinking", Thinking: "plan A"}, Block{Type: "thinking", Redacted: true, Signature: "enc"}, text("done")),
		user(text("again")),
		asst("anthropic/claude-x", Block{Type: "thinking", Thinking: "mine", Signature: "sig"}, text("ok")),
		asst("", Block{Type: "thinking", Thinking: "unknown origin", Signature: "s2"}),
		asst("openai/gpt-x", Block{Type: "thinking", Redacted: true, Signature: "only"}),
	}
	got := TransformMessages(hist, anthropicTarget("anthropic/claude-x"))
	if len(got) != 5 { // the message that only held a redacted block is gone
		t.Fatalf("messages = %d: %+v", len(got), got)
	}
	if b := got[1].Content; len(b) != 2 || b[0].Type != "text" || b[0].Text != "plan A" || b[1].Text != "done" {
		t.Errorf("foreign thinking should become text and the redacted block vanish: %+v", b)
	}
	if b := got[3].Content[0]; b.Type != "thinking" || b.Signature != "sig" {
		t.Errorf("thinking of the same model is kept: %+v", b)
	}
	if b := got[4].Content[0]; b.Type != "thinking" || b.Signature != "s2" {
		t.Errorf("thinking of unknown origin is left alone: %+v", b)
	}
}

func TestTransformDoesNotChangeItsInput(t *testing.T) {
	hist := []Message{user(text("hi")), asst("other/m", Block{Type: "thinking", Thinking: "t"}, toolUse("call|1")),
		user(toolResult("call|1"), toolResult("stray"))}
	before, _ := json.Marshal(hist)
	got := TransformMessages(hist, anthropicTarget("anthropic/claude-x"))
	if after, _ := json.Marshal(hist); string(before) != string(after) {
		t.Errorf("input was modified:\n%s\n%s", before, after)
	}
	if hist[1].Content[1].ID != "call|1" || got[1].Content[1].ID == "call|1" {
		t.Errorf("only the copy should be renamed: %q %q", hist[1].Content[1].ID, got[1].Content[1].ID)
	}
	if hist[1].Content[0].Type != "thinking" || got[1].Content[0].Type != "text" {
		t.Error("thinking conversion leaked into the input")
	}
}

func TestTransformNormalisesToolCallIDs(t *testing.T) {
	long := strings.Repeat("x", 100)
	hist := []Message{user(text("go")),
		asst("", toolUse("call_ok-1"), toolUse("fc_1|call 2"), toolUse(long), toolUse("a.b"), toolUse("a_b")),
		user(toolResult("call_ok-1"), toolResult("fc_1|call 2"), toolResult(long), toolResult("a.b"), toolResult("a_b"))}
	got := TransformMessages(hist, anthropicTarget(""))
	seen := map[string]bool{}
	for i, b := range got[1].Content {
		if !(idRule{max: 64}).valid(b.ID) {
			t.Errorf("call %d id %q is not acceptable", i, b.ID)
		}
		if seen[b.ID] {
			t.Errorf("id %q used twice", b.ID)
		}
		seen[b.ID] = true
		if got[2].Content[i].ToolUseID != b.ID {
			t.Errorf("result %d answers %q, not %q", i, got[2].Content[i].ToolUseID, b.ID)
		}
	}
	if got[1].Content[0].ID != "call_ok-1" {
		t.Errorf("a valid id must stay: %q", got[1].Content[0].ID)
	}
	again := TransformMessages(hist, anthropicTarget(""))
	if !reflect.DeepEqual(got, again) {
		t.Error("renaming is not deterministic")
	}
	// converting the converted history changes nothing
	if twice := TransformMessages(got, anthropicTarget("")); !reflect.DeepEqual(got, twice) {
		t.Error("transform is not idempotent")
	}
}

func TestTransformIDRulesPerProvider(t *testing.T) {
	long := "call_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH"
	hist := []Message{user(text("go")), asst("", toolUse(long)), user(toolResult(long))}
	id := func(tg Target) string { return TransformMessages(hist, tg)[1].Content[0].ID }
	if got := id(Target{API: APICompletions, Provider: "openai"}); len(got) > 40 || got == "" {
		t.Errorf("chat completions ids are at most 40 characters: %q", got)
	}
	got := id(Target{API: APICompletions, Provider: "mistral"})
	if len(got) != 9 || !(idRule{max: 9, exact: true}).valid(got) {
		t.Errorf("mistral wants 9 alphanumeric characters: %q", got)
	}
	if got := id(Target{API: APIGemini}); got != long {
		t.Errorf("gemini does not send ids: %q", got)
	}
	for _, api := range []string{APIBedrock, APIResponses, APIAzure} {
		if got := id(Target{API: api}); len(got) > 64 {
			t.Errorf("%s: %q", api, got)
		}
	}
	// two mistral ids that hash alike would collide only with a 36^9 chance; the salt path is covered by a forced collision
	r := idRule{max: 9, exact: true}
	if r.fix("x", 0) == r.fix("x", 1) {
		t.Error("a salt must change the id")
	}
	if (idRule{max: 64}).valid("") || (idRule{max: 9, exact: true}).valid("abc") {
		t.Error("empty and short exact ids are invalid")
	}
	if got := (idRule{max: 64}).fix("", 0); !(idRule{max: 64}).valid(got) {
		t.Errorf("an empty id still gets a valid one: %q", got)
	}
}

func TestTransformRepairsOrphanToolCallsAndResults(t *testing.T) {
	hist := []Message{
		user(toolResult("nobody")), // a result with no call before it
		user(text("start")),
		asst("", toolUse("a"), toolUse("b")),
		user(text("interrupt"), toolResult("b"), toolResult("b"), toolResult("zzz")), // duplicate, stray, and the result for a is missing
		asst("", text("fine"), toolUse("c")),                                         // last message: its result never arrived
	}
	got := TransformMessages(hist, anthropicTarget(""))
	if len(got) != 5 {
		t.Fatalf("messages = %d: %+v", len(got), got)
	}
	if got[0].Content[0].Text != "start" {
		t.Errorf("stray result message should be gone: %+v", got[0])
	}
	u := got[2].Content
	if len(u) != 3 || u[0].ToolUseID != "b" || u[0].Content != "ok" || u[1].ToolUseID != "a" || !u[1].IsError || u[2].Text != "interrupt" {
		t.Errorf("results must come first, once each, missing ones added: %+v", u)
	}
	if last := got[4]; last.Role != "user" || len(last.Content) != 1 || last.Content[0].ToolUseID != "c" || !last.Content[0].IsError {
		t.Errorf("a trailing call gets an error result: %+v", got[4])
	}
	if len(TransformMessages(nil, anthropicTarget(""))) != 0 {
		t.Error("empty history")
	}
}

// TestRouterConvertsHistoryAcrossProviders switches from an OpenAI-style history to Anthropic and checks what goes over the wire.
func TestRouterConvertsHistoryAcrossProviders(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	r := &Router{Getenv: func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "fake-key"
		}
		return ""
	}, Custom: map[string]ProviderConfig{"anthropic": {BaseURL: srv.URL}}}
	hist := []Message{user(text("hi")),
		asst("openai/gpt-x", Block{Type: "thinking", Thinking: "secret plan", Signature: "openai-sig"}, toolUse("call_1|fc_9")),
		user(toolResult("call_1|fc_9")), asst("openai/gpt-x", text("done")), user(text("next"))}
	if _, err := r.Stream(context.Background(), Request{Model: "anthropic/claude-sonnet-5-5", Messages: hist}, nil); err != nil {
		t.Fatal(err)
	}
	wire := mustJSON(body["messages"])
	if strings.Contains(wire, `"type":"thinking"`) || strings.Contains(wire, "openai-sig") || strings.Contains(wire, "|") {
		t.Errorf("foreign thinking and the odd id must not reach Anthropic: %s", wire)
	}
	if !strings.Contains(wire, "secret plan") {
		t.Errorf("readable thinking should survive as text: %s", wire)
	}
	if hist[1].Content[1].ID != "call_1|fc_9" {
		t.Error("the session history must not be rewritten")
	}
	// the same model keeps its signed thinking
	hist = []Message{user(text("hi")), asst("claude-sonnet-5-5", Block{Type: "thinking", Thinking: "own", Signature: "own-sig"}, text("x")), user(text("more"))}
	if _, err := r.Complete(context.Background(), Request{Model: "anthropic/claude-sonnet-5-5", Messages: hist}); err != nil {
		t.Fatal(err)
	}
	if wire = mustJSON(body["messages"]); !strings.Contains(wire, "own-sig") {
		t.Errorf("the model's own thinking must be replayed: %s", wire)
	}
}
