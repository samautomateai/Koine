package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KeyAction is one entry of Pi's keybinding table: its id, default keys and a short description for /hotkeys.
type KeyAction struct {
	ID, Desc string
	Keys     []string
}

// Actions lists the ids Koine acts on, with Pi's default keys (https://pi.dev/docs/latest/keybindings, Linux/macOS column). Ids Pi
// documents for features Koine does not have (fullscreen scrolling, image paste, the message queue, the scoped-model reorder keys)
// are accepted in keybindings.json and kept, but nothing reads them (see knownInert).
var Actions = []KeyAction{
	{"tui.editor.cursorUp", "Cursor up", []string{"up"}},
	{"tui.editor.cursorDown", "Cursor down", []string{"down"}},
	{"tui.editor.historyPrevious", "Previous prompt in history", nil},
	{"tui.editor.historyNext", "Next prompt in history", nil},
	{"tui.editor.cursorLeft", "Cursor left", []string{"left", "ctrl+b"}},
	{"tui.editor.cursorRight", "Cursor right", []string{"right", "ctrl+f"}},
	{"tui.editor.cursorWordLeft", "Cursor word left", []string{"alt+left", "ctrl+left", "alt+b"}},
	{"tui.editor.cursorWordRight", "Cursor word right", []string{"alt+right", "ctrl+right", "alt+f"}},
	{"tui.editor.cursorLineStart", "Start of line", []string{"home", "ctrl+a"}},
	{"tui.editor.cursorLineEnd", "End of line", []string{"end", "ctrl+e"}},
	{"tui.editor.pageUp", "Page up", []string{"pageUp", "ctrl+pageUp"}},
	{"tui.editor.pageDown", "Page down", []string{"pageDown", "ctrl+pageDown"}},
	{"tui.editor.deleteCharBackward", "Delete character backward", []string{"backspace"}},
	{"tui.editor.deleteCharForward", "Delete character forward", []string{"delete", "ctrl+d"}},
	{"tui.editor.deleteWordBackward", "Delete word backward", []string{"ctrl+w", "alt+backspace"}},
	{"tui.editor.deleteWordForward", "Delete word forward", []string{"alt+d", "alt+delete"}},
	{"tui.editor.deleteToLineStart", "Delete to line start", []string{"ctrl+u"}},
	{"tui.editor.deleteToLineEnd", "Delete to line end", []string{"ctrl+k"}},
	{"tui.editor.yank", "Paste the last killed text", []string{"ctrl+y"}},
	{"tui.editor.yankPop", "Cycle the kill ring after a paste", []string{"alt+y"}},
	{"tui.editor.undo", "Undo", []string{"ctrl+-"}},
	{"tui.input.newLine", "Insert a newline", []string{"shift+enter", "ctrl+j"}},
	{"tui.input.submit", "Send the prompt", []string{"enter"}},
	{"tui.input.tab", "Complete", []string{"tab"}},
	{"tui.select.up", "Selection up", []string{"up"}},
	{"tui.select.down", "Selection down", []string{"down"}},
	{"tui.select.pageUp", "Selection page up", []string{"pageUp"}},
	{"tui.select.pageDown", "Selection page down", []string{"pageDown"}},
	{"tui.select.confirm", "Confirm selection", []string{"enter"}},
	{"tui.select.cancel", "Cancel selection", []string{"escape", "ctrl+c"}},
	{"app.interrupt", "Interrupt the running turn", []string{"escape"}},
	{"app.clear", "Clear the editor (twice to exit)", []string{"ctrl+c"}},
	{"app.exit", "Exit when the editor is empty", []string{"ctrl+d"}},
	{"app.suspend", "Suspend to the shell", []string{"ctrl+z"}},
	{"app.editor.external", "Edit the prompt in $EDITOR", []string{"ctrl+g"}},
	{"app.session.new", "New session", nil},
	{"app.session.tree", "Open the session tree", nil},
	{"app.session.fork", "Fork the session", nil},
	{"app.session.resume", "Resume a session", nil},
	{"app.model.select", "Select a model", []string{"ctrl+l"}},
	{"app.model.cycleForward", "Next scoped model", []string{"ctrl+p"}},
	{"app.model.cycleBackward", "Previous scoped model", []string{"ctrl+shift+p"}},
	{"app.models.save", "Save the model scope", []string{"ctrl+s"}},
	{"app.models.enableAll", "Scoped models: enable all", []string{"ctrl+a"}},
	{"app.models.clearAll", "Scoped models: clear all", []string{"ctrl+x"}},
	{"app.thinking.cycle", "Cycle the thinking level", []string{"shift+tab"}},
	{"app.thinking.toggle", "Show or hide thinking blocks", []string{"ctrl+t"}},
	{"app.tools.expand", "Expand or collapse tool output", []string{"ctrl+o"}},
	{"tui.altScreen.pageUp", "Fullscreen: transcript page up", []string{"pageUp"}},
	{"tui.altScreen.pageDown", "Fullscreen: transcript page down", []string{"pageDown"}},
	{"tui.altScreen.halfPageUp", "Fullscreen: transcript half page up", nil},
	{"tui.altScreen.halfPageDown", "Fullscreen: transcript half page down", nil},
	{"tui.altScreen.lineUp", "Fullscreen: transcript line up", nil},
	{"tui.altScreen.lineDown", "Fullscreen: transcript line down", nil},
	{"tui.altScreen.top", "Fullscreen: start of the transcript", []string{"ctrl+home"}},
	{"tui.altScreen.bottom", "Fullscreen: end of the transcript, follow output", []string{"ctrl+end"}},
	{"app.message.copy", "Copy the last answer", []string{"ctrl+x"}},
	{"app.tree.foldOrUp", "Tree: fold, or go to the parent", []string{"ctrl+left", "alt+left"}},
	{"app.tree.unfoldOrDown", "Tree: unfold, or go to the child", []string{"ctrl+right", "alt+right"}},
	{"app.tree.editLabel", "Tree: edit the label", []string{"shift+l"}},
	{"app.tree.toggleLabelTimestamp", "Tree: show label timestamps", []string{"shift+t"}},
	{"app.tree.filter.default", "Tree filter: default", []string{"ctrl+d"}},
	{"app.tree.filter.noTools", "Tree filter: no tools", []string{"ctrl+t"}},
	{"app.tree.filter.userOnly", "Tree filter: user messages", []string{"ctrl+u"}},
	{"app.tree.filter.labeledOnly", "Tree filter: labelled", []string{"ctrl+l"}},
	{"app.tree.filter.all", "Tree filter: all entries", []string{"ctrl+a"}},
	{"app.tree.filter.cycleForward", "Tree filter: next", []string{"ctrl+o"}},
	{"app.tree.filter.cycleBackward", "Tree filter: previous", []string{"ctrl+shift+o"}},
}

