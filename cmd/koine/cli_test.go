package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	good := []struct {
		name string
		in   []string
		want cliArgs
	}{
		{"message and files", []string{"-p", "fix", "@a.go", "the", "bug"},
			cliArgs{Print: true, Files: []string{"a.go"}, Message: []string{"fix", "the", "bug"}}},
		{"equals and short", []string{"--model=m1", "-C", "/x", "-a"}, cliArgs{Model: "m1", Dir: "/x", Trust: true}},
		{"legacy single dash", []string{"-trust", "-serve", ":1", "-version", "-compat"}, cliArgs{Trust: true, Serve: ":1", Version: true, Compat: true}},
		{"double dash ends flags", []string{"--", "-p", "@f"}, cliArgs{Message: []string{"-p"}, Files: []string{"f"}}},
		{"tools list", []string{"-t", "read, bash,,grep", "--system-prompt", "S", "--append-system-prompt", "A"},
			cliArgs{Tools: []string{"read", "bash", "grep"}, System: "S", AppendSystem: "A"}},
		{"no tools, mode, help", []string{"-nt", "--mode", "json", "-h"}, cliArgs{NoTools: true, Mode: "json", Help: true}},
		{"session flags", []string{"-c", "--session", "abc", "--fork=def", "-n", "nm", "--no-session", "--session-dir", "/d", "-r"},
			cliArgs{Continue: true, Session: "abc", Fork: "def", Name: "nm", NoSession: true, SessionDir: "/d", Resume: true}},
		{"lone dash is a message", []string{"-"}, cliArgs{Message: []string{"-"}}},
	}
	for _, c := range good {
		got, err := parseArgs(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
	for name, in := range map[string][]string{
		"unknown":       {"-nope"},
		"missing value": {"--model"},
		"bad mode":      {"--mode", "xml"},
		"bool value":    {"--print=yes"},
	} {
		if _, err := parseArgs(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// firstUserText is the text of the first user message the fake API saw.
func firstUserText(seen []map[string]any) string {
	blocks := seen[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)
	return blocks[0].(map[string]any)["text"].(string)
}

func TestPipedStdinFilesAndMessageOrder(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "notes.md"), []byte("NOTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := cli("PIPED DIFF\n", "-C", proj, "@notes.md", "summarise", "this")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	want := "PIPED DIFF\n\n<file name=\"notes.md\">\nNOTES\n</file>\n\nsummarise this"
	if got := firstUserText(seen); got != want {
		t.Errorf("prompt:\n%q\nwant:\n%q", got, want)
	}
}

func TestMessageAloneRunsPrintModeEvenOnTerminal(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	var out, errb strings.Builder
	code := run([]string{"-C", t.TempDir(), "hello"}, streams{Out: &out, Err: &errb, InTTY: true, OutTTY: true})
	if code != 0 || len(seen) == 0 || firstUserText(seen) != "hello" {
		t.Fatalf("%d %s seen=%d", code, errb.String(), len(seen))
	}
}

func TestModelAndSystemPromptFlags(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	dir := t.TempDir()
	sysFile := filepath.Join(dir, "sys.txt")
	if err := os.WriteFile(sysFile, []byte("FROM FILE"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := cli("", "-C", dir, "--model", "claude-haiku-5-5", "--system-prompt", sysFile,
		"--append-system-prompt", "EXTRA RULE", "hi")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	sys := systemText(seen[0]["system"])
	replaced := strings.HasPrefix(sys, "FROM FILE") && !strings.Contains(sys, "You are koine")
	if seen[0]["model"] != "claude-haiku-5-5" || !replaced || !strings.Contains(sys, "EXTRA RULE") {
		t.Errorf("model=%v system=%q", seen[0]["model"], sys)
	}
	code, _, errs = cli("", "-C", dir, "--model", "no-such-vendor/zzz", "hi")
	if code != 1 || errs == "" {
		t.Errorf("unknown model should fail with exit 1, got %d %q", code, errs)
	}
}

func TestToolSelectionFlags(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	if code, _, errs := cli("", "-C", t.TempDir(), "-t", "read,grep", "hi"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	var names []string
	for _, td := range seen[0]["tools"].([]any) {
		names = append(names, td.(map[string]any)["name"].(string))
	}
	if len(names) != 2 {
		t.Errorf("-t read,grep advertised %v", names)
	}
	seen = nil
	if code, _, errs := cli("", "-C", t.TempDir(), "-nt", "hi"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if tl, _ := seen[0]["tools"].([]any); len(tl) != 0 {
		t.Errorf("-nt advertised %d tools", len(tl))
	}
}

func TestNothingToDoAndHelpAndMissingFile(t *testing.T) {
	isolate(t, "http://127.0.0.1:1")
	if code, _, errs := cli("  \n", "-C", t.TempDir(), "-p"); code != 2 || !strings.Contains(errs, "nothing to do") {
		t.Errorf("empty -p: %d %q", code, errs)
	}
	if code, out, _ := cli("", "-h"); code != 0 || !strings.Contains(out, "usage: koine") {
		t.Errorf("help: %d %q", code, out)
	}
	if code, _, errs := cli("", "-C", t.TempDir(), "@nope.txt", "x"); code != 2 || !strings.Contains(errs, "@nope.txt") {
		t.Errorf("missing file: %d %q", code, errs)
	}
}

func TestSessionsPersistContinueAndList(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	dir := t.TempDir()
	if code, _, errs := cli("", "-C", dir, "-n", "alpha", "remember the number 7"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	seen = nil
	if code, _, errs := cli("", "-C", dir, "-c", "what number?"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if first := firstUserText(seen); first != "remember the number 7" {
		t.Errorf("-c should replay the earlier conversation first, got %q", first)
	}
	code, out, _ := cli("", "-C", dir, "-r")
	if code != 0 || !strings.Contains(out, "alpha") || !strings.Contains(out, "msgs") {
		t.Errorf("-r listing: %d %q", code, out)
	}
	seen = nil
	cli("", "-C", dir, "--no-session", "fresh start")
	if firstUserText(seen) != "fresh start" {
		t.Errorf("--no-session run must not see history")
	}
	if code, _, _ := cli("", "-C", dir, "--session", "no-such"); code != 1 {
		t.Errorf("unknown --session: %d", code)
	}
}

func TestResumePickerOnATerminal(t *testing.T) {
	var seen []map[string]any
	api := fakeAnthropic(t, &seen)
	defer api.Close()
	isolate(t, api.URL)
	dir := t.TempDir()
	if code, _, errs := cli("", "-C", dir, "-n", "alpha", "remember the number 7"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	var o, e strings.Builder
	tty := func(in string) streams { return streams{In: strings.NewReader(in), Out: &o, Err: &e, InTTY: true} }
	if code := run([]string{"-C", dir, "-r"}, tty("q\n")); code != 0 || !strings.Contains(o.String(), "alpha") {
		t.Errorf("cancelled picker: %d %q %q", code, o.String(), e.String())
	}
	seen = nil
	o.Reset()
	if code := run([]string{"-C", dir, "-r", "-p", "what number?"}, tty("1\n")); code != 0 {
		t.Fatalf("picked: %d %q", code, e.String())
	}
	if first := firstUserText(seen); first != "remember the number 7" {
		t.Errorf("the chosen session should be replayed first, got %q", first)
	}
}
