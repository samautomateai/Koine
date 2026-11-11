package tools

import (
	"strings"
	"testing"
)

func TestFormatSize(t *testing.T) {
	for n, want := range map[int]string{0: "0B", 512: "512B", 1024: "1.0KB", 51200: "50.0KB", 1572864: "1.5MB"} {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d)=%q want %q", n, got, want)
		}
	}
}

func TestTruncateHead(t *testing.T) {
	small := TruncateHead("a\nb", 10, 100)
	if small.Truncated || small.Content != "a\nb" || small.TotalLines != 2 || small.OutputLines != 2 {
		t.Fatalf("%+v", small)
	}
	byLines := TruncateHead("1\n2\n3\n4\n5", 3, 1000)
	if !byLines.Truncated || byLines.By != "lines" || byLines.Content != "1\n2\n3" || byLines.OutputLines != 3 || byLines.TotalLines != 5 {
		t.Fatalf("%+v", byLines)
	}
	byBytes := TruncateHead("aaaa\nbbbb\ncccc", 100, 9)
	if !byBytes.Truncated || byBytes.By != "bytes" || byBytes.Content != "aaaa\nbbbb" {
		t.Fatalf("%+v", byBytes)
	}
	first := TruncateHead(strings.Repeat("x", 20)+"\nshort", 100, 10)
	if !first.FirstLineExceedsLimit || first.Content != "" || !first.Truncated {
		t.Fatalf("%+v", first)
	}
}

func TestTruncateTail(t *testing.T) {
	if got := TruncateTail("a\nb", 10, 100); got.Truncated || got.Content != "a\nb" {
		t.Fatalf("%+v", got)
	}
	byLines := TruncateTail("1\n2\n3\n4\n5", 2, 1000)
	if !byLines.Truncated || byLines.By != "lines" || byLines.Content != "4\n5" || byLines.TotalLines != 5 {
		t.Fatalf("%+v", byLines)
	}
	byBytes := TruncateTail("aaaa\nbbbb\ncccc", 100, 9)
	if byBytes.By != "bytes" || byBytes.Content != "bbbb\ncccc" {
		t.Fatalf("%+v", byBytes)
	}
	partial := TruncateTail("short\n"+strings.Repeat("\u00e9", 10), 100, 7)
	if !partial.LastLinePartial || partial.By != "bytes" || partial.Content != "\u00e9\u00e9\u00e9" || partial.OutputBytes != 6 {
		t.Fatalf("%+v", partial)
	}
}

func TestTruncateLine(t *testing.T) {
	if s, cut := TruncateLine("short", 10); cut || s != "short" {
		t.Fatal(s, cut)
	}
	if s, cut := TruncateLine("h\u00e9llo w\u00f6rld", 5); !cut || s != "h\u00e9llo... [truncated]" {
		t.Fatal(s, cut)
	}
}
