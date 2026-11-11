package tui

import (
	"strings"
	"testing"
)

func TestWidthAndStrip(t *testing.T) {
	cases := map[string]int{"abc": 3, "\x1b[31mred\x1b[0m": 3, "日本": 4, "á": 1, "": 0, "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\": 4}
	for s, want := range cases {
		if got := Width(s); got != want {
			t.Errorf("Width(%q)=%d want %d", s, got, want)
		}
	}
	if StripANSI("\x1b[1mhi\x1b[0m") != "hi" {
		t.Error("StripANSI")
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello world", 8, "…"); got != "hello w…" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("short", 10, "…"); got != "short" {
		t.Errorf("got %q", got)
	}
	got := Truncate("\x1b[31mhello world\x1b[0m", 5, "")
	if StripANSI(got) != "hello" || !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("styles must be closed: %q", got)
	}
	if got := Truncate("abcdef", 1, "…"); got != "a" {
		t.Errorf("tail wider than the room is dropped: %q", got)
	}
	if got := Truncate("日本語", 5, ""); got != "日本" {
		t.Errorf("wide runes: %q", got)
	}
	if Pad("ab", 4) != "ab  " || Pad("abcd", 2) != "abcd" {
		t.Error("Pad")
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("the quick brown fox jumps", 10)
	want := []string{"the quick", "brown fox", "jumps"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	long := Wrap("abcdefghijklmnop", 6)
	if len(long) != 3 || long[0] != "abcdef" || long[2] != "mnop" {
		t.Errorf("long word: %q", long)
	}
	if got := Wrap("a\nb", 10); len(got) != 2 {
		t.Errorf("newlines kept: %q", got)
	}
	if got := Wrap("x", 0); len(got) != 1 {
		t.Errorf("zero width: %q", got)
	}
	styled := Wrap("\x1b[1mbold words that wrap around\x1b[0m", 10)
	if len(styled) < 3 {
		t.Fatalf("styled: %q", styled)
	}
	for _, l := range styled {
		if Width(l) > 10 {
			t.Errorf("line too wide: %q", l)
		}
	}
	if !strings.HasPrefix(styled[1], "\x1b[1m") || !strings.HasSuffix(styled[0], "\x1b[0m") {
		t.Errorf("style must be closed and reopened across the break: %q", styled)
	}
	spaced := Wrap("aaaa      bbbb", 5)
	if len(spaced) < 2 {
		t.Errorf("spaces: %q", spaced)
	}
}
