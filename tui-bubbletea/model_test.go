package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"koine/core/rpcclient"
)

type fakeBackend struct {
	prompts   []string
	behaviors []string
	aborted   int
	replies   []map[string]any
	err       error
}

func (f *fakeBackend) Prompt(_ context.Context, msg, behavior string) (string, error) {
	f.prompts, f.behaviors = append(f.prompts, msg), append(f.behaviors, behavior)
	return "started", f.err
}
func (f *fakeBackend) Abort(context.Context) error { f.aborted++; return f.err }
func (f *fakeBackend) RespondUI(_ string, fields map[string]any) error {
	f.replies = append(f.replies, fields)
	return nil
}

func event(s string) evMsg {
	var h struct{ Type string }
	_ = json.Unmarshal([]byte(s), &h)
	return evMsg(rpcclient.Event{Type: h.Type, Raw: json.RawMessage(s)})
}

func update(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func typeLine(m model, s string) model {
	m.in.SetValue(s)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

func TestEnterSendsAPromptAndSteersWhileBusy(t *testing.T) {
	f := &fakeBackend{}
	m := newModel(f, "m · /p")
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.in.SetValue("hello")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.in.Value() != "" {
		t.Fatal("enter should clear the editor and return a command")
	}
	cmd()
	m, _ = update(m, event(`{"type":"agent_start"}`))
	m.in.SetValue("also this")
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	cmd()
	if strings.Join(f.prompts, "|") != "hello|also this" || f.behaviors[0] != "" || f.behaviors[1] != "steer" {
		t.Errorf("prompts %v behaviors %v", f.prompts, f.behaviors)
	}
	m.in.Reset()
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("an empty line sends nothing")
	}
}

func TestEventsRenderIntoTheViewAndEscAborts(t *testing.T) {
	f := &fakeBackend{}
	m := newModel(f, "m · /p")
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	for _, e := range []string{
		`{"type":"agent_start"}`,
		`{"type":"message_end","message":{"role":"user","content":"list"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hel"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"lo"}}`,
	} {
		m, _ = update(m, event(e))
	}
	view := m.View()
	if !strings.Contains(view, "Hello") || !strings.Contains(view, "› list") || !strings.Contains(view, "working") {
		t.Errorf("view:\n%s", view)
	}
	if strings.Count(strings.Join(m.log, "\n"), "Hello") != 1 {
		t.Errorf("streamed text must be rendered once: %q", m.log)
	}
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEsc})
	cmd()
	if f.aborted != 1 {
		t.Error("esc while busy aborts")
	}
}

func TestDialogsAreAnsweredFromTheEditor(t *testing.T) {
	f := &fakeBackend{}
	m := newModel(f, "")
	m, _ = update(m, event(`{"type":"extension_ui_request","id":"ui-1","method":"select","title":"Pick","options":["a","b"]}`))
	if !strings.Contains(m.View(), "1. a") {
		t.Errorf("view:\n%s", m.View())
	}
	m = typeLine(m, "9") // out of range: stays open
	if m.tr.Dialog == nil || len(f.replies) != 0 {
		t.Fatal("a bad answer keeps the dialog")
	}
	m = typeLine(m, "2")
	if m.tr.Dialog != nil || f.replies[0]["value"] != "b" {
		t.Errorf("replies %v", f.replies)
	}
	m, _ = update(m, event(`{"type":"extension_ui_request","id":"ui-2","method":"confirm","title":"Sure?"}`))
	m = typeLine(m, "y")
	if f.replies[1]["confirmed"] != true {
		t.Errorf("replies %v", f.replies)
	}
	m, _ = update(m, event(`{"type":"extension_ui_request","id":"ui-3","method":"input","title":"Name"}`))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.tr.Dialog != nil || f.replies[2]["cancelled"] != true {
		t.Errorf("replies %v", f.replies)
	}
}

func TestErrorsAndExit(t *testing.T) {
	m := newModel(&fakeBackend{}, "")
	m, _ = update(m, sentMsg{errors.New("busy")})
	if !strings.Contains(strings.Join(m.log, "\n"), "busy") {
		t.Errorf("log %q", m.log)
	}
	_, cmd := update(m, exitMsg{errors.New("exit status 1")})
	if cmd == nil {
		t.Error("exit quits")
	}
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("ctrl+c quits")
	}
}

func TestRenderEntry(t *testing.T) {
	got := renderEntry(rpcclient.Entry{Kind: rpcclient.KindTool, Tool: "bash", Args: `{"command":"ls"}`, Text: "a b", Done: true, IsError: true})
	if !strings.Contains(got, "bash") || !strings.Contains(got, "a b") {
		t.Errorf("%q", got)
	}
	if got := renderEntry(rpcclient.Entry{Kind: rpcclient.KindAssistant, Text: "hi"}); got != "hi" {
		t.Errorf("%q", got)
	}
}
