package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"koine/core/agentdir"
	"koine/core/llm"
	"koine/core/session"
)

// localCommand handles the slash commands the UI owns. It reports whether it did.
func (ui *UI) localCommand(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	name, args, _ := strings.Cut(text[1:], " ")
	args = strings.TrimSpace(args)
	switch {
	case name == "hotkeys":
		ui.notice(ui.keys.Hotkeys())
	case name == "settings":
		ui.openSettings(0)
	case name == "model" && args == "":
		ui.openModelSelector()
	case name == "thinking" && args == "":
		ui.openThinkingSelector()
	case name == "scoped-models":
		ui.openScoped()
	case name == "copy":
		ui.actions = append(ui.actions, Action{Kind: "copy", Text: ui.lastAnswer()})
	case name == "tree" && args == "":
		ui.openTree("tree", ui.set.TreeFilter)
	default:
		return false
	}
	return true
}

func (ui *UI) lastAnswer() string {
	for i := len(ui.blocks) - 1; i >= 0; i-- {
		if ui.blocks[i].Kind == "assistant" {
			return ui.blocks[i].Text
		}
	}
	return ""
}

// ---- model and thinking ----

func (ui *UI) openModelSelector() {
	ui.modelSelector(len(ui.scoped) > 0)
}

func (ui *UI) modelSelector(scopedOnly bool) {
	cur := ui.a.Agent.Model
	var items []SelItem
	add := func(id, desc string) {
		it := SelItem{Label: id, Desc: desc, Value: id}
		if id == cur || strings.HasSuffix(id, "/"+cur) {
			it.Mark = "✓"
		}
		items = append(items, it)
	}
	if scopedOnly {
		for _, id := range ui.scoped {
			add(id, "")
		}
	} else if cat := ui.a.Agent.Catalog; cat != nil {
		for _, m := range cat.Models {
			desc := m.Provider
			if m.ContextWindow > 0 {
				desc += " · " + FormatTokens(m.ContextWindow) + " context"
			}
			add(m.ID, strings.TrimPrefix(desc, " · "))
		}
	}
	if len(items) == 0 {
		ui.notice("no models to choose from; /model <id> sets one")
		return
	}
	title := "Select model"
	if scopedOnly {
		title += " (scoped; tab for all)"
	} else if len(ui.scoped) > 0 {
		title += " (all; tab for scoped)"
	}
	s := NewSelector(title, items)
	for i, it := range items {
		if it.Mark != "" {
			s.Sel = i
		}
	}
	ui.openSelector(s, func(i int) {
		if i >= 0 {
			ui.submitLine("/model " + items[i].Value)
		}
	})
	ui.selKey = func(k Key) bool {
		if k.Name == "tab" && len(ui.scoped) > 0 {
			ui.sel = nil
			ui.modelSelector(!scopedOnly)
			return true
		}
		return false
	}
}

func (ui *UI) openThinkingSelector() {
	cur := ui.a.Agent.ThinkingLevel()
	if cur == "" {
		ui.notice("thinking: not used with " + ui.a.Agent.Model)
		return
	}
	var items []SelItem
	for _, l := range llm.ThinkingLevels {
		it := SelItem{Label: l, Value: l}
		if l == cur {
			it.Mark = "✓"
		}
		items = append(items, it)
	}
	s := NewSelector("Thinking level", items)
	s.NoSearch = true
	for i, it := range items {
		if it.Mark != "" {
			s.Sel = i
		}
	}
	ui.openSelector(s, func(i int) {
		if i >= 0 {
			ui.submitLine("/thinking " + items[i].Value)
		}
	})
}

func (ui *UI) cycleThinking() {
	cur := ui.a.Agent.ThinkingLevel()
	if cur == "" {
		ui.say("this model has no thinking levels")
		return
	}
	for i, l := range llm.ThinkingLevels {
		if l == cur {
			ui.submitLine("/thinking " + llm.ThinkingLevels[(i+1)%len(llm.ThinkingLevels)])
			return
		}
	}
}

