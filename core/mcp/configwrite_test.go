package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddAndRemoveServerKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "mcp.json")
	if err := AddServer(path, "fs", ServerConfig{Command: "npx", Args: []string{"-y", "server-fs", "."}}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	// hand-written content around our entries survives
	b, _ := os.ReadFile(path)
	edited := strings.Replace(string(b), `"mcpServers"`, `"note": "mine", "mcpServers"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(path, "docs", ServerConfig{URL: "https://d.example/mcp", Headers: map[string]string{"Authorization": "Bearer ${T}"}}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), `"note": "mine"`) || !strings.Contains(string(b), `"fs"`) || !strings.HasSuffix(string(b), "}\n") ||
		!strings.Contains(string(b), `"type": "http"`) {
		t.Fatalf("file = %s", b)
	}
	if err := AddServer(path, "fs", ServerConfig{Command: "other"}); err != nil { // replaces
		t.Fatal(err)
	}
	for _, bad := range []struct {
		name string
		cfg  ServerConfig
	}{{"bad name", ServerConfig{Command: "x"}}, {"x", ServerConfig{}}, {"docs", ServerConfig{Command: "x", URL: "https://x"}}} {
		if err := AddServer(path, bad.name, bad.cfg); err == nil {
			t.Errorf("AddServer(%q, %+v) should fail", bad.name, bad.cfg)
		}
	}
	if err := AddServer(path, "do-cs", ServerConfig{Command: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(path, "do_cs", ServerConfig{Command: "x"}); err == nil || !strings.Contains(err.Error(), "same server") {
		t.Fatalf("err = %v", err)
	}
	if gone, err := RemoveServer(path, "docs"); !gone || err != nil {
		t.Fatalf("remove = %v %v", gone, err)
	}
	if gone, err := RemoveServer(path, "docs"); gone || err != nil {
		t.Fatalf("second remove = %v %v", gone, err)
	}
	if b, _ = os.ReadFile(path); strings.Contains(string(b), "docs") || !strings.Contains(string(b), "mine") {
		t.Fatalf("file = %s", b)
	}
	for _, content := range []string{"{nope", `{"mcpServers": 5}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := AddServer(path, "x", ServerConfig{Command: "x"}); err == nil {
			t.Errorf("%q should be refused rather than overwritten", content)
		}
		if _, err := RemoveServer(path, "x"); err == nil {
			t.Errorf("remove on %q", content)
		}
	}
	if _, err := RemoveServer(filepath.Join(t.TempDir(), "none.json"), "x"); err != nil {
		t.Fatal("a missing file has nothing to remove, which is fine")
	}
	if err := AddServer(filepath.Join(path, "x", "mcp.json"), "x", ServerConfig{Command: "x"}); err == nil {
		t.Fatal("unwritable path")
	}
}
