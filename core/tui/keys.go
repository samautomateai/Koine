package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Key is one decoded keypress. Name is the canonical key string ("ctrl+a", "alt+left", "shift+enter", "escape", "a"); modifiers come in
// the order ctrl, shift, alt, super, as keybindings are normalised. Text is set for characters that go into the editor, and for a bracketed
// paste (Paste true) it is the whole pasted text.
type Key struct {
	Name  string
	Text  string
	Paste bool
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// Terminal modes Koine asks for: bracketed paste, the kitty keyboard protocol's "disambiguate" flag (so Shift+Enter and Ctrl+[ are told
// apart) and xterm's modifyOtherKeys level 2 for terminals that only have that. Restore undoes all of them.
const (
	EnableKeys  = "\x1b[?2004h\x1b[>1u\x1b[>4;2m"
	RestoreKeys = "\x1b[?2004l\x1b[<u\x1b[>4;0m"
)

// modName builds "ctrl+shift+alt+super+" prefixes from the xterm modifier value (1 + bitmask: shift 1, alt 2, ctrl 4, super 8).
func modName(m int) string {
	m--
	if m <= 0 {
		return ""
	}
	var b strings.Builder
	if m&4 != 0 {
		b.WriteString("ctrl+")
	}
	if m&1 != 0 {
		b.WriteString("shift+")
	}
	if m&2 != 0 {
		b.WriteString("alt+")
	}
	if m&8 != 0 {
		b.WriteString("super+")
	}
	return b.String()
}

// Decode turns the front of buf into keys and says how many bytes it used. An incomplete escape sequence (or an unfinished paste) is left
// unconsumed unless final is set, which the reader does after a short silence so a lone Escape is not mistaken for the start of one.
func Decode(buf []byte, final bool) (keys []Key, used int) {
	for used < len(buf) {
		k, n, ok := decodeOne(buf[used:], final)
		if !ok {
			break
		}
		used += n
		if k.Name != "" || k.Text != "" || k.Paste {
			keys = append(keys, k)
		}
	}
	return keys, used
}

func decodeOne(b []byte, final bool) (Key, int, bool) {
	c := b[0]
	switch {
	case c == 0x1b:
		return decodeEsc(b, final)
	case c == '\r':
		return Key{Name: "enter"}, 1, true
	case c == '\n':
		return Key{Name: "ctrl+j"}, 1, true
	case c == '\t':
		return Key{Name: "tab"}, 1, true
	case c == 0x7f || c == 0x08:
		return Key{Name: "backspace"}, 1, true
	case c == 0:
		return Key{Name: "ctrl+space"}, 1, true
	case c == 0x1c:
		return Key{Name: "ctrl+\\"}, 1, true
	case c == 0x1d:
		return Key{Name: "ctrl+]"}, 1, true
	case c == 0x1e:
		return Key{Name: "ctrl+^"}, 1, true
	case c == 0x1f:
		return Key{Name: "ctrl+-"}, 1, true
	case c < 0x20:
		return Key{Name: "ctrl+" + string(rune('a'+c-1))}, 1, true
	}
	if !utf8.FullRune(b) && !final {
		return Key{}, 0, false
	}
	r, n := utf8.DecodeRune(b)
	return textKey(r), n, true
}

func textKey(r rune) Key {
	if r == ' ' {
		return Key{Name: "space", Text: " "}
	}
	s := string(r)
	if l := strings.ToLower(s); l != s && len(l) == 1 { // a capital letter is shift+letter
		return Key{Name: "shift+" + l, Text: s}
	}
	return Key{Name: s, Text: s}
}

func decodeEsc(b []byte, final bool) (Key, int, bool) {
	if len(b) == 1 {
		if final {
			return Key{Name: "escape"}, 1, true
		}
		return Key{}, 0, false
	}
	switch b[1] {
	case '[':
		return decodeCSI(b, final)
	case 'O':
		if len(b) < 3 {
			return waitOr(final, Key{Name: "alt+o"}, 2)
		}
		if name, ok := ss3[b[2]]; ok {
			return Key{Name: name}, 3, true
		}
		return Key{Name: "alt+o"}, 2, true
	case 0x1b: // ESC ESC [A is alt+up on some terminals
		k, n, ok := decodeEsc(b[1:], final)
		if ok && k.Name != "" && k.Name != "escape" {
			return Key{Name: "alt+" + k.Name}, n + 1, true
		}
		return Key{Name: "escape"}, 1, true
	}
	k, n, ok := decodeOne(b[1:], final)
	if !ok {
		return waitOr(final, Key{Name: "escape"}, 1)
	}
	if k.Name == "" || strings.HasPrefix(k.Name, "alt+") {
		return Key{Name: "escape"}, 1, true
	}
	return Key{Name: "alt+" + k.Name}, n + 1, true
}

func waitOr(final bool, k Key, n int) (Key, int, bool) {
	if final {
		return k, n, true
	}
	return Key{}, 0, false
}

var ss3 = map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end", 'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4"}

