package tui

import "testing"

func names(ks []Key) []string {
	var out []string
	for _, k := range ks {
		n := k.Name
		if k.Paste {
			n = "paste:" + k.Text
		}
		out = append(out, n)
	}
	return out
}

func TestDecodeKeys(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a", "a"}, {"A", "shift+a"}, {" ", "space"}, {"\r", "enter"}, {"\n", "ctrl+j"}, {"\t", "tab"}, {"\x7f", "backspace"}, {"\x08", "backspace"},
		{"\x01", "ctrl+a"}, {"\x1a", "ctrl+z"}, {"\x1f", "ctrl+-"}, {"\x1d", "ctrl+]"}, {"\x00", "ctrl+space"}, {"\x1c", "ctrl+\\"}, {"\x1e", "ctrl+^"},
		{"\x1b[A", "up"}, {"\x1b[B", "down"}, {"\x1b[C", "right"}, {"\x1b[D", "left"}, {"\x1b[H", "home"}, {"\x1b[F", "end"},
		{"\x1b[1;3D", "alt+left"}, {"\x1b[1;5C", "ctrl+right"}, {"\x1b[1;6A", "ctrl+shift+up"}, {"\x1b[3~", "delete"}, {"\x1b[5~", "pageUp"},
		{"\x1b[6~", "pageDown"}, {"\x1b[2~", "insert"}, {"\x1b[1~", "home"}, {"\x1b[4~", "end"}, {"\x1b[15~", "f5"}, {"\x1b[24~", "f12"},
		{"\x1bOP", "f1"}, {"\x1bOA", "up"}, {"\x1b[Z", "shift+tab"}, {"\x1b[1;2P", "shift+f1"},
		{"\x1bb", "alt+b"}, {"\x1b\x7f", "alt+backspace"}, {"\x1b\r", "alt+enter"}, {"\x1b[13;2u", "shift+enter"}, {"\x1b[13u", "enter"},
		{"\x1b[97;5u", "ctrl+a"}, {"\x1b[27;2;13~", "shift+enter"}, {"\x1b[9;2u", "shift+tab"}, {"\x1b[27u", "escape"}, {"\x1b[127;3u", "alt+backspace"},
		{"\x1b[57419;1u", "up"}, {"\x1b[32;5u", "ctrl+space"}, {"\x1b[1;9A", "super+up"},
		{"\x1b\x1b[A", "alt+up"}, {"é", "é"}, {"日", "日"},
	}
	for _, c := range cases {
		ks, used := Decode([]byte(c.in), true)
		if used != len(c.in) || len(ks) != 1 || ks[0].Name != c.want {
			t.Errorf("%q: got %v used %d, want %q", c.in, names(ks), used, c.want)
		}
	}
	if ks, _ := Decode([]byte("A"), true); ks[0].Text != "A" {
		t.Error("shifted letters keep their text")
	}
}

func TestDecodeSequencesAndWaiting(t *testing.T) {
	ks, used := Decode([]byte("ab\x1b[A"), false)
	if got := names(ks); len(got) != 3 || got[2] != "up" || used != 5 {
		t.Errorf("sequence: %v %d", got, used)
	}
	// a lone ESC, and a half sequence, wait unless final
	for _, in := range []string{"\x1b", "\x1b[", "\x1b[1;", "\x1bO", "\xe6"} {
		if ks, used := Decode([]byte(in), false); len(ks) != 0 || used != 0 {
			t.Errorf("%q should wait: %v %d", in, names(ks), used)
		}
	}
	if ks, _ := Decode([]byte("\x1b"), true); len(ks) != 1 || ks[0].Name != "escape" {
		t.Error("final ESC is escape")
	}
	if ks, _ := Decode([]byte("\x1b["), true); len(ks) == 0 || ks[0].Name != "escape" {
		t.Errorf("final half sequence: %v", names(ks))
	}
	if ks, _ := Decode([]byte("\x1bO"), true); len(ks) != 1 || ks[0].Name != "alt+o" {
		t.Errorf("final ESC O: %v", names(ks))
	}
	if ks, used := Decode([]byte("\x1b[999~x"), false); len(ks) != 1 || ks[0].Name != "x" || used != 7 {
		t.Errorf("unknown sequences are dropped: %v %d", names(ks), used)
	}
	if ks, used := Decode([]byte("\x1bOZ"), false); used != 3 || len(ks) != 2 || ks[0].Name != "alt+o" || ks[1].Name != "shift+z" {
		t.Errorf("unknown SS3: %v %d", names(ks), used)
	}
	if ks, _ := Decode([]byte("\x1b\x1b"), true); len(ks) == 0 || ks[0].Name != "escape" {
		t.Errorf("double escape: %v", names(ks))
	}
	if ks, _ := Decode([]byte("\x1b[1;5u"), true); len(ks) != 0 {
		t.Errorf("control code points are ignored: %v", names(ks))
	}
}

func TestDecodePaste(t *testing.T) {
	in := "\x1b[200~line1\nline2\x1b[201~z"
	ks, used := Decode([]byte(in), false)
	if len(ks) != 2 || !ks[0].Paste || ks[0].Text != "line1\nline2" || ks[1].Name != "z" || used != len(in) {
		t.Errorf("paste: %v %d", names(ks), used)
	}
	if ks, used := Decode([]byte("\x1b[200~partial"), false); len(ks) != 0 || used != 0 {
		t.Error("an unfinished paste waits")
	}
	if ks, _ := Decode([]byte("\x1b[200~partial"), true); len(ks) != 1 || ks[0].Text != "partial" {
		t.Error("a final unfinished paste is delivered")
	}
}
