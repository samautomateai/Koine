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

const tinyPNG = "iVBORw0KGgo="

func userWithImage() Message {
	return Message{Role: "user", Content: []Block{{Type: "text", Text: "what is this?"}, ImageBlock("image/png", tinyPNG)}}
}

func toolResultWithImage() Message {
	r := Block{Type: "tool_result", ToolUseID: "t1", Content: "Read image file [image/png]", Images: []Block{ImageBlock("image/jpeg", "/9j/")}}
	return Message{Role: "user", Content: []Block{r}}
}

// capture runs fn against a fake server that records the request body and answers with reply.
func capture(t *testing.T, reply string, fn func(url string)) map[string]any {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	fn(srv.URL)
	return got
}

func TestAnthropicSendsImageSourceBlocks(t *testing.T) {
	body := capture(t, `{"content":[{"type":"text","text":"a pixel"}],"stop_reason":"end_turn"}`, func(url string) {
		a := &Anthropic{APIKey: "k", BaseURL: url}
		hist := []Message{userWithImage(), {Role: "assistant", Content: []Block{{Type: "tool_use", ID: "t1", Name: "read"}}}, toolResultWithImage()}
		if _, err := a.Complete(context.Background(), Request{Model: "m", Messages: hist}); err != nil {
			t.Fatal(err)
		}
	})
	msgs := body["messages"].([]any)
	img := msgs[0].(map[string]any)["content"].([]any)[1].(map[string]any)
	src := img["source"].(map[string]any)
	if img["type"] != "image" || src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != tinyPNG {
		t.Errorf("user image block: %v", img)
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	parts, ok := res["content"].([]any)
	if res["type"] != "tool_result" || !ok || len(parts) != 2 {
		t.Fatalf("a tool result with images has a content array: %v", res)
	}
	if parts[0].(map[string]any)["text"] != "Read image file [image/png]" ||
		parts[1].(map[string]any)["source"].(map[string]any)["media_type"] != "image/jpeg" {
		t.Errorf("tool result parts: %v", parts)
	}
	if res["cache_control"] == nil {
		t.Error("the last user block keeps its cache breakpoint")
	}
}

func TestToolResultWithoutImagesStaysAString(t *testing.T) {
	b, err := json.Marshal(Block{Type: "tool_result", ToolUseID: "t", Content: "plain"})
	if err != nil || !strings.Contains(string(b), `"content":"plain"`) {
		t.Errorf("%s %v", b, err)
	}
	b, _ = json.Marshal(Block{Type: "tool_result", ToolUseID: "t", Images: []Block{ImageBlock("image/png", "x")}})
	if strings.Contains(string(b), `"type":"text"`) {
		t.Errorf("an empty text part must not be written: %s", b)
	}
}

func TestOpenAISendsImageURLParts(t *testing.T) {
	body := capture(t, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, func(url string) {
		o := &OpenAICompat{BaseURL: url}
		hist := []Message{userWithImage(), {Role: "assistant", Content: []Block{{Type: "tool_use", ID: "t1", Name: "read"}}}, toolResultWithImage()}
		if _, err := o.Complete(context.Background(), Request{Model: "m", Messages: hist}); err != nil {
			t.Fatal(err)
		}
	})
	msgs := body["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("want user, assistant, tool, image user: %v", msgs)
	}
	parts := msgs[0].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["type"] != "text" || parts[1].(map[string]any)["type"] != "image_url" ||
		parts[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,"+tinyPNG {
		t.Errorf("user parts: %v", parts)
	}
	if tool := msgs[2].(map[string]any); tool["role"] != "tool" || tool["content"] != "Read image file [image/png]" {
		t.Errorf("the tool message stays text: %v", tool)
	}
	follow := msgs[3].(map[string]any)
	fp := follow["content"].([]any)
	if follow["role"] != "user" || !strings.Contains(fp[0].(map[string]any)["text"].(string), "image(s) from tool result") ||
		fp[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/jpeg;base64,/9j/" {
		t.Errorf("tool result images follow as a user message: %v", follow)
	}
}

func TestOpenAITextOnlyUserMessageIsStillAString(t *testing.T) {
	msgs := toOpenAI("", []Message{{Role: "user", Content: []Block{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}}})
	if s, ok := msgs[0].Content.(*string); !ok || *s != "a\nb" {
		t.Errorf("%#v", msgs[0].Content)
	}
}

func TestStripImagesReplacesThemWithANoteAndLeavesTheHistoryAlone(t *testing.T) {
	hist := []Message{userWithImage(), toolResultWithImage(), {Role: "assistant", Content: []Block{{Type: "text", Text: "ok"}}}}
	if CountImages(hist) != 2 {
		t.Fatalf("count %d", CountImages(hist))
	}
	out, n := StripImages(hist, "because")
	if n != 2 || CountImages(out) != 0 || CountImages(hist) != 2 {
		t.Fatalf("stripped %d, left %d, history now %d", n, CountImages(out), CountImages(hist))
	}
	if out[0].Content[1].Text != "[image omitted: because]" || !strings.Contains(out[1].Content[0].Content, "[image omitted: because]") ||
		!strings.HasPrefix(out[1].Content[0].Content, "Read image file") {
		t.Errorf("notes: %+v %+v", out[0].Content, out[1].Content)
	}
	same, none := StripImages(out, "x")
	if none != 0 || len(same) != len(out) {
		t.Error("nothing to strip")
	}
}

func TestAcceptsImagesFollowsTheCatalogAndAllowsUnknownModels(t *testing.T) {
	c := &Catalog{Models: []Model{
		{ID: "vision", Input: []string{"text", "image"}}, {ID: "texty", Input: []string{"text"}}, {ID: "unlisted"},
	}}
	for id, want := range map[string]bool{"vision": true, "texty": false, "unlisted": true, "never-heard-of-it": true, "p/texty": false} {
		if got := c.AcceptsImages(id); got != want {
			t.Errorf("AcceptsImages(%q) = %v, want %v", id, got, want)
		}
	}
	var nilCat *Catalog
	if !nilCat.AcceptsImages("x") {
		t.Error("no catalog = allow")
	}
	if !DefaultCatalog().AcceptsImages("claude-sonnet-5-5") || NoImagesWhy("m") == "" {
		t.Error("the built-in Claude models take images")
	}
	if m, _ := DefaultCatalog().Lookup("claude-haiku-5-5"); len(m.Input) != 2 {
		t.Errorf("built-in input list: %v", m.Input)
	}
}

func TestImageBlockAccessors(t *testing.T) {
	b := ImageBlock("image/gif", "AAA")
	if b.MimeType() != "image/gif" || b.Base64() != "AAA" || (Block{Type: "text"}).MimeType() != "" || (Block{}).Base64() != "" {
		t.Errorf("%+v", b)
	}
}
