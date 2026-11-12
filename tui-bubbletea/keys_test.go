package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"koine/core/rpcclient"
)

func TestKeysViewAndDialogHelpers(t *testing.T) {
	f := &fakeBackend{}
	m := newModel(f, "")
	if m.Init() == nil {
		t.Error("Init starts the cursor blink")
	}
	m.in.SetValue("a")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if !strings.Contains(m.in.Value(), "\n") {
		t.Errorf("alt+enter inserts a newline: %q", m.in.Value())
	}
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Error("esc with nothing running does nothing")
	}
	m, _ = update(m, event(`{"type":"extension_ui_request","id":"u","method":"set_editor_text","text":"from an extension"}`))
	if m.in.Value() != "from an extension" {
		t.Errorf("editor text %q", m.in.Value())
	}
	m, _ = update(m, event(`{"type":"extension_ui_request","id":"u","method":"setStatus","statusKey":"k","statusText":"ready"}`))
	m, _ = update(m, event(`{"type":"agent_start"}`))
	if v := m.View(); !strings.Contains(v, "ready") || !strings.Contains(v, "working") {
		t.Errorf("view:\n%s", v)
	}

	sel := &rpcclient.Dialog{Method: "select", Title: "T", Message: "msg", Options: []string{"a"}}
	for _, d := range []*rpcclient.Dialog{sel, {Method: "confirm", Title: "C"}, {Method: "input", Title: "I"}} {
		if renderDialog(d) == "" {
			t.Errorf("empty render for %s", d.Method)
		}
	}
	if _, ok := dialogAnswer(sel, "x"); ok {
		t.Error("a select needs a number")
	}
	if got, ok := dialogAnswer(sel, "1"); !ok || got["value"] != "a" {
		t.Errorf("select: %v %v", got, ok)
	}
	conf := &rpcclient.Dialog{Method: "confirm"}
	if got, ok := dialogAnswer(conf, "No"); !ok || got["confirmed"] != false {
		t.Errorf("confirm no: %v %v", got, ok)
	}
	if got, ok := dialogAnswer(conf, "y"); !ok || got["confirmed"] != true {
		t.Errorf("confirm yes: %v %v", got, ok)
	}
	if _, ok := dialogAnswer(conf, "maybe"); ok {
		t.Error("a confirm needs y or n")
	}
	if got, ok := dialogAnswer(&rpcclient.Dialog{Method: "input"}, " text "); !ok || got["value"] != "text" {
		t.Errorf("input: %v %v", got, ok)
	}
	for _, k := range []rpcclient.Kind{rpcclient.KindUser, rpcclient.KindThinking, rpcclient.KindNotice, rpcclient.KindError} {
		if renderEntry(rpcclient.Entry{Kind: k, Text: "x"}) == "" {
			t.Errorf("empty render for %s", k)
		}
	}
	if got := renderEntry(rpcclient.Entry{Kind: rpcclient.KindTool, Tool: "read"}); !strings.Contains(got, "read") {
		t.Errorf("running tool: %q", got)
	}
	if got := renderEntry(rpcclient.Entry{Kind: rpcclient.KindTool, Tool: "read", Text: "ok", Done: true}); !strings.Contains(got, "ok") {
		t.Errorf("finished tool: %q", got)
	}
}
