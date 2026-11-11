package session

import (
	"os"
	"strings"
	"testing"

	"koine/core/llm"
)

func TestAbortedAssistantMessageIsSavedWithPisStopReasonAndRestored(t *testing.T) {
	s := Create(t.TempDir(), "/p")
	aborted := text("assistant", "half an ans")
	aborted.StopReason = "aborted"
	for _, x := range []llm.Message{text("user", "q"), aborted, text("user", "again"), text("assistant", "fine")} {
		if err := s.AppendMessage(x, "anthropic", "m1"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(s.Path)
	if !strings.Contains(string(b), `"stopReason":"aborted"`) || strings.Count(string(b), `"stopReason":"stop"`) != 1 {
		t.Errorf("stop reasons:\n%s", b)
	}
	r, _ := Open(s.Path)
	got := r.Messages()
	if len(got) != 4 || got[1].StopReason != "aborted" || got[3].StopReason != "" {
		t.Errorf("restored: %+v", got)
	}
}
