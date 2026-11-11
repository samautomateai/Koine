package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"koine/core/llm"
)

func lastEntryMessage(t *testing.T, s *Session) map[string]any {
	t.Helper()
	b, _ := os.ReadFile(s.Path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var e struct {
		Message map[string]any `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e); err != nil {
		t.Fatal(err)
	}
	return e.Message
}

func TestImageBlocksAreStoredInPisShapeAndReplayed(t *testing.T) {
	s := Create(t.TempDir(), "/proj")
	user := llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "look"},
		llm.ImageBlock("image/png", "AAAA"), llm.ImageBlock("image/jpeg", "BBBB")}}
	if err := s.AppendMessage(user, "", "m"); err != nil {
		t.Fatal(err)
	}
	c := lastEntryMessage(t, s)["content"].([]any)
	if len(c) != 3 || c[1].(map[string]any)["type"] != "image" || c[1].(map[string]any)["data"] != "AAAA" || c[1].(map[string]any)["mimeType"] != "image/png" {
		t.Fatalf("Pi's ImageContent is {type, data, mimeType}: %v", c)
	}
	asst := llm.Message{Role: "assistant", Content: []llm.Block{{Type: "tool_use", ID: "t1", Name: "read", Input: json.RawMessage(`{"path":"a.png"}`)}}}
	res := llm.Message{Role: "user", Content: []llm.Block{{Type: "tool_result", ToolUseID: "t1", Content: "Read image file [image/png]",
		Images: []llm.Block{llm.ImageBlock("image/png", "CCCC")}}}}
	for _, m := range []llm.Message{asst, res} {
		if err := s.AppendMessage(m, "p", "m"); err != nil {
			t.Fatal(err)
		}
	}
	tr := lastEntryMessage(t, s)
	tc := tr["content"].([]any)
	if tr["role"] != "toolResult" || tr["toolName"] != "read" || len(tc) != 2 || tc[0].(map[string]any)["text"] != "Read image file [image/png]" ||
		tc[1].(map[string]any)["data"] != "CCCC" {
		t.Fatalf("tool result: %v", tr)
	}

	r, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Messages()
	if len(got) != 3 {
		t.Fatalf("messages: %+v", got)
	}
	u := got[0].Content
	if len(u) != 3 || u[0].Text != "look" || u[1].Type != "image" || u[1].MimeType() != "image/png" || u[1].Base64() != "AAAA" ||
		u[2].MimeType() != "image/jpeg" {
		t.Errorf("user message not replayed in order: %+v", u)
	}
	rb := got[2].Content[0]
	if rb.Type != "tool_result" || rb.Content != "Read image file [image/png]" || len(rb.Images) != 1 || rb.Images[0].Base64() != "CCCC" {
		t.Errorf("tool result images not replayed: %+v", rb)
	}
}

func TestAnImageOnlyUserMessageIsKeptAndImagesWithoutDataAreSkipped(t *testing.T) {
	s := Create(t.TempDir(), "/proj")
	if err := s.AppendMessage(llm.Message{Role: "user", Content: []llm.Block{llm.ImageBlock("image/gif", "RRRR")}}, "", "m"); err != nil {
		t.Fatal(err)
	}
	// a hand-edited or foreign file with an image block that has no data, and a Pi file with text beside images
	raw := `{"role":"user","content":[{"type":"image","mimeType":"image/png"},{"type":"text","text":"only text left"}]}`
	if err := s.appendEntry("message", func(e *Entry) { e.Message = json.RawMessage(raw) }); err != nil {
		t.Fatal(err)
	}
	got := s.Messages()
	if len(got) != 2 || got[0].Content[0].Type != "image" || got[1].Content[0].Text != "only text left" || len(got[1].Content) != 1 {
		t.Errorf("%+v", got)
	}
}

func TestImagesSurviveCloneByteForByte(t *testing.T) {
	dir := t.TempDir()
	s := Create(dir, "/proj")
	msg := llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "q"}, llm.ImageBlock("image/png", "ZZZZ")}}
	if err := s.AppendMessage(msg, "", "m"); err != nil {
		t.Fatal(err)
	}
	c, err := s.Fork(dir, "/proj")
	if err != nil {
		t.Fatal(err)
	}
	if m := c.Messages(); len(m) != 1 || len(m[0].Content) != 2 || m[0].Content[1].Base64() != "ZZZZ" {
		t.Errorf("clone: %+v", m)
	}
}