var csiLetters = map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end", 'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4"}

var csiTilde = map[int]string{1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pageUp", 6: "pageDown", 7: "home", 8: "end",
	11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5", 17: "f6", 18: "f7", 19: "f8", 20: "f9", 21: "f10", 23: "f11", 24: "f12"}

func decodeCSI(b []byte, final bool) (Key, int, bool) {
	if len(b) >= len(pasteStart) && string(b[:len(pasteStart)]) == pasteStart {
		rest := string(b[len(pasteStart):])
		i := strings.Index(rest, pasteEnd)
		if i < 0 {
			if !final {
				return Key{}, 0, false
			}
			return Key{Paste: true, Text: rest}, len(b), true
		}
		return Key{Paste: true, Text: rest[:i]}, len(pasteStart) + i + len(pasteEnd), true
	}
	i := 2
	for i < len(b) && b[i] >= 0x20 && b[i] <= 0x3f {
		i++
	}
	if i >= len(b) {
		return waitOr(final, Key{Name: "escape"}, 1)
	}
	params, f := string(b[2:i]), b[i]
	n := i + 1
	nums := splitNums(params)
	mod := 1
	if len(nums) >= 2 && nums[1] > 0 {
		mod = nums[1]
	}
	switch {
	case f == 'Z':
		return Key{Name: "shift+tab"}, n, true
	case f == 'u' && len(nums) >= 1:
		return Key{Name: kittyName(nums[0], mod)}, n, true
	case f == '~' && len(nums) >= 1 && nums[0] == 27 && len(nums) >= 3: // modifyOtherKeys: ESC [ 27 ; mod ; code ~
		return Key{Name: kittyName(nums[2], nums[1])}, n, true
	case f == '~' && len(nums) >= 1:
		if name, ok := csiTilde[nums[0]]; ok {
			return Key{Name: modName(mod) + name}, n, true
		}
	default:
		if name, ok := csiLetters[f]; ok {
			return Key{Name: modName(mod) + name}, n, true
		}
	}
	return Key{}, n, true // an unknown sequence is dropped
}

func splitNums(p string) []int {
	if p == "" {
		return nil
	}
	var out []int
	for _, f := range strings.Split(p, ";") {
		f, _, _ = strings.Cut(f, ":")
		v, _ := strconv.Atoi(f)
		out = append(out, v)
	}
	return out
}

func kittyName(code, mod int) string {
	var base string
	switch code {
	case 13, 57414:
		base = "enter"
	case 9:
		base = "tab"
	case 27:
		base = "escape"
	case 127, 8:
		base = "backspace"
	case 32:
		base = "space"
	case 57417:
		base = "left"
	case 57418:
		base = "right"
	case 57419:
		base = "up"
	case 57420:
		base = "down"
	default:
		if code < 32 {
			return ""
		}
		base = strings.ToLower(string(rune(code)))
	}
	return modName(mod) + base
}
