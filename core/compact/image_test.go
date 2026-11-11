package compact

import (
	"strings"
	"testing"

	"koine/core/llm"
)

func TestImagesCountForTokensAndAppearInTheTranscript(t *testing.T) {
	plain := llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "look"}}}
	user := llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "look"}, llm.ImageBlock("image/png", "QUJD")}}
	result := llm.Block{Type: "tool_result", ToolUseID: "1", Content: "Read image file", Images: []llm.Block{llm.ImageBlock("image/jpeg", "QUJD")}}
	res := llm.Message{Role: "user", Content: []llm.Block{result}}
	if d := EstimateTokens([]llm.Message{user}) - EstimateTokens([]llm.Message{plain}); d != ImageTokens {
		t.Errorf("a user image adds %d tokens, want %d", d, ImageTokens)
	}
	if n := EstimateTokens([]llm.Message{res}); n < ImageTokens || n > ImageTokens+50 {
		t.Errorf("a tool result image: %d", n)
	}
	tr := Transcript([]llm.Message{user, res})
	if !strings.Contains(tr, "[User image: image/png]") || !strings.Contains(tr, "[Tool result image: 1 attached]") || strings.Contains(tr, "QUJD") {
		t.Errorf("transcript: %q", tr)
	}
}
