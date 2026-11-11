package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Output limits shared by every tool, as in Pi: whichever of the two is reached first applies.
const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024
	GrepMaxLineLen  = 500
)

// Truncation describes what a truncation did.
type Truncation struct {
	Content               string
	Truncated             bool
	By                    string // "lines", "bytes" or "" when nothing was cut
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	LastLinePartial       bool // tail: only the end of the last line was kept
	FirstLineExceedsLimit bool // head: the first line alone is over the byte limit
}

// FormatSize renders a byte count the way Pi does ("512B", "50.0KB", "1.5MB").
func FormatSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}

// TruncateHead keeps the first lines of s that fit both limits, never a partial line (read, grep, find, ls).
func TruncateHead(s string, maxLines, maxBytes int) Truncation {
	lines := strings.Split(s, "\n")
	t := Truncation{TotalLines: len(lines), TotalBytes: len(s)}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		t.Content, t.OutputLines, t.OutputBytes = s, len(lines), len(s)
		return t
	}
	t.Truncated = true
	if len(lines[0]) > maxBytes {
		t.FirstLineExceedsLimit, t.By = true, "bytes"
		return t
	}
	t.By = "lines"
	var out []string
	size := 0
	for i := 0; i < len(lines) && i < maxLines; i++ {
		add := len(lines[i])
		if i > 0 {
			add++
		}
		if size+add > maxBytes {
			t.By = "bytes"
			break
		}
		out = append(out, lines[i])
		size += add
	}
	t.Content, t.OutputLines, t.OutputBytes = strings.Join(out, "\n"), len(out), size
	return t
}

// TruncateTail keeps the last lines of s that fit both limits (bash). When even the last line is over the byte limit, its tail is kept.
func TruncateTail(s string, maxLines, maxBytes int) Truncation {
	lines := strings.Split(s, "\n")
	t := Truncation{TotalLines: len(lines), TotalBytes: len(s)}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		t.Content, t.OutputLines, t.OutputBytes = s, len(lines), len(s)
		return t
	}
	t.Truncated, t.By = true, "lines"
	var out []string
	size := 0
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		add := len(lines[i])
		if len(out) > 0 {
			add++
		}
		if size+add > maxBytes {
			t.By = "bytes"
			if len(out) == 0 {
				out = append(out, tailBytes(lines[i], maxBytes))
				size, t.LastLinePartial = len(out[0]), true
			}
			break
		}
		out = append([]string{lines[i]}, out...)
		size += add
	}
	t.Content, t.OutputLines, t.OutputBytes = strings.Join(out, "\n"), len(out), size
	return t
}

// tailBytes returns the last n bytes of s, moved forward to a rune boundary.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// TruncateLine cuts one line to max runes and says so (grep).
func TruncateLine(s string, max int) (string, bool) {
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	r := []rune(s)
	return string(r[:max]) + "... [truncated]", true
}