func (ui *UI) cycleModel(d int) {
	if len(ui.scoped) == 0 {
		ui.say("no scoped models: set enabledModels or start with --models")
		return
	}
	cur := ui.a.Agent.Model
	idx := -1
	for i, m := range ui.scoped {
		if base, _ := llm.SplitThinkingSuffix(m); base == cur || strings.HasSuffix(base, "/"+cur) {
			idx = i
		}
	}
	n := len(ui.scoped)
	next := ui.scoped[((idx+d)%n+n)%n]
	if idx < 0 && d < 0 {
		next = ui.scoped[n-1]
	}
	ui.submitLine("/model " + next)
}

// openScoped lets the user pick the models Ctrl+P cycles through and saves them as the enabledModels setting.
func (ui *UI) openScoped() {
	cat := ui.a.Agent.Catalog
	if cat == nil || len(cat.Models) == 0 {
		ui.notice("no model catalog to choose from")
		return
	}
	on := map[string]bool{}
	for _, m := range ui.scoped {
		on[m] = true
	}
	var items []SelItem
	for _, m := range cat.Models {
		items = append(items, SelItem{Label: m.ID, Desc: m.Provider, Value: m.ID})
	}
	s := NewSelector("Scoped models", items)
	s.Multi = true
	s.Hint = fmt.Sprintf("enter toggles · %s all · %s none · %s saves · esc closes", firstKey(ui.keys, "app.models.enableAll"),
		firstKey(ui.keys, "app.models.clearAll"), firstKey(ui.keys, "app.models.save"))
	mark := func() {
		for i := range items {
			items[i].Mark = "[ ]"
			if on[items[i].Value] {
				items[i].Mark = "[x]"
			}
		}
		s.Items = items
	}
	mark()
	ui.openSelector(s, func(int) {})
	ui.selKey = func(k Key) bool {
		switch {
		case ui.keys.Matches("tui.select.confirm", k):
			if i, ok := s.Current(); ok {
				on[items[i].Value] = !on[items[i].Value]
				mark()
			}
		case ui.keys.Matches("app.models.enableAll", k):
			for _, it := range items {
				on[it.Value] = true
			}
			mark()
		case ui.keys.Matches("app.models.clearAll", k):
			on = map[string]bool{}
			mark()
		case ui.keys.Matches("app.models.save", k):
			var list []string
			for _, it := range items {
				if on[it.Value] {
					list = append(list, it.Value)
				}
			}
			ui.scoped = list
			var v any = list
			if len(list) == 0 {
				v = nil
			}
			ui.save("enabledModels", v)
			ui.sel, ui.selPick, ui.selKey = nil, nil, nil
			ui.notice(fmt.Sprintf("scoped models saved (%d)", len(list)))
		default:
			return false
		}
		return true
	}
}

// ---- settings ----

func (ui *UI) save(key string, v any) {
	if err := SaveSetting(ui.a.Home, key, v); err != nil {
		ui.add(Block{Kind: "error", Text: err.Error(), Final: true})
	}
}

type settingDef struct {
	key, label string
	values     []any
	cur        func(ui *UI) any
	apply      func(ui *UI, v any)
}

func onOff(v any) string {
	if b, ok := v.(bool); ok && b {
		return "on"
	}
	return "off"
}

