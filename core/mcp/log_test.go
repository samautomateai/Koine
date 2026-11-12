package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "mcp.log")
	big := strings.Repeat("x", 100)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(big+big), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Log{Path: path, Limit: 150}
	if err := l.open(); err != nil { // already over the limit: the old content moves to .1 at open
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".1"); len(b) != 200 {
		t.Fatalf(".1 holds %d bytes", len(b))
	}
	for i := 0; i < 40; i++ { // growing past the limit again rotates during writes
		if _, err := l.Write([]byte("0123456789\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil || l.Close() != nil {
		t.Fatal("close")
	}
	if _, err := l.Write([]byte("late")); err == nil {
		t.Fatal("write after close")
	}
	if info, err := os.Stat(path); err != nil || info.Size() > 150 || info.Mode().Perm() != 0o600 {
		t.Fatalf("log file: %v %v", info, err)
	}
	if b, _ := os.ReadFile(path + ".1"); !strings.Contains(string(b), "0123456789") {
		t.Fatalf(".1 = %q", b)
	}
	opened, err := OpenLog(filepath.Join(t.TempDir(), "x", "mcp.log"))
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Close()
	if _, err := OpenLog(filepath.Join(path, "sub", "mcp.log")); err == nil { // a file where a directory is needed
		t.Fatal("want an error")
	}
	t.Setenv("KOINE_AGENT_DIR", "")
	if LogFile("/h") != filepath.Join("/h", ".koine", "mcp.log") {
		t.Fatalf("LogFile = %s", LogFile("/h"))
	}
}
