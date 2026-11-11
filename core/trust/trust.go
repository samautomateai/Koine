// Package trust decides whether project-local *code* (extensions, hooks,
// provider endpoints) may run. Opening a freshly cloned repository must never
// execute its .pi/extensions or .claude hooks on its own.
package trust

import (
	"encoding/json"
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"strings"
)

// Store persists trusted project roots in ~/.koine/trust.json.
type Store struct {
	path    string
	Trusted []string `json:"trusted"`
}

func Open(home string) *Store {
	s := &Store{path: filepath.Join(agentdir.Of(home), "trust.json")}
	if home == "" {
		return s
	}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}

// IsTrusted reports whether dir, or any ancestor of it, was trusted.
func (s *Store) IsTrusted(dir string) bool {
	dir = clean(dir)
	for _, t := range s.Trusted {
		t = clean(t)
		if dir == t || strings.HasPrefix(dir, t+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Add trusts dir and saves the store.
func (s *Store) Add(dir string) error {
	dir = clean(dir)
	if s.IsTrusted(dir) {
		return nil
	}
	s.Trusted = append(s.Trusted, dir)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

// FromEnv is true when KOINE_TRUST=1 (CI / containers where everything is disposable).
func FromEnv() bool { v := os.Getenv("KOINE_TRUST"); return v == "1" || v == "true" }

func clean(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}