// knownInert are ids Pi documents that Koine accepts in keybindings.json without acting on them.
var knownInert = []string{
	"tui.editor.jumpForward", "tui.editor.jumpBackward", "tui.input.copy",
	"tui.altScreen.previousPrompt", "tui.altScreen.nextPrompt", "tui.altScreen.search", "tui.altScreen.searchNext",
	"tui.altScreen.searchPrevious", "tui.altScreen.searchClose",
	"app.clipboard.pasteImage", "app.session.togglePath", "app.session.toggleSort", "app.session.toggleNamedFilter", "app.session.rename",
	"app.session.delete", "app.session.deleteNoninvasive", "app.thinking.save", "app.message.followUp", "app.message.dequeue",
	"app.models.toggleProvider", "app.models.reorderUp", "app.models.reorderDown",
}

var modOrder = map[string]int{"ctrl": 0, "shift": 1, "alt": 2, "super": 3}

var keyAliases = map[string]string{"esc": "escape", "return": "enter"}

// CanonKey normalises a key string: lower case, aliases resolved (esc, return), modifiers in the order ctrl, shift, alt, super. It returns
// "" and false for a string that is not a key.
func CanonKey(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	parts := strings.Split(s, "+")
	if s == "+" {
		return "+", true
	}
	if strings.HasSuffix(s, "++") { // "ctrl++"
		parts = append(parts[:len(parts)-2], "+")
	}
	key := strings.ToLower(parts[len(parts)-1])
	if a, ok := keyAliases[key]; ok {
		key = a
	}
	var mods []string
	for _, m := range parts[:len(parts)-1] {
		m = strings.ToLower(m)
		if _, ok := modOrder[m]; !ok {
			return "", false
		}
		mods = append(mods, m)
	}
	if !validKey(key) {
		return "", false
	}
	sort.SliceStable(mods, func(i, j int) bool { return modOrder[mods[i]] < modOrder[mods[j]] })
	return strings.Join(append(mods, key), "+"), true
}

