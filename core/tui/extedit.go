package tui

import (
	"fmt"
	"os"
	"strings"
)

// EditorCommand is the program (and arguments) the external-editor key runs: the externalEditor setting, else $VISUAL, then $EDITOR, then
// vi. Quotes and backslashes group words the way a shell does, but nothing else of shell syntax is interpreted.
func EditorCommand(setting string, env Env) []string {
	for _, s := range []string{setting, env("VISUAL"), env("EDITOR")} {
		if f := splitCommand(s); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

func splitCommand(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// EditExternally writes text to a temporary file, runs cmd with the file's path appended, and returns the file's content afterwards
// (one trailing newline removed, which editors add).
func EditExternally(text string, cmd []string, run func(cmd []string) error) (string, error) {
	f, err := os.CreateTemp("", "koine-prompt-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := run(append(append([]string(nil), cmd...), path)); err != nil {
		return "", fmt.Errorf("%s: %w", cmd[0], err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), nil
}
