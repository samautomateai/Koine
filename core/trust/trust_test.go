package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreTrustAndAncestors(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(t.TempDir(), "repo")
	sub := filepath.Join(proj, "pkg", "x")
	_ = os.MkdirAll(sub, 0o755)

	s := Open(home)
	if s.IsTrusted(proj) {
		t.Fatal("nothing is trusted by default")
	}
	if err := s.Add(proj); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(proj); err != nil || len(s.Trusted) != 1 { // idempotent
		t.Fatalf("%v %v", err, s.Trusted)
	}
	if !s.IsTrusted(proj) || !s.IsTrusted(sub) {
		t.Fatal("trusting a root trusts its subdirectories")
	}
	if s.IsTrusted(proj + "-evil") { // prefix must respect path boundaries
		t.Fatal("sibling with common prefix must not be trusted")
	}
	// persisted, owner-only
	if !Open(home).IsTrusted(proj) {
		t.Fatal("not persisted")
	}
	if st, _ := os.Stat(filepath.Join(home, ".koine", "trust.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if Open("").IsTrusted(proj) {
		t.Fatal("no home: nothing trusted")
	}
}

func TestFromEnv(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "1": true, "true": true} {
		t.Setenv("KOINE_TRUST", v)
		if FromEnv() != want {
			t.Errorf("%q", v)
		}
	}
}