var specialKeys = map[string]bool{"escape": true, "enter": true, "tab": true, "space": true, "backspace": true, "delete": true, "insert": true,
	"clear": true, "home": true, "end": true, "pageup": true, "pagedown": true, "up": true, "down": true, "left": true, "right": true}

func validKey(k string) bool {
	if specialKeys[k] {
		return true
	}
	if len(k) >= 2 && k[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(k, "f%d", &n); err == nil && n >= 1 && n <= 12 {
			return true
		}
	}
	return len([]rune(k)) == 1 && !strings.ContainsAny(k, " \t")
}

// Bindings maps action ids to key strings.
type Bindings struct{ keys map[string][]string }

// DefaultBindings returns Pi's defaults.
func DefaultBindings() *Bindings {
	b := &Bindings{keys: map[string][]string{}}
	for _, a := range Actions {
		for _, k := range a.Keys {
			c, _ := CanonKey(k)
			b.keys[a.ID] = append(b.keys[a.ID], c)
		}
	}
	return b
}

// LoadBindings reads <dir>/keybindings.json over the defaults. A missing file is fine. Problems (bad JSON, unknown ids, keys that are
// not keys) are returned as warnings and the affected entries are ignored. A configured value replaces the action's defaults; an empty
// array removes them.
func LoadBindings(dir string) (*Bindings, []string) {
	b := DefaultBindings()
	if dir == "" {
		return b, nil
	}
	path := filepath.Join(dir, "keybindings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return b, nil
	}
	return b, b.apply(raw, path)
}

func (b *Bindings) apply(raw []byte, path string) []string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	known := map[string]bool{}
	for _, a := range Actions {
		known[a.ID] = true
	}
	for _, id := range knownInert {
		known[id] = true
	}
	var warns []string
	ids := make([]string, 0, len(doc))
	for id := range doc {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if id == "$schema" {
			continue
		}
		if !known[id] {
			warns = append(warns, fmt.Sprintf("%s: unknown keybinding %q", path, id))
			continue
		}
		var list []string
		var one string
		if json.Unmarshal(doc[id], &one) == nil {
			list = []string{one}
		} else if json.Unmarshal(doc[id], &list) != nil {
			warns = append(warns, fmt.Sprintf("%s: %q needs a key or an array of keys", path, id))
			continue
		}
		keys := []string{}
		for _, k := range list {
			c, ok := CanonKey(k)
			if !ok {
				warns = append(warns, fmt.Sprintf("%s: %q is not a key (in %s)", path, k, id))
				continue
			}
			keys = append(keys, c)
		}
		b.keys[id] = keys
	}
	return warns
}

// Keys returns the canonical keys bound to id.
func (b *Bindings) Keys(id string) []string { return b.keys[id] }

// Matches reports whether k triggers the action id.
func (b *Bindings) Matches(id string, k Key) bool {
	if k.Name == "" || k.Paste {
		return false
	}
	name, ok := CanonKey(k.Name)
	if !ok {
		name = strings.ToLower(k.Name)
	}
	for _, c := range b.keys[id] {
		if c == name {
			return true
		}
	}
	return false
}

// Any reports whether k triggers any of the ids.
func (b *Bindings) Any(k Key, ids ...string) bool {
	for _, id := range ids {
		if b.Matches(id, k) {
			return true
		}
	}
	return false
}

// Hotkeys renders the active bindings, one line per action that has a key, for /hotkeys.
func (b *Bindings) Hotkeys() string {
	var lines []string
	for _, a := range Actions {
		if k := b.keys[a.ID]; len(k) > 0 {
			lines = append(lines, fmt.Sprintf("%-34s %-26s %s", a.ID, strings.Join(k, ", "), a.Desc))
		}
	}
	return strings.Join(lines, "\n")
}
