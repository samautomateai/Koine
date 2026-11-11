package tui

import (
	"strings"
	"time"
)

// Key handles one keypress.
func (ui *UI) Key(k Key) {
	switch {
	case ui.sel != nil:
		ui.selectorKey(k)
	case ui.label != nil:
		ui.labelKey(k)
	case ui.tree != nil:
		ui.treeKey(k)
	default:
		ui.editorKey(k)
	}
}

func (ui *UI) afterEdit() {
	if t := ui.ed.Text(); t != ui.lastText {
		ui.lastText, ui.compOff = t, false
	}
	if ui.compOff {
		ui.comp = nil
		return
	}
	old := ui.comp
	ui.comp = Complete(ui.ed.lines[ui.ed.Row], ui.ed.Row, ui.ed.Col, ui.src, false)
	if ui.comp != nil && old != nil && old.Kind == ui.comp.Kind && old.Sel < len(ui.comp.Items) {
		ui.comp.Sel = 0
	}
}

func (ui *UI) editorKey(k Key) {
	b, ed := ui.keys, ui.ed
	if k.Paste {
		ed.Paste(k.Text)
		ui.afterEdit()
		return
	}
	if ui.comp != nil && ui.completionKey(k) {
		return
	}
	switch {
	case b.Matches("app.interrupt", k):
		ui.escape()
		return
	case b.Matches("app.clear", k):
		ui.ctrlC()
		return
	case b.Matches("app.exit", k) && ed.IsEmpty():
		ui.quit = true
		ui.actions = append(ui.actions, Action{Kind: "quit"})
		return
	case b.Matches("app.suspend", k):
		ui.actions = append(ui.actions, Action{Kind: "suspend"})
		return
	case b.Matches("app.editor.external", k):
		ui.actions = append(ui.actions, Action{Kind: "editor", Text: ed.Submitted()})
		return
	case b.Matches("app.model.select", k):
		ui.openModelSelector()
		return
	case b.Matches("app.model.cycleForward", k):
		ui.cycleModel(1)
		return
	case b.Matches("app.model.cycleBackward", k):
		ui.cycleModel(-1)
		return
	case b.Matches("app.thinking.cycle", k):
		ui.cycleThinking()
		return
	case b.Matches("app.thinking.toggle", k):
		ui.hideThinking = !ui.hideThinking
		ui.say(map[bool]string{true: "thinking blocks hidden", false: "thinking blocks shown"}[ui.hideThinking])
		ui.full = true
		return
	case b.Matches("app.tools.expand", k):
		ui.expanded = !ui.expanded
		ui.say(map[bool]string{true: "tool output expanded", false: "tool output collapsed"}[ui.expanded])
		ui.full = true
		return
	case b.Matches("app.message.copy", k):
		ui.actions = append(ui.actions, Action{Kind: "copy", Text: ui.lastAnswer()})
		return
	case b.Matches("app.session.new", k):
		ui.submitLine("/new")
		return
	case b.Matches("app.session.tree", k):
		ui.openTree("tree", ui.set.TreeFilter)
		return
	case b.Matches("app.session.fork", k):
		ui.openTree("fork", "user-only")
		return
	case b.Matches("app.session.resume", k):
		ui.submitLine("/resume")
		return
	case b.Matches("tui.input.newLine", k):
		ed.Newline()
	case b.Matches("tui.input.submit", k):
		ui.submit()
		return
	case b.Matches("tui.input.tab", k):
		ui.tabComplete()
		return
	default:
		if !ui.editKey(k) {
			return
		}
	}
	ui.afterEdit()
}

// editKey applies the editing and movement bindings. It reports whether the key was an edit (so completion is refreshed).
func (ui *UI) editKey(k Key) bool {
	b, ed := ui.keys, ui.ed
	switch {
	case b.Matches("tui.editor.cursorUp", k) || b.Matches("tui.editor.historyPrevious", k):
		if !ed.MoveRows(-1) && (ed.IsEmpty() || ed.Browsing()) {
			ed.HistoryPrev()
		}
	case b.Matches("tui.editor.cursorDown", k) || b.Matches("tui.editor.historyNext", k):
		if !ed.MoveRows(1) && ed.Browsing() {
			ed.HistoryNext()
		}
	case b.Matches("tui.editor.pageUp", k):
		ed.MoveRows(-5)
	case b.Matches("tui.editor.pageDown", k):
		ed.MoveRows(5)
	case b.Matches("tui.editor.cursorLeft", k):
		ed.Left()
	case b.Matches("tui.editor.cursorRight", k):
		ed.Right()
	case b.Matches("tui.editor.cursorWordLeft", k):
		ed.WordLeft()
	case b.Matches("tui.editor.cursorWordRight", k):
		ed.WordRight()
	case b.Matches("tui.editor.cursorLineStart", k):
		ed.LineStart()
	case b.Matches("tui.editor.cursorLineEnd", k):
		ed.LineEnd()
	case b.Matches("tui.editor.deleteCharBackward", k):
		ed.Backspace()
	case b.Matches("tui.editor.deleteCharForward", k):
		ed.DeleteForward()
	case b.Matches("tui.editor.deleteWordBackward", k):
		ed.KillWordBack()
	case b.Matches("tui.editor.deleteWordForward", k):
		ed.KillWordForward()
	case b.Matches("tui.editor.deleteToLineStart", k):
		ed.KillToStart()
	case b.Matches("tui.editor.deleteToLineEnd", k):
		ed.KillToEnd()
	case b.Matches("tui.editor.yank", k):
		ed.Yank()
	case b.Matches("tui.editor.yankPop", k):
		ed.YankPop()
	case b.Matches("tui.editor.undo", k):
		ed.Undo()
	case k.Text != "" && !strings.ContainsAny(k.Text, "\x1b\x00"):
		ed.Insert(k.Text)
	default:
		return false
	}
	return true
}

