package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treePiFile is a hand-written Pi v3 file: a user/assistant exchange, a custom entry with fields Koine does not know, a thinking block
// and an image block. The odd spacing and key order must survive appends and copies.
const treePiFile = `{"type":"session","version":3,"id":"sid-1","timestamp":"2025-01-01T00:00:00.000Z","cwd":"/p"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"user","content":"first","timestamp":1}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2025-01-01T00:00:02.000Z","message":{"role":"assistant","content":[` +
	`{"type":"thinking","thinking":"hm","thinkingSignature":"sig"},{"type":"text","text":"answer one"}],"provider":"x","model":"m","timestamp":2}}
{ "id":"c1", "type":"custom",  "parentId":"a1","timestamp":"2025-01-01T00:00:03.000Z","customType":"my-ext","data":{"keep":[1,2,3]},"futureField":true}
{"type":"message","id":"u2","parentId":"c1","timestamp":"2025-01-01T00:00:04.000Z","message":{"role":"user","content":[` +
	`{"type":"text","text":"second"},{"type":"image","data":"QUJD","mimeType":"image/png"}],"timestamp":3}}
{"type":"message","id":"a2","parentId":"u2","timestamp":"2025-01-01T00:00:05.000Z","message":{"role":"assistant","content":[` +
	`{"type":"text","text":"answer two"}],"timestamp":4}}`

