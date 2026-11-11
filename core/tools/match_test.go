package tools

import (
	"strings"
	"testing"
)

func TestMatchNamesAndGlobs(t *testing.T) {
	cases := []struct {
		entry, name string
		want        bool
	}{
		{"read", "read", true}, {"Read", "read", true}, {"MultiEdit", "edit", true}, {"bash", "read", false},
		{"mcp__radius__*", "mcp__radius__search", true}, {"MCP__*", "mcp__x__y", true}, {"mcp__radius__*", "mcp__other__x", false},
		{"r?ad", "read", true}, {"*", "anything", true}, {"[", "x", false},
	}
	for _, c := range cases {
		if got := Match(c.entry, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.entry, c.name, got, c.want)
		}
	}
}

func TestBashGetsShellEnvironmentPerCall(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	n := 0
	RegisterBuiltinsEnv(r, dir, func() []string { n++; return []string{"KOINE_TEST_MARK=call" + string(rune('0'+n))} })
	for _, want := range []string{"call1", "call2"} {
		out, err := run(t, r, "bash", `{"command":"echo $KOINE_TEST_MARK"}`)
		if err != nil || strings.TrimSpace(out) != want {
			t.Fatalf("got %q %v, want %s", out, err, want)
		}
	}
}
