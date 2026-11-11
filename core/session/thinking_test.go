package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"koine/core/llm"
)

func TestThinkingBlocksArePersistedInPiShapeAndRestored(t *testing.T) {
	s := Create(t.TempDir(), "/proj")
	asst := llm.Message{Role: "assistant", ThinkingLevel: "high", Content: []llm.Block{
		{Type: "thinking", Thinking: "step one", Signature: "sigA"},
		{Type: "thinking", Redacted: true, Signature: "encrypted"},
		{Type: "thinking", Thinking: "no signature, other provider"},
		{Type: "text", Text: "answer"},
		{Type: "tool_use", ID: "t1", Name: "ls", Input: json.RawMessage(`{}`)}}}
	for _, m := range []llm.Message{text("user", "q"), asst} {
		if err := s.AppendMessage(m, "anthropic", "claude-sonnet-5-5"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(s.Path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var entry struct {
		Message struct {
			Content       []map[string]any `json:"content"`
			ThinkingLevel string           `json:"thinkingLevel"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatal(err)
	}
	c := entry.Message.Content
	if entry.Message.ThinkingLevel != "high" || len(c) != 5 {
		t.Fatalf("message: %s", lines[len(lines)-1])
	}
	if c[0]["type"] != "thinking" || c[0]["thinking"] != "step one" || c[0]["thinkingSignature"] != "sigA" || c[0]["redacted"] != nil {
		t.Errorf("signed block: %v", c[0])
	}
	if th, present := c[1]["thinking"]; !present || th != "" || c[1]["thinkingSignature"] != "encrypted" || c[1]["redacted"] != true {
		t.Errorf("redacted block keeps its empty text and payload: %v", c[1])
	}
	r, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Messages()
	if len(got) != 2 || got[1].ThinkingLevel != "high" {
		t.Fatalf("restored: %+v", got)
	}
	blocks := got[1].Content
	want := []llm.Block{{Type: "thinking", Thinking: "step one", Signature: "sigA"}, {Type: "thinking", Redacted: true, Signature: "encrypted"},
		{Type: "thinking", Thinking: "no signature, other provider"}}
	for i, w := range want {
		if blocks[i].Type != w.Type || blocks[i].Thinking != w.Thinking || blocks[i].Signature != w.Signature || blocks[i].Redacted != w.Redacted {
			t.Errorf("block %d: %+v, want %+v", i, blocks[i], w)
		}
	}
	if blocks[3].Text != "answer" || blocks[4].Type != "tool_use" {
		t.Errorf("order: %+v", blocks)
	}
}

func TestEmptyThinkingBlocksAreNotRestored(t *testing.T) {
	f := `{"type":"session","version":3,"id":"t","cwd":"/p"}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"q"}}` + "\n" +
		`{"type":"message","id":"m2","parentId":"m1","message":{"role":"assistant","content":` +
		`[{"type":"thinking","thinking":""},{"type":"thinking"},{"type":"text","text":"a"}]}}` + "\n"
	p := t.TempDir() + "/e.jsonl"
	if err := os.WriteFile(p, []byte(f), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Messages(); len(got) != 2 || len(got[1].Content) != 1 || got[1].Content[0].Text != "a" {
		t.Errorf("%+v", got)
	}
}
