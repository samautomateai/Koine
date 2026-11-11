package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/core/llm"
)

func text(role, t string) llm.Message {
	return llm.Message{Role: role, Content: []llm.Block{{Type: "text", Text: t}}}
}

func convo() []llm.Message {
	return []llm.Message{
		text("user", "list files"),
		{Role: "assistant", Content: []llm.Block{{Type: "text", Text: "ok"}, {Type: "tool_use", ID: "t1", Name: "ls", Input: json.RawMessage(`{"path":"."}`)},
			{Type: "tool_use", ID: "t2", Name: "read", Input: json.RawMessage(`{"path":"a"}`)}}},
		{Role: "user", Content: []llm.Block{
			{Type: "tool_result", ToolUseID: "t1", Content: "a b"}, {Type: "tool_result", ToolUseID: "t2", Content: "boom", IsError: true}}},
		text("assistant", "done"),
	}
}

func TestRoundTripKeepsToolCallsAndResultsTogether(t *testing.T) {
	dir := t.TempDir()
	s := Create(dir, "/proj")
	if _, err := os.Stat(s.Path); err == nil {
		t.Fatal("file must not exist before the first entry")
	}
	for _, m := range convo() {
		if err := s.AppendMessage(m, "anthropic", "m1"); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Messages()
	want := convo()
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	if res := got[2].Content; len(res) != 2 || res[1].ToolUseID != "t2" || !res[1].IsError || res[0].Content != "a b" {
		t.Errorf("tool results were not merged back into one user message: %+v", res)
	}
	if tu := got[1].Content[1]; tu.Type != "tool_use" || tu.Name != "ls" || string(tu.Input) != `{"path":"."}` {
		t.Errorf("tool call: %+v", tu)
	}
	if r.ID != s.ID || r.Cwd != "/proj" || r.Count() != 5 {
		t.Errorf("header or count: %+v count=%d", r, r.Count())
	}
	// the file is Pi's format: header first, toolResult entries carry the tool's name, entries chain by parentId
	b, _ := os.ReadFile(s.Path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if !strings.HasPrefix(lines[0], `{"type":"session","version":3,"id":"`) || len(lines) != 6 {
		t.Fatalf("file:\n%s", b)
	}
	isToolResult := strings.Contains(lines[3], `"toolName":"ls"`) && strings.Contains(lines[3], `"role":"toolResult"`)
	if !isToolResult || !strings.Contains(lines[2], `"stopReason":"toolUse"`) {
		t.Errorf("toolResult/assistant lines:\n%s\n%s", lines[2], lines[3])
	}
	var first, second Entry
	_ = json.Unmarshal([]byte(lines[1]), &first)
	_ = json.Unmarshal([]byte(lines[2]), &second)
	if first.ParentID != nil || second.ParentID == nil || *second.ParentID != first.ID {
		t.Errorf("parent chain: %+v %+v", first, second)
	}
}

const piFile = `{"type":"session","version":3,"id":"abc12345-0000-4000-8000-000000000000","cwd":"/p"}
{"type":"message","id":"a1","parentId":null,"message":{"role":"user","content":"first question","timestamp":1}}
{"type":"message","id":"a2","parentId":"a1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"first answer"}]}}
{"type":"message","id":"a3","parentId":"a2","message":{"role":"user","content":[{"type":"text","text":"second question"},{"type":"image","data":"x"}]}}
{"type":"message","id":"a4","parentId":"a3","message":{"role":"custom","customType":"x","content":"hook"}}
{"type":"label","id":"a5","parentId":"a4","targetId":"a1","label":"start"}
`

func TestReadsPiSessionsAndSkipsWhatItCannotRepresent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pi.jsonl")
	if err := os.WriteFile(p, []byte(piFile), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Messages()
	if len(got) != 3 || got[0].Content[0].Text != "first question" || got[1].Content[0].Text != "first answer" || got[2].Content[0].Text != "second question" {
		t.Fatalf("%+v", got)
	}
	// appending to a Pi file chains from its last entry and keeps the original lines
	if err := s.AppendMessage(text("user", "third"), "", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), piFile) || !strings.Contains(string(b), `"parentId":"a5"`) {
		t.Errorf("append must keep earlier lines and chain from the leaf:\n%s", b)
	}
}

func TestCompactionReplacesEarlierMessagesWithItsSummary(t *testing.T) {
	f := `{"type":"session","version":3,"id":"c","cwd":"/p"}
{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"old 1"}}
{"type":"message","id":"m2","parentId":"m1","message":{"role":"assistant","content":[{"type":"text","text":"old 2"}]}}
{"type":"message","id":"m3","parentId":"m2","message":{"role":"user","content":"kept"}}
{"type":"compaction","id":"c1","parentId":"m3","summary":"SUM","firstKeptEntryId":"m3","tokensBefore":9}
{"type":"message","id":"m4","parentId":"c1","message":{"role":"assistant","content":[{"type":"text","text":"new"}]}}
`
	p := filepath.Join(t.TempDir(), "c.jsonl")
	_ = os.WriteFile(p, []byte(f), 0o600)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	m := s.Messages()
	if len(m) != 3 || !strings.Contains(m[0].Content[0].Text, "SUM") || m[1].Content[0].Text != "kept" || m[2].Content[0].Text != "new" {
		t.Fatalf("%+v", m)
	}
}

func TestOpenRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"notheader.jsonl": `{"type":"message"}` + "\n",
		"newer.jsonl":     `{"type":"session","version":9,"id":"x","cwd":"/"}` + "\n",
		"badline.jsonl":   `{"type":"session","version":3,"id":"x","cwd":"/"}` + "\nnot json\n",
		"empty.jsonl":     "\n",
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		if _, err := Open(p); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := Open(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("missing file: want an error")
	}
}

