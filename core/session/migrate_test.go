package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const v1File = `{"type":"session","id":"old-1","timestamp":"2024-01-01T00:00:00Z","cwd":"/p"}
{"type":"message","timestamp":"2024-01-01T00:00:01Z","message":{"role":"user","content":"hello"}}
{"type":"message","timestamp":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
{"type":"message","timestamp":"2024-01-01T00:00:03Z","message":{"role":"user","content":"more"}}
{"type":"message","timestamp":"2024-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"sure"}]}}
{"type":"compaction","timestamp":"2024-01-01T00:00:05Z","summary":"earlier chat","firstKeptEntryIndex":2,"tokensBefore":99}
`

func TestOpenMigratesV1ToV3(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.jsonl")
	_ = os.WriteFile(p, []byte(v1File), 0o600)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Messages() // summary, then the kept "more" and "sure"
	if len(msgs) != 3 || !strings.Contains(msgs[0].Content[0].Text, "earlier chat") || msgs[1].Content[0].Text != "more" {
		t.Fatalf("messages after migration: %+v", msgs)
	}
	disk, _ := os.ReadFile(p)
	if !strings.Contains(string(disk), `"version":3`) || strings.Contains(string(disk), "firstKeptEntryIndex") {
		t.Errorf("file not rewritten as v3:\n%s", disk)
	}
	if bak, err := os.ReadFile(p + ".v1.bak"); err != nil || string(bak) != v1File {
		t.Errorf("backup missing or changed: %v", err)
	}
	// ids chain each entry to the previous one
	for i, e := range s.entries {
		if e.ID == "" || (i == 0) != (e.ParentID == nil) || (i > 0 && *e.ParentID != s.entries[i-1].ID) {
			t.Errorf("entry %d: id=%q parent=%v", i, e.ID, e.ParentID)
		}
	}
	// appending after migration keeps one version in the file, and a second open does not migrate again
	if err := s.AppendMessage(text("user", "next"), "", ""); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil || len(r.Messages()) != 4 {
		t.Fatalf("reopen: %v", err)
	}
}

func TestOpenMigratesV2HookMessages(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v2.jsonl")
	data := `{"type":"session","version":2,"id":"v2","timestamp":"t","cwd":"/p"}
{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"hookMessage","content":"x","customType":"c"}}
{"type":"message","id":"b","parentId":"a","timestamp":"t","message":{"role":"user","content":"keep"}}
`
	_ = os.WriteFile(p, []byte(data), 0o600)
	if _, err := Open(p); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(p)
	d := string(disk)
	if strings.Contains(d, "hookMessage") || !strings.Contains(d, `"role":"custom"`) || !strings.Contains(d, `"customType":"c"`) {
		t.Errorf("hookMessage not renamed:\n%s", disk)
	}
	if !strings.Contains(string(disk), `"id":"a"`) { // v2 ids are kept
		t.Errorf("ids changed:\n%s", disk)
	}
}

func TestListMigratesInMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "old.jsonl")
	_ = os.WriteFile(p, []byte(v1File), 0o600)
	l := List(dir)
	if len(l) != 1 || l[0].ID != "old-1" {
		t.Fatalf("list: %+v", l)
	}
	if disk, _ := os.ReadFile(p); string(disk) != v1File {
		t.Error("listing must not rewrite a file")
	}
}

func TestMigrationErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	_ = os.WriteFile(bad, []byte(`{"type":"session","version":1,"id":"x","cwd":"/"}`+"\n"+`not json`+"\n"), 0o600)
	if _, err := Open(bad); err == nil {
		t.Error("corrupt v1 line should fail")
	}
	// an unwritable target: the backup cannot be created
	ro := filepath.Join(dir, "missing", "f.jsonl")
	if err := replaceFile(ro, 1, []byte("a"), []byte("b")); err == nil {
		t.Error("replaceFile into a missing directory should fail")
	}
	// blank file and non-session first line are left for Open to report
	blank := filepath.Join(dir, "blank.jsonl")
	_ = os.WriteFile(blank, []byte("\n\n"), 0o600)
	if _, err := Open(blank); err == nil {
		t.Error("blank file")
	}
	notSession := filepath.Join(dir, "ns.jsonl")
	_ = os.WriteFile(notSession, []byte(`{"type":"message"}`+"\n"), 0o600)
	if _, err := Open(notSession); err == nil {
		t.Error("not a session")
	}
}