// completionKey handles the keys of an open autocomplete popup. It reports whether it used the key.
func (ui *UI) completionKey(k Key) bool {
	b := ui.keys
	switch {
	case b.Matches("tui.select.up", k):
		ui.comp.Move(-1)
	case b.Matches("tui.select.down", k):
		ui.comp.Move(1)
	case b.Matches("tui.select.confirm", k) && ui.typedInFull():
		ui.comp = nil // the command is already typed out: Enter sends it
		return false
	case b.Matches("tui.input.tab", k) || b.Matches("tui.select.confirm", k):
		ui.comp.Apply(ui.ed)
		ui.comp = nil
		ui.lastText = ui.ed.Text()
		ui.afterEdit()
	case b.Matches("tui.select.cancel", k) && !b.Matches("app.clear", k):
		ui.comp, ui.compOff = nil, true
	default:
		return false
	}
	return true
}

// tabComplete is Tab with no popup: complete a path, or open the popup for what is under the cursor.
func (ui *UI) tabComplete() {
	ed := ui.ed
	c := Complete(ed.lines[ed.Row], ed.Row, ed.Col, ui.src, true)
	switch {
	case c == nil:
	case len(c.Items) == 1:
		c.Apply(ed)
		ui.afterEdit()
	default:
		ui.comp = c
	}
}

func (ui *UI) escape() {
	now := ui.now()
	switch {
	case ui.busy:
		ui.actions = append(ui.actions, Action{Kind: "interrupt"})
	case !ui.ed.IsEmpty():
	case now.Sub(ui.armedEsc) < 600*time.Millisecond:
		ui.armedEsc = time.Time{}
		switch ui.set.DoubleEscape {
		case "tree":
			ui.openTree("tree", ui.set.TreeFilter)
		case "fork":
			ui.openTree("fork", "user-only")
		}
	default:
		ui.armedEsc = now
	}
}

// ctrlC clears the editor; with nothing to clear it interrupts a running turn, and a second press within a second leaves.
func (ui *UI) ctrlC() {
	now := ui.now()
	switch {
	case !ui.ed.IsEmpty():
		ui.ed.Clear()
		ui.comp = nil
	case ui.busy:
		ui.actions = append(ui.actions, Action{Kind: "interrupt"})
	case now.Sub(ui.armedC) < time.Second:
		ui.quit = true
		ui.actions = append(ui.actions, Action{Kind: "quit"})
	default:
		ui.armedC = now
		ui.say("press ctrl+c again to exit")
	}
}

// submit sends the editor's text.
func (ui *UI) submit() {
	text := strings.TrimSpace(ui.ed.Submitted())
	if text == "" {
		return
	}
	if ui.busy {
		ui.say("a turn is running; esc interrupts it")
		return
	}
	ui.ed.AddHistory(text)
	ui.ed.Clear()
	ui.comp = nil
	ui.lastText = ""
	ui.submitLine(text)
}

// submitLine runs a line: commands the UI owns are handled here, everything else goes to the app.
func (ui *UI) submitLine(text string) {
	if ui.localCommand(text) {
		return
	}
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "/skill:") {
		ui.add(Block{Kind: "user", Text: text, Final: true})
	}
	ui.busy, ui.busyAt, ui.lastRun = true, ui.now(), text
	ui.actions = append(ui.actions, Action{Kind: "run", Text: text})
}

// selectorKey handles keys while a selector is open.
func (ui *UI) selectorKey(k Key) {
	b, s := ui.keys, ui.sel
	if ui.selKey != nil && ui.selKey(k) {
		return
	}
	switch {
	case b.Matches("tui.select.cancel", k):
		ui.closeSelector(-1)
	case b.Matches("tui.select.confirm", k):
		if i, ok := s.Current(); ok {
			ui.closeSelector(i)
		}
	case b.Matches("tui.select.up", k):
		s.Move(-1)
	case b.Matches("tui.select.down", k):
		s.Move(1)
	case b.Matches("tui.select.pageUp", k):
		s.Move(-s.Max)
	case b.Matches("tui.select.pageDown", k):
		s.Move(s.Max)
	case k.Name == "backspace":
		s.Backspace()
	case k.Text != "" && !k.Paste:
		s.Type(k.Text)
	}
}

// openSelector shows s in place of the editor; pick gets the chosen item's index in s.Items, or -1 when cancelled.
func (ui *UI) openSelector(s *Selector, pick func(idx int)) {
	ui.sel, ui.selPick, ui.selKey = s, pick, nil
}

func (ui *UI) closeSelector(idx int) {
	pick := ui.selPick
	ui.sel, ui.selPick, ui.selKey = nil, nil, nil
	if pick != nil {
		pick(idx)
	}
}

// Ask opens a question with numbered choices (App.Choose). done gets the index, or -1.
func (ui *UI) Ask(title string, options []string, done func(int)) {
	items := make([]SelItem, len(options))
	for i, o := range options {
		items[i] = SelItem{Label: o}
	}
	s := NewSelector(title, items)
	s.NoSearch = len(items) <= 4
	ui.openSelector(s, done)
}

// typedInFull reports whether the token under the popup already is the selected command, so Enter should send it rather than complete it.
func (ui *UI) typedInFull() bool {
	c := ui.comp
	if c.Kind != "command" || c.Sel >= len(c.Items) {
		return false
	}
	return string(ui.ed.lines[ui.ed.Row][c.Start:min(c.End, len(ui.ed.lines[ui.ed.Row]))]) == strings.TrimSpace(c.Items[c.Sel].Value)
}
