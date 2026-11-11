package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileTools holds what read, write and edit share.
type fileTools struct{ o Options }

func (f fileTools) read(ctx context.Context, a json.RawMessage) (string, error) {
	var p struct {
		Path          string
		Offset, Limit int
	}
	if err := json.Unmarshal(a, &p); err != nil {
		return "", err
	}
	full := ResolveReadPath(p.Path, f.o.Cwd, f.o.Home)
	if text, ok, err := readImage(ctx, full); ok || (err != nil && !errors.Is(err, os.ErrNotExist)) { // images become attachments (readimage.go)
		return text, err
	}
	data, err := os.ReadFile(full)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("file not found: %s", p.Path)
	case err != nil:
		return "", err
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return "", fmt.Errorf("%s is a binary file (%s); read only handles text. Use bash to inspect it", p.Path, FormatSize(len(data)))
	}
	_, text := SplitBOM(string(data))
	all := strings.Split(text, "\n")
	start := 0
	if p.Offset > 1 {
		start = p.Offset - 1
	}
	if start >= len(all) {
		return "", fmt.Errorf("offset %d is beyond end of file (%d lines total)", p.Offset, len(all))
	}
	sel, limited := all[start:], 0
	if p.Limit > 0 && start+p.Limit < len(all) {
		sel, limited = all[start:start+p.Limit], p.Limit
	}
	t := TruncateHead(strings.Join(sel, "\n"), DefaultMaxLines, DefaultMaxBytes)
	first := start + 1
	switch {
	case t.FirstLineExceedsLimit:
		return fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
			first, FormatSize(len(sel[0])), FormatSize(DefaultMaxBytes), first, p.Path, DefaultMaxBytes), nil
	case t.Truncated:
		last := first + t.OutputLines - 1
		if t.By == "lines" {
			return fmt.Sprintf("%s\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", t.Content, first, last, len(all), last+1), nil
		}
		return fmt.Sprintf("%s\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
			t.Content, first, last, len(all), FormatSize(DefaultMaxBytes), last+1), nil
	case limited > 0:
		return fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", t.Content, len(all)-start-limited, start+limited+1), nil
	}
	return t.Content, nil
}

func (f fileTools) write(_ context.Context, a json.RawMessage) (string, error) {
	var p struct{ Path, Content string }
	if err := json.Unmarshal(a, &p); err != nil {
		return "", err
	}
	full := ResolvePath(p.Path, f.o.Cwd, f.o.Home)
	return WithFileMutation(full, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully wrote %d bytes to %s", len(p.Content), p.Path), nil
	})
}

// editArgs accepts the current shape (edits[]) and the older single-replacement shapes models still send: top-level oldText/newText, and the
// old/new names of Claude-style tools.
type editArgs struct {
	Path    string
	Edits   []Edit
	OldText *string
	NewText *string
	Old     *string
	New     *string
}

func (e editArgs) all() []Edit {
	out := append([]Edit(nil), e.Edits...)
	oldT, newT := e.OldText, e.NewText
	if oldT == nil {
		oldT, newT = e.Old, e.New
	}
	if oldT != nil {
		n := ""
		if newT != nil {
			n = *newT
		}
		out = append(out, Edit{OldText: *oldT, NewText: n})
	}
	return out
}

func (f fileTools) edit(_ context.Context, a json.RawMessage) (string, error) {
	var p editArgs
	if err := json.Unmarshal(a, &p); err != nil {
		return "", err
	}
	edits := p.all()
	if len(edits) == 0 {
		return "", errors.New("edit needs edits: [{oldText, newText}, …]")
	}
	full := ResolvePath(p.Path, f.o.Cwd, f.o.Home)
	return WithFileMutation(full, func() (string, error) {
		raw, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("file not found: %s", p.Path)
		} else if err != nil {
			return "", err
		}
		mark, text := SplitBOM(string(raw))
		ending := DetectLineEnding(text)
		oldText := NormalizeToLF(text)
		newText, err := ApplyEdits(oldText, edits, p.Path)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(mark+RestoreLineEnding(newText, ending)), 0o644); err != nil {
			return "", err
		}
		diff, line := UnifiedDiff(oldText, newText, p.Path)
		return fmt.Sprintf("Successfully replaced %d block(s) in %s.\nFirst changed line: %d\n\n%s", len(edits), p.Path, line, diff), nil
	})
}