var settingDefs = []settingDef{
	{"theme", "Theme", nil, func(ui *UI) any { return ui.set.Theme }, nil},
	{"hideThinkingBlock", "Hide thinking blocks", []any{false, true}, func(ui *UI) any { return ui.hideThinking },
		func(ui *UI, v any) { ui.hideThinking = v.(bool); ui.full = true }},
	{"quietStartup", "Quiet startup", []any{false, "header", true}, func(ui *UI) any {
		switch ui.set.Quiet {
		case "true":
			return true
		case "header":
			return "header"
		}
		return false
	}, func(ui *UI, v any) { ui.set.Quiet = map[any]string{false: "", "header": "header", true: "true"}[v] }},
	{"editorPaddingX", "Editor padding", []any{0.0, 1.0, 2.0, 3.0}, func(ui *UI) any { return float64(ui.set.EditorPaddingX) },
		func(ui *UI, v any) { ui.set.EditorPaddingX = int(v.(float64)) }},
	{"outputPad", "Output padding", []any{0.0, 1.0}, func(ui *UI) any { return float64(ui.set.OutputPad) },
		func(ui *UI, v any) { ui.set.OutputPad = int(v.(float64)); ui.full = true }},
	{"autocompleteMaxVisible", "Autocomplete rows", []any{3.0, 5.0, 8.0, 10.0, 15.0, 20.0}, func(ui *UI) any { return float64(ui.set.AutocompleteMax) },
		func(ui *UI, v any) { ui.set.AutocompleteMax = int(v.(float64)) }},
	{"showHardwareCursor", "Hardware cursor", []any{false, true}, func(ui *UI) any { return ui.set.HardwareCursor },
		func(ui *UI, v any) { ui.set.HardwareCursor = v.(bool) }},
	{"terminal.showTerminalProgress", "Terminal progress indicator", []any{false, true}, func(ui *UI) any { return ui.set.TerminalProgress },
		func(ui *UI, v any) { ui.set.TerminalProgress = v.(bool) }},
}

func (def settingDef) show(v any) string {
	switch x := v.(type) {
	case bool:
		return onOff(x)
	case float64:
		return fmt.Sprint(int(x))
	}
	return fmt.Sprint(v)
}

func (ui *UI) openSettings(at int) {
	items := make([]SelItem, len(settingDefs))
	for i, d := range settingDefs {
		items[i] = SelItem{Label: d.label, Desc: d.show(d.cur(ui)), Value: d.key}
	}
	s := NewSelector("Settings", items)
	s.NoSearch = true
	s.Sel = at
	s.Hint = "enter changes the value and saves it to settings.json · esc closes"
	ui.openSelector(s, func(i int) {
		if i < 0 {
			return
		}
		d := settingDefs[i]
		if d.key == "theme" {
			ui.openThemeSelector()
			return
		}
		cur := d.show(d.cur(ui))
		next := d.values[0]
		for k, v := range d.values {
			if d.show(v) == cur {
				next = d.values[(k+1)%len(d.values)]
			}
		}
		d.apply(ui, next)
		ui.save(d.key, next)
		ui.openSettings(i)
	})
}

func (ui *UI) openThemeSelector() {
	names, warns := ThemeNames(ui.themeDirs)
	ui.themeWarns = warns
	items := make([]SelItem, len(names))
	for i, n := range names {
		items[i] = SelItem{Label: n, Value: n}
		if n == ui.set.Theme {
			items[i].Mark = "✓"
		}
	}
	s := NewSelector("Theme", items)
	ui.openSelector(s, func(i int) {
		if i >= 0 {
			ui.SetTheme(items[i].Value)
			ui.save("theme", items[i].Value)
		}
		ui.openSettings(0)
	})
}

// SetTheme switches the colour theme and redraws.
func (ui *UI) SetTheme(name string) {
	th, warns := LoadTheme(name, ui.themeDirs, ui.set.TrueColor)
	ui.th, ui.set.Theme = th, th.Name
	for _, w := range warns {
		ui.notice("theme: " + w)
	}
	ui.full = true
}

// ThemeDirsFor lists where themes are looked for: the agent directory and (when trusted) the project, unless --no-themes, plus --theme paths.
func ThemeDirsFor(home, cwd string, trusted, noThemes bool, extra []string) []string {
	var d []string
	if !noThemes {
		d = ThemeDirs(agentdir.Of(home), cwd, trusted)
	}
	return append(d, extra...)
}

// ReloadLooks re-reads keybindings.json and the theme after /reload.
func (ui *UI) ReloadLooks() {
	keys, warns := LoadBindings(agentdir.Of(ui.a.Home))
	ui.keys = keys
	for _, w := range warns {
		ui.notice("keybindings: " + w)
	}
	ui.SetTheme(ui.a.Cfg.TUI().Theme)
	ui.src.Commands = ui.commandList()
}

// ---- session tree ----

