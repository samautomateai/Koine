package agentdir

import (
	"path/filepath"
	"testing"
)

func TestOf(t *testing.T) {
	t.Setenv(EnvVar, "")
	if got := Of("/h"); got != filepath.Join("/h", ".koine") {
		t.Errorf("default: %q", got)
	}
	if Of("") != "" {
		t.Error("no home and no override: unknown")
	}
	t.Setenv(EnvVar, "/custom/agent")
	if Of("/h") != "/custom/agent" || Of("") != "/custom/agent" {
		t.Error("the override wins, with or without a home")
	}
	t.Setenv(EnvVar, "~/pi-like")
	if got := Of("/h"); got != filepath.Join("/h", "pi-like") {
		t.Errorf("tilde: %q", got)
	}
	t.Setenv(EnvVar, "~")
	if Of("/h") != "/h" {
		t.Error("a bare ~ is the home directory")
	}
}
