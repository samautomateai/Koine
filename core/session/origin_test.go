package session

import (
	"testing"

	"koine/core/llm"
)

// TestRestoredAssistantMessagesRememberTheirModel is what lets a later model switch know which thinking blocks it cannot replay.
func TestRestoredAssistantMessagesRememberTheirModel(t *testing.T) {
	s := Create(t.TempDir(), "/p")
	for _, c := range []struct {
		m               llm.Message
		provider, model string
	}{
		{text("user", "q"), "", "ignored"},
		{text("assistant", "a"), "", "claude-sonnet-5-5"},          // Koine's own files: the model as selected
		{text("assistant", "b"), "openai", "gpt-5"},                // Pi's files: provider and model apart
		{text("assistant", "c"), "openrouter", "anthropic/claude"}, // the model id may hold a slash
	} {
		if err := s.AppendMessage(c.m, c.provider, c.model); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Messages()
	want := []string{"", "claude-sonnet-5-5", "openai/gpt-5", "openrouter/anthropic/claude"}
	for i, w := range want {
		if got[i].Origin != w {
			t.Errorf("message %d origin = %q, want %q", i, got[i].Origin, w)
		}
	}
}