var treeModes = []string{session.FilterDefault, session.FilterNoTools, session.FilterUserOnly, session.FilterLabeledOnly, session.FilterAll}

func (ui *UI) openTree(kind, mode string) {
	if ui.session() == nil {
		ui.notice("sessions are off (--no-session)")
		return
	}
	if !session.ValidFilter(mode) {
		mode = session.FilterDefault
	}
	ui.tree, ui.treeFor = NewTreeNav(ui.session(), mode), kind
}

func (ui *UI) treeKey(k Key) {
	b, t := ui.keys, ui.tree
	filters := map[string]string{"app.tree.filter.default": session.FilterDefault, "app.tree.filter.noTools": session.FilterNoTools,
		"app.tree.filter.userOnly": session.FilterUserOnly, "app.tree.filter.labeledOnly": session.FilterLabeledOnly,
		"app.tree.filter.all": session.FilterAll}
	ids := make([]string, 0, len(filters))
	for id := range filters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if b.Matches(id, k) {
			t.SetMode(filters[id], "")
			return
		}
	}
	cycle := func(d int) {
		for i, m := range treeModes {
			if m == t.Mode {
				t.SetMode(treeModes[((i+d)%len(treeModes)+len(treeModes))%len(treeModes)], "")
				return
			}
		}
	}
	switch {
	case b.Matches("app.tree.filter.cycleForward", k):
		cycle(1)
	case b.Matches("app.tree.filter.cycleBackward", k):
		cycle(-1)
	case b.Matches("tui.select.cancel", k):
		ui.tree = nil
	case b.Matches("tui.select.confirm", k):
		id := t.CurrentID()
		ui.tree = nil
		if id != "" && ui.treeFor == "fork" {
			ui.submitLine("/fork " + id)
		} else if id != "" {
			ui.submitLine("/tree " + id)
		}
	case b.Matches("tui.select.up", k):
		t.Move(-1)
	case b.Matches("tui.select.down", k):
		t.Move(1)
	case b.Matches("tui.select.pageUp", k):
		t.Move(-t.Max)
	case b.Matches("tui.select.pageDown", k):
		t.Move(t.Max)
	case b.Matches("app.tree.foldOrUp", k):
		t.FoldOrUp()
	case b.Matches("app.tree.unfoldOrDown", k):
		t.UnfoldOrDown()
	case b.Matches("app.tree.toggleLabelTimestamp", k):
		t.Timestamps = !t.Timestamps
	case b.Matches("app.tree.editLabel", k):
		if id := t.CurrentID(); id != "" {
			ed := NewEditor()
			ed.SetText(ui.session().Labels()[id])
			ui.label = &labelEdit{id: id, ed: ed}
		}
	case k.Name == "backspace":
		t.Backspace()
	case k.Text != "":
		t.Type(k.Text)
	}
}

func (ui *UI) labelKey(k Key) {
	b := ui.keys
	switch {
	case b.Matches("tui.select.cancel", k):
		ui.label = nil
	case b.Matches("tui.input.submit", k):
		id, text := ui.label.id, strings.TrimSpace(ui.label.ed.Text())
		ui.label = nil
		if err := ui.session().SetLabel(id, text); err != nil {
			ui.add(Block{Kind: "error", Text: err.Error(), Final: true})
		}
		ui.tree.SetMode(ui.tree.Mode, id)
	default:
		saved := ui.ed
		ui.ed = ui.label.ed
		ui.editKey(k)
		ui.ed = saved
	}
}

// StartupPrompts asks the questions that belong at startup: whether to trust a folder that has project code.
func (ui *UI) StartupPrompts() {
	if ui.a.Trusted || ui.a.UntrustedCode == 0 {
		return
	}
	n := ui.a.UntrustedCode
	ui.Ask(fmt.Sprintf("Trust %s? It has %d project extension, hook or provider entr%s that can run code.", filepath.Base(ui.a.Cwd), n,
		map[bool]string{true: "y", false: "ies"}[n == 1]), []string{"Trust this folder and load them", "Continue without them"},
		func(i int) {
			if i == 0 {
				ui.submitLine("/trust")
			}
		})
}