func writePi(t *testing.T, trailingNewline bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pi.jsonl")
	data := treePiFile
	if trailingNewline {
		data += "\n"
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAppendKeepsExistingBytesAndAddsMissingNewline(t *testing.T) {
	for _, nl := range []bool{true, false} {
		p := writePi(t, nl)
		s, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetLabel("a1", "mark"); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendMessage(text("user", "third"), "", ""); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(p)
		if !bytes.HasPrefix(got, []byte(treePiFile)) {
			t.Fatalf("newline=%v: the Pi file's bytes changed:\n%s", nl, got)
		}
		r, err := Open(p)
		if err != nil {
			t.Fatalf("newline=%v: appended file no longer parses: %v", nl, err)
		}
		if n, _, _ := r.Stats(); n != 7 || r.Labels()["a1"] != "mark" {
			t.Errorf("newline=%v: entries=%d labels=%v", nl, n, r.Labels())
		}
		if got := len(r.Messages()); got != 5 { // u1 a1(text) u2 a2 + new user
			t.Errorf("newline=%v: messages=%d", nl, got)
		}
	}
}

func TestBranchingNavigationAndSummary(t *testing.T) {
	s, err := Open(writePi(t, true))
	if err != nil {
		t.Fatal(err)
	}
	// moving to a user message continues from its parent and hands the text back
	u2, _ := s.Find("u2")
	leaf, txt := Destination(u2)
	if leaf != "c1" || txt != "second" {
		t.Fatalf("destination %q %q", leaf, txt)
	}
	left, anc := s.Abandoned(leaf)
	if anc != "c1" || len(left) != 2 || left[0].ID != "u2" || left[1].ID != "a2" {
		t.Fatalf("abandoned %v anc %q", left, anc)
	}
	s.SetLeaf(leaf)
	if err := s.AddBranchSummary("a2", "tried the second approach"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(text("user", "alternative"), "", ""); err != nil {
		t.Fatal(err)
	}
	msgs := s.Messages()
	if len(msgs) != 4 || !strings.Contains(msgs[2].Content[0].Text, "tried the second approach") || msgs[3].Content[0].Text != "alternative" {
		t.Fatalf("branch context: %+v", msgs)
	}
	// both branches are still in the file and reachable
	if _, leaves, _ := s.Stats(); leaves != 2 {
		t.Errorf("leaves=%d, want 2", leaves)
	}
	r, _ := Open(s.Path)
	if len(r.Messages()) != 4 {
		t.Errorf("reopened: %d messages", len(r.Messages()))
	}
	// moving to a root-level user message empties the context
	u1, _ := s.Find("u1")
	if l, _ := Destination(u1); l != "" {
		t.Errorf("root user message: leaf %q", l)
	}
	s.SetLeaf("")
	if len(s.Messages()) != 0 {
		t.Error("empty leaf should give no messages")
	}
	if err := s.AppendMessage(text("user", "new root"), "", ""); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Entry(s.Leaf()); e.ParentID != nil {
		t.Error("an entry added at the empty leaf must be a new root")
	}
	// moving to an assistant message keeps it
	a1, _ := s.Find("a1")
	if l, tx := Destination(a1); l != "a1" || tx != "" {
		t.Errorf("assistant destination %q %q", l, tx)
	}
	if rest, anc := s.Abandoned("nonexistent"); anc != "" || len(rest) != len(s.path()) {
		t.Errorf("no common ancestor: %d %q", len(rest), anc)
	}
	if MessagesOf(nil) != nil {
		t.Error("MessagesOf(nil)")
	}
}

func TestLabelsFindAndRows(t *testing.T) {
	s, _ := Open(writePi(t, true))
	if err := s.SetLabel("zzz", "x"); err == nil {
		t.Error("labelling a missing entry should fail")
	}
	_ = s.SetLabel("a1", "checkpoint")
	_ = s.SetLabel("u2", "second")
	_ = s.SetLabel("u2", "") // cleared
	if l := s.Labels(); len(l) != 1 || l["a1"] != "checkpoint" {
		t.Fatalf("labels %v", l)
	}
	if e, err := s.Find("checkpoint"); err != nil || e.ID != "a1" {
		t.Errorf("find by label: %v %v", e.ID, err)
	}
	if e, err := s.Find("a2"); err != nil || e.ID != "a2" {
		t.Errorf("find by id: %v", err)
	}
	if _, err := s.Find("nope"); err == nil {
		t.Error("unknown ref")
	}
	if _, err := s.Find(""); err == nil {
		t.Error("empty ref")
	}
	if _, err := s.Find("u"); err == nil { // u is one character: no prefix matching
		t.Error("one-character prefix must not match")
	}
	_ = s.AppendMessage(text("assistant", "x"), "", "")
	ids := func(mode string) string {
		var out []string
		for _, r := range s.Rows(mode) {
			out = append(out, r.Entry.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(FilterAll); !strings.HasPrefix(got, "u1,a1,c1,u2,a2,") || strings.Count(got, ",") != 8 {
		t.Errorf("all: %s", got)
	}
	// entry ids are random hex, so match whole ids: a substring check on "c1" fails whenever a random id happens to contain it
	if got := ids(FilterDefault); strings.Contains(","+got+",", ",c1,") || strings.Count(got, ",") != 4 { // no custom, no label entries
		t.Errorf("default: %s", got)
	}
	if got := ids(FilterLabeledOnly); got != "a1" {
		t.Errorf("labeled-only: %s", got)
	}
	if got := ids(FilterUserOnly); got != "u1,u2" {
		t.Errorf("user-only: %s", got)
	}
	if !ValidFilter("no-tools") || ValidFilter("bogus") {
		t.Error("ValidFilter")
	}
	rows := s.Rows(FilterAll)
	last := rows[len(rows)-1]
	if !last.IsLeaf || !last.OnPath || rows[1].Label != "checkpoint" {
		t.Errorf("rows: leaf=%v onpath=%v label=%q", last.IsLeaf, last.OnPath, rows[1].Label)
	}
}

func TestRowsDescribeAndNoToolsFilter(t *testing.T) {
	s := Create(t.TempDir(), "/p")
	for _, m := range convo() {
		_ = s.AppendMessage(m, "", "")
	}
	_ = s.Compact(2, "sum", 10)
	_ = s.SetName("n")
	_ = s.RecordModel("p", "m")
	var all, notools []string
	for _, r := range s.Rows(FilterAll) {
		all = append(all, r.Text)
	}
	for _, r := range s.Rows(FilterNoTools) {
		notools = append(notools, r.Text)
	}
	joined := strings.Join(all, "|")
	for _, want := range []string{"user: list files", "assistant: ok [tool: ls] [tool: read]", "tool result: ls", "compaction: sum", "name: n", "model: m"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(strings.Join(notools, "|"), "tool result") {
		t.Errorf("no-tools still shows tool results: %v", notools)
	}
}

func TestForkAtKeepsRawLinesAndRelinksSkippedNames(t *testing.T) {
	p := writePi(t, true)
	s, _ := Open(p)
	_ = s.SetName("named") // a session_info entry in the middle of the chain
	_ = s.AppendMessage(text("user", "after name"), "", "")
	dir := t.TempDir()
	f, err := s.Fork(dir, "/q")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name() != "" || f.Parent != p || len(f.Messages()) != 5 {
		t.Fatalf("fork: name=%q parent=%q msgs=%d", f.Name(), f.Parent, len(f.Messages()))
	}
	raw, _ := os.ReadFile(f.Path)
	if !strings.Contains(string(raw), `{ "id":"c1", "type":"custom",  "parentId":"a1"`) || !strings.Contains(string(raw), `"futureField":true`) {
		t.Errorf("custom entry not copied byte for byte:\n%s", raw)
	}
	last, _ := f.Entry(f.Leaf())
	if last.ParentID == nil || *last.ParentID != "a2" {
		t.Errorf("entry after the skipped name should hang off a2, got %v", last.ParentID)
	}
	// fork at an earlier entry
	c, err := s.ForkAt(dir, "/q", "a1")
	if err != nil || len(c.Messages()) != 2 || c.Leaf() != "a1" {
		t.Fatalf("ForkAt: %v msgs=%d leaf=%q", err, len(c.Messages()), c.Leaf())
	}
	empty, err := s.ForkAt(dir, "/q", "")
	if err != nil || len(empty.Messages()) != 0 {
		t.Fatalf("empty ForkAt: %v", err)
	}
	if _, err := os.Stat(empty.Path); err == nil {
		t.Error("an empty fork must not create a file")
	}
}

func TestExportJSONLAndHTML(t *testing.T) {
	s, _ := Open(writePi(t, false))
	_ = s.SetLabel("a1", "ckpt")
	_ = s.AppendMessage(text("user", "<script>alert(1)</script> & more"), "", "")
	var j bytes.Buffer
	if err := s.ExportJSONL(&j); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(j.String(), treePiFile+"\n") || !strings.HasSuffix(j.String(), "\n") {
		t.Errorf("jsonl export differs from the file:\n%s", j.String())
	}
	var h bytes.Buffer
	if err := s.ExportHTML(&h); err != nil {
		t.Fatal(err)
	}
	out := h.String()
	for _, want := range []string{"<!doctype html>", "&lt;script&gt;alert(1)&lt;/script&gt; &amp; more", "answer two", "<summary>thinking</summary>",
		`src="data:image/png;base64,QUJD"`, "ckpt", "Entry tree", "<style>"} {
		if !strings.Contains(out, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "http://") || strings.Contains(out, "https://") {
		t.Error("export must be self-contained and script-free")
	}
}

func TestExportHTMLOtherEntriesAndHostileImages(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"i","timestamp":"t","cwd":"/"}`,
		`{"type":"message","id":"1","parentId":null,"timestamp":"t","message":{"role":"user","content":"hi"}}`,
		`{"type":"message","id":"2","parentId":"1","timestamp":"t","message":{"role":"assistant","content":[` +
			`{"type":"image","data":"\"><script>","mimeType":"image/png"},{"type":"image","data":"QUJD","mimeType":"text/html"},` +
			`{"type":"toolCall","name":"ls","arguments":{"a":"<b>"}}]}}`,
		`{"type":"message","id":"3","parentId":"2","timestamp":"t","message":{"role":"toolResult","toolName":"ls",` +
			`"content":[{"type":"text","text":"out"}],"isError":true}}`,
		`{"type":"message","id":"4","parentId":"3","timestamp":"t","message":{"role":"system","content":"sys"}}`,
		`{"type":"compaction","id":"5","parentId":"4","timestamp":"t","summary":"S","firstKeptEntryId":"1"}`,
		`{"type":"branch_summary","id":"6","parentId":"5","timestamp":"t","fromId":"3","summary":"B"}`,
		`{"type":"custom_message","id":"7","parentId":"6","timestamp":"t","customType":"ext","content":"CM","display":true}`,
	}
	_ = os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	var h bytes.Buffer
	_ = s.ExportHTML(&h)
	out := h.String()
	if strings.Contains(out, "<script") || strings.Contains(out, "&lt;b&gt;") == false || strings.Count(out, "[image]") != 2 {
		t.Errorf("hostile content not neutralised:\n%s", out)
	}
	for _, want := range []string{"compaction", "branch summary", ">ext<", "CM", "tool result ls", "tool err"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if m := s.Messages(); len(m) < 2 { // compaction summary + custom_message + branch summary reach the model
		t.Errorf("messages: %d", len(m))
	}
}
