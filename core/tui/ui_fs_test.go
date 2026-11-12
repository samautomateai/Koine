package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/app"
	"koine/core/llm"
)

func fullscreenUI(t *testing.T, cfg string, env map[string]string) *UI {
	t.Helper()
	a := newTestApp(t, "")
	if cfg != "" {
		if err := os.MkdirAll(filepath.Join(a.Home, ".koine"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(a.Home, ".koine", "settings.json"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		a.Cfg, _ = app.LoadConfig(a.Cwd, a.Home)
	}
	set := ResolveSettings(a, func(k string) string { return env[k] })
	th, keys, _, _ := Looks(a, set)
	th.TrueColor = true
	return NewUI(a, set, th, keys, 60, 12, (&clock{}).now)
}

func fillTranscript(ui *UI, n int) {
	for i := 1; i <= n; i++ {
		ui.Event(agent.Event{Type: "text", Text: fmt.Sprintf("answer number %02d", i)})
		ui.blocks[len(ui.blocks)-1].Final = true
	}
}

func TestFullscreenIsOptIn(t *testing.T) {
	if ui := fullscreenUI(t, "", nil); ui.fs != nil || ui.ExitOutput() != "" {
		t.Error("regular mode is the default")
	}
	if ui := fullscreenUI(t, `{"tuiMode":"fullscreen"}`, map[string]string{"TERM": "dumb"}); ui.fs != nil {
		t.Error("a dumb terminal cannot show fullscreen mode")
	}
	if ui := fullscreenUI(t, `{"tuiMode":"fullscreen"}`, nil); ui.fs == nil {
		t.Error("tuiMode fullscreen turns it on")
	}
}

func TestFullscreenFrameScrollsWithKeysAndWheel(t *testing.T) {
	ui := fullscreenUI(t, `{"tuiMode":"fullscreen","fullscreenWheelScrollLines":2}`, nil)
	fillTranscript(ui, 30)
	f := ui.Frame()
	if !strings.HasPrefix(f, "\x1b[?25l") || !strings.Contains(f, "answer number 30") || strings.Contains(f, "answer number 01") {
		t.Fatalf("follows the tail:\n%s", StripANSI(f))
	}
	if strings.Contains(f, "\r\n") {
		t.Error("fullscreen frames address rows; nothing scrolls the screen")
	}
	press(ui, "pageUp")
	if f = ui.Frame(); strings.Contains(f, "answer number 30") && !strings.Contains(f, "answer number 29") {
		t.Errorf("page up moved the view:\n%s", StripANSI(f))
	}
	before := ui.fs.Off()
	ui.Key(Key{Name: "wheelUp"})
	ui.Key(Key{Name: "alt+wheelUp"}) // five times as far
	ui.Frame()
	if got := ui.fs.Off(); got != before+2+10 {
		t.Errorf("wheel: %d -> %d", before, got)
	}
	ui.Key(Key{Name: "wheelDown"})
	press(ui, "ctrl+end")
	ui.Frame()
	if !ui.fs.Following() {
		t.Error("ctrl+end follows the output again")
	}
	press(ui, "ctrl+home")
	if ui.Frame(); ui.fs.Following() || ui.fs.Off() == 0 {
		t.Error("ctrl+home goes to the top")
	}
	for _, k := range []string{"pageDown"} {
		press(ui, k)
	}
	typeText(ui, "hello")
	press(ui, "enter")
	if ui.Frame(); !ui.fs.Following() {
		t.Error("sending a message goes back to the tail")
	}
}

func TestFullscreenCustomKeysAndOverlayPrecedence(t *testing.T) {
	ui := fullscreenUI(t, `{"tuiMode":"fullscreen"}`, nil)
	fillTranscript(ui, 40)
	ui.Frame()
	ui.keys.keys["tui.altScreen.lineUp"] = []string{"ctrl+k"}
	ui.keys.keys["tui.altScreen.halfPageUp"] = []string{"ctrl+u"}
	ui.keys.keys["tui.altScreen.lineDown"] = []string{"ctrl+j"}
	ui.keys.keys["tui.altScreen.halfPageDown"] = []string{"ctrl+d"}
	press(ui, "ctrl+k")
	if ui.Frame(); ui.fs.Off() != 1 {
		t.Errorf("line up: %d", ui.fs.Off())
	}
	press(ui, "ctrl+u")
	if ui.Frame(); ui.fs.Off() != 1+ui.fs.PageSize()/2 {
		t.Errorf("half page up: %d", ui.fs.Off())
	}
	press(ui, "ctrl+d", "ctrl+j")
	if ui.Frame(); ui.fs.Off() != 0 {
		t.Errorf("down: %d", ui.fs.Off())
	}
	typeText(ui, "/mo") // the completion popup has the page keys while it is open
	before := ui.fs.Off()
	press(ui, "pageDown")
	if ui.Frame(); ui.fs.Off() != before {
		t.Error("an open popup keeps the page keys")
	}
}

func TestFullscreenExitOutput(t *testing.T) {
	ui := fullscreenUI(t, `{"tuiMode":"fullscreen"}`, nil)
	fillTranscript(ui, 3)
	if out := ui.ExitOutput(); !strings.Contains(StripANSI(out), "answer number 03") || !strings.HasSuffix(out, "\r\n") {
		t.Errorf("transcript: %q", out)
	}
	hint := fullscreenUI(t, `{"tuiMode":"fullscreen","fullscreenExitOutput":"resume-hint"}`, nil)
	if sess := hint.session(); sess == nil || sess.ID == "" {
		t.Fatal("the app opens a session")
	} else if out := hint.ExitOutput(); out != "Resume this session with: koine --session "+sess.ID+"\r\n" {
		t.Errorf("hint: %q", out)
	}
	hint.a.Session = nil
	if hint.ExitOutput() != "" {
		t.Error("no session, no hint")
	}
	empty := fullscreenUI(t, `{"tuiMode":"fullscreen","quietStartup":true}`, nil)
	empty.blocks = nil
	if empty.ExitOutput() != "" {
		t.Error("nothing to print")
	}
}

func TestFullscreenParkAndUnpark(t *testing.T) {
	ui := fullscreenUI(t, `{"tuiMode":"fullscreen"}`, nil)
	ui.Frame()
	if p := ui.park(); !strings.Contains(p, FullscreenLeave) || !strings.HasPrefix(p, RestoreKeys) {
		t.Errorf("park: %q", p)
	}
	if u := ui.unpark(); !strings.Contains(u, FullscreenEnter) || !strings.HasPrefix(u, EnableKeys) {
		t.Errorf("unpark: %q", u)
	}
	if f := ui.Frame(); !strings.Contains(f, "\x1b[2J") {
		t.Error("coming back repaints the whole screen")
	}
	reg := fullscreenUI(t, "", nil)
	if strings.Contains(reg.park(), FullscreenLeave) || reg.unpark() != EnableKeys {
		t.Error("regular mode never touches the alternate screen")
	}
}

func TestFullscreenLiveToolAndImages(t *testing.T) {
	ui := fullscreenUI(t, `{"tuiMode":"fullscreen","terminal":{"images":"kitty","imageWidthCells":10}}`, nil)
	ui.Event(agent.Event{Type: "tool_start", Tool: "read", CallID: "1", Args: []byte(`{"path":"a.png"}`)})
	d := pngData(t, 80, 32)
	ui.Event(agent.Event{Type: "tool_end", Tool: "read", CallID: "1", Text: "Read image file [image/png]",
		Images: []llm.Block{llm.ImageBlock(d.MIME, d.Data)}})
	f := ui.Frame()
	if !strings.Contains(f, "a=T") || !strings.Contains(f, kittyDeleteAll) {
		t.Errorf("the image is drawn once on the first frame: %q", f[:min(len(f), 100)])
	}
	if again := ui.Frame(); strings.Contains(again, "a=T") {
		t.Error("and not again while nothing moves")
	}
}

func TestHistoryImagesBecomeBlocks(t *testing.T) {
	d := pngData(t, 80, 32)
	tool := llm.Block{Type: "tool_result", ToolUseID: "t1", Content: "ok", Images: []llm.Block{llm.ImageBlock(d.MIME, d.Data)}}
	h := []llm.Message{
		{Role: "user", Content: []llm.Block{{Type: "text", Text: "see"}, llm.ImageBlock(d.MIME, d.Data)}},
		{Role: "user", Content: []llm.Block{llm.ImageBlock("image/gif", "R0lGODlhAQABAAAAACw=")}},
		{Role: "assistant", Content: []llm.Block{{Type: "tool_use", ID: "t1", Name: "read", Input: []byte(`{"path":"x"}`)}}},
		{Role: "user", Content: []llm.Block{tool}},
	}
	bs := BlocksFromHistory(h)
	if len(bs) != 3 || len(bs[0].Images) != 1 || bs[0].Text != "see" || len(bs[1].Images) != 1 || bs[1].Text != "" ||
		bs[2].Tool == nil || len(bs[2].Tool.Images) != 1 {
		t.Fatalf("blocks: %+v", bs)
	}
	ctx := RenderCtx{Width: 60, Theme: Builtin("dark")}
	if out := RenderBlock(bs[1], ctx); len(out) != 1 || !strings.Contains(out[0], "[image: image/gif") {
		t.Errorf("an image-only user message has no empty text line: %q", out)
	}
}

func TestLoopFullscreenEntersAndLeavesTheAlternateScreen(t *testing.T) {
	term := newFakeTerm()
	a := newTestApp(t, "")
	dir := filepath.Join(a.Home, ".koine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"tuiMode":"fullscreen"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Cfg, _ = app.LoadConfig(a.Cwd, a.Home)
	done := make(chan error, 1)
	go func() { done <- Loop(t.Context(), a, term, Options{Env: func(string) string { return "" }}) }()
	term.waitFor(t, "koine")
	term.send("hello there\r")
	term.waitFor(t, "model says hi")
	term.send("\x1b[<64;5;5M") // wheel up
	term.send("\x1b[1;5H")     // an unknown sequence is dropped
	term.send("\x04")
	waitDone(t, done)
	out := term.out.String()
	i, j := strings.Index(out, FullscreenEnter), strings.LastIndex(out, FullscreenLeave)
	if i < 0 || j < i || !strings.Contains(out[j:], "model says hi") || !strings.HasSuffix(out, "\r\n") {
		t.Errorf("enter at %d, leave at %d; the transcript is printed after leaving", i, j)
	}
}

func TestResolveSettingsDetectsImagesAndWheel(t *testing.T) {
	a := newTestApp(t, "")
	s := ResolveSettings(a, func(k string) string { return map[string]string{"KITTY_WINDOW_ID": "1"}[k] })
	if s.ImageProto != "kitty" || !s.ShowImages || s.ImageWidth != 60 || s.FullscreenOn {
		t.Errorf("settings: %+v", s)
	}
	if !wheelIsLocalMac("darwin", func(string) string { return "" }) || wheelIsLocalMac("linux", func(string) string { return "" }) ||
		wheelIsLocalMac("darwin", func(k string) string { return map[string]string{"SSH_TTY": "/dev/pts/1"}[k] }) {
		t.Error("only a local Mac terminal is flat")
	}
}
