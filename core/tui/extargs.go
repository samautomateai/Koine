package tui

import (
	"context"
	"strings"
	"time"
)

// completeArgs offers the arguments of an extension command: the line is "/name rest" and the cursor is somewhere in rest. The whole of
// rest is what the suggestion replaces, as Pi's getArgumentCompletions(prefix) sees it.
func completeArgs(line []rune, row, col int, src Sources) *Completion {
	if row != 0 || src.Args == nil || len(line) < 2 || line[0] != '/' {
		return nil
	}
	col = min(col, len(line))
	name, rest, found := strings.Cut(string(line[1:col]), " ")
	if !found || name == "" || strings.Contains(name, "/") {
		return nil
	}
	items := src.Args(name, rest)
	if len(items) == 0 {
		return nil
	}
	return &Completion{Start: 1 + len([]rune(name)) + 1, End: col, Items: items, Kind: "arg"}
}

// commandArgs asks the extension that owns the command for suggestions. It waits a moment at most: this runs on a key press.
func (ui *UI) commandArgs(command, prefix string) []Suggestion {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	var out []Suggestion
	for _, i := range ui.a.CompleteCommand(ctx, command, prefix) {
		out = append(out, Suggestion{Value: i.Value, Label: i.Label})
	}
	return out
}
