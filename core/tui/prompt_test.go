package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUIPromptHidesSecretsAndCanBeCancelled(t *testing.T) {
	ui := newTestUI(t, nil)
	var got string
	var ok bool
	ui.Prompt("API key: ", true, func(s string, o bool) { got, ok = s, o })
	typeText(ui, "fake-keyX")
	press(ui, "backspace")
	f := frameText(ui)
	if !strings.Contains(f, "API key:") || strings.Contains(f, "fake-key") || !strings.Contains(f, "••••••••") {
		t.Errorf("a secret is drawn as bullets:\n%s", f)
	}
	press(ui, "enter")
	if got != "fake-key" || !ok || ui.prompt != nil {
		t.Errorf("answer %q %v", got, ok)
	}
	ui.Prompt("Name: ", false, func(s string, o bool) { got, ok = s, o })
	typeText(ui, "abc")
	if !strings.Contains(frameText(ui), "abc") {
		t.Error("plain answers are shown")
	}
	press(ui, "escape")
	if ok || got != "" || ui.prompt != nil {
		t.Errorf("cancel %q %v", got, ok)
	}
}

func TestLoopLoginAsksForTheKeyInTheTerminalUI(t *testing.T) {
	term := newFakeTerm()
	done := startLoop(t, term, nil, nil)
	term.waitFor(t, "koine")
	term.send("/login mistral\r")
	term.waitFor(t, "API key for mistral")
	term.send("fake-mistral-key\r")
	path := filepath.Join(os.Getenv("HOME"), ".koine", "auth.json")
	var data []byte
	for i := 0; i < 400 && !strings.Contains(string(data), "fake-mistral-key"); i++ {
		time.Sleep(10 * time.Millisecond)
		data, _ = os.ReadFile(path)
	}
	if !strings.Contains(string(data), "fake-mistral-key") {
		t.Fatalf("the key was not stored: %s", data)
	}
	term.send("/login mistral\r")
	term.waitFor(t, "API key for mistral")
	term.send("\x1b") // cancel
	term.waitFor(t, "cancelled")
	term.send("/exit\r")
	waitDone(t, done)
}
