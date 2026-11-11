package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCustomEntriesThinkingLevelAndTheAppendHook(t *testing.T) {
	s := Create(t.TempDir(), "/proj")
	var seen []Entry
	s.OnAppend = func(e Entry) { seen = append(seen, e) }

	if err := s.AppendCustom("my-ext", json.RawMessage(`{"count":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordThinkingLevel("high"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetName("named"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCustom("", nil); err == nil {
		t.Error("an empty customType should be refused")
	}
	if err := s.AppendCustom("x", json.RawMessage(`{broken`)); err == nil {
		t.Error("data that is not JSON should be refused")
	}
	if len(seen) != 3 || seen[0].Type != "custom" || seen[1].Type != "thinking_level_change" || seen[2].Type != "session_info" {
		t.Fatalf("hook saw %+v", seen)
	}
	if seen[1].ParentID == nil || *seen[1].ParentID != seen[0].ID {
		t.Errorf("entries should chain: %+v", seen[1])
	}
	// the JSON the hook consumers get is the line in the file
	var line map[string]any
	if err := json.Unmarshal(seen[0].JSON(), &line); err != nil || line["customType"] != "my-ext" || line["data"].(map[string]any)["count"] != float64(2) {
		t.Errorf("entry json %v %v", line, err)
	}
	b, _ := os.ReadFile(s.Path)
	if !strings.Contains(string(b), `"customType":"my-ext"`) || !strings.Contains(string(b), `"thinkingLevel":"high"`) {
		t.Errorf("file: %s", b)
	}
	// they survive a reload and never become conversation
	again, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Messages()) != 0 || again.Name() != "named" {
		t.Errorf("reloaded: %v %q", again.Messages(), again.Name())
	}
	again.OnAppend = nil
	if err := again.SetName("quiet"); err != nil {
		t.Fatal(err)
	}
}