func TestNameModelAndFork(t *testing.T) {
	dir := t.TempDir()
	s := Create(dir, "/p")
	for _, m := range convo() {
		_ = s.AppendMessage(m, "", "")
	}
	if err := s.SetName("refactor"); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordModel("anthropic", "m9")
	if s.Name() != "refactor" || s.Model() != "m9" {
		t.Fatalf("%q %q", s.Name(), s.Model())
	}
	f, err := s.Fork(dir, "/p")
	if err != nil {
		t.Fatal(err)
	}
	if f.Parent != s.Path || f.ID == s.ID || len(f.Messages()) != 4 || f.Name() != "" {
		t.Errorf("fork: parent=%q msgs=%d name=%q", f.Parent, len(f.Messages()), f.Name())
	}
	r, _ := Open(f.Path)
	if r.Parent != s.Path || len(r.Messages()) != 4 {
		t.Errorf("reopened fork: %q %d", r.Parent, len(r.Messages()))
	}
}

func TestStoreListLatestResolve(t *testing.T) {
	root := t.TempDir()
	dir := DirFor(root, "/home/me/proj")
	if filepath.Base(dir) != "--home-me-proj--" {
		t.Fatalf("dir %s", dir)
	}
	if got := filepath.Base(DirFor(root, `C:\x\y`)); got != "--C--x-y--" {
		t.Errorf("windows path: %s", got)
	}
	if _, err := Latest(dir); err == nil {
		t.Error("no sessions yet: want an error")
	}
	a, b := Create(dir, "/p"), Create(dir, "/p")
	_ = a.AppendMessage(text("user", "alpha   question\nwith breaks"), "", "")
	_ = a.SetName("first")
	_ = b.AppendMessage(text("user", strings.Repeat("long ", 40)), "", "")
	_ = os.WriteFile(filepath.Join(dir, "junk.jsonl"), []byte("x"), 0o600)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(a.Path, old, old)

	l := List(dir)
	if len(l) != 2 || l[0].ID != b.ID || l[1].Name != "first" || l[1].Preview != "alpha question with breaks" || l[0].Preview[len(l[0].Preview)-3:] != "…" {
		t.Fatalf("%+v", l)
	}
	if s, err := Latest(dir); err != nil || s.ID != b.ID {
		t.Errorf("latest: %v %v", s, err)
	}
	for _, ref := range []string{a.ID, a.ID[:8], "first", a.Path} {
		if s, err := Resolve(dir, ref); err != nil || s.ID != a.ID {
			t.Errorf("resolve %q: %v", ref, err)
		}
	}
	if _, err := Resolve(dir, "nope"); err == nil {
		t.Error("unknown ref: want an error")
	}
	if _, err := Resolve(dir, "0"); err == nil {
		t.Error("too-short prefix must not match")
	}
}
