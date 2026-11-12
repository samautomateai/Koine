// Package auth keeps provider credentials: the auth.json store in the agent directory and the resolution of secret values ("!command",
// "$VAR" templates). It reads no project files, so nothing an untrusted folder contains can run through it.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileName is the credential store inside the agent directory, as in Pi.
const FileName = "auth.json"

// TypeAPIKey is the only credential type Koine can use. Pi also stores "oauth" entries; they are kept when the file is rewritten and
// reported as unusable.
const TypeAPIKey = "api_key"

// Credential is one entry of auth.json, keyed by provider id.
type Credential struct {
	Type string            `json:"type"`
	Key  string            `json:"key,omitempty"` // the key itself, "!command", or a "$VAR" template (see Resolver)
	Env  map[string]string `json:"env,omitempty"` // provider settings that win over the process environment (CLOUDFLARE_ACCOUNT_ID, ...)
}

// Store reads and writes auth.json. The zero value (no path) holds nothing and refuses to save.
type Store struct{ path string }

var mu sync.Mutex // one process, one writer at a time; there is no cross-process lock

// Open returns the store of the agent directory dir ("" = none).
func Open(dir string) *Store {
	if dir == "" {
		return &Store{}
	}
	return &Store{path: filepath.Join(dir, FileName)}
}

// Path is the file, "" when the agent directory is unknown.
func (s *Store) Path() string { return s.path }

// read returns the entries as raw JSON so that entries Koine does not understand survive a rewrite. A missing file is empty.
func (s *Store) read() (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if s.path == "" {
		return out, nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: not valid JSON (%w); fix or remove the file", s.path, err)
	}
	return out, nil
}

// Get returns the credential stored for provider.
func (s *Store) Get(provider string) (Credential, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := s.read()
	if err != nil {
		return Credential{}, false, err
	}
	raw, ok := all[provider]
	if !ok {
		return Credential{}, false, nil
	}
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credential{}, false, fmt.Errorf("%s: the entry for %q is not an object: %w", s.path, provider, err)
	}
	return c, true, nil
}

// Providers lists the providers that have an entry, sorted.
func (s *Store) Providers() ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for k := range all {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// SetAPIKey stores an API key for provider, replacing its entry. env is kept in the entry when not empty.
func (s *Store) SetAPIKey(provider, key string, env map[string]string) error {
	if provider == "" || key == "" {
		return errors.New("auth: a provider and a key are needed")
	}
	if len(env) == 0 {
		env = nil
	}
	raw, err := json.Marshal(Credential{Type: TypeAPIKey, Key: key, Env: env})
	if err != nil {
		return err
	}
	return s.update(func(all map[string]json.RawMessage) bool { all[provider] = raw; return true })
}

// Remove deletes the entry of provider and reports whether there was one. Removing nothing is not an error.
func (s *Store) Remove(provider string) (bool, error) {
	had := false
	err := s.update(func(all map[string]json.RawMessage) bool {
		if _, had = all[provider]; had {
			delete(all, provider)
		}
		return had
	})
	return had, err
}

// update reads the file, lets change edit it and, when change says so, writes it back atomically with mode 0600.
func (s *Store) update(change func(map[string]json.RawMessage) bool) error {
	if s.path == "" {
		return errors.New("auth: the agent directory is unknown, so credentials cannot be stored")
	}
	mu.Lock()
	defer mu.Unlock()
	return s.updateLocked(change)
}

// updateLocked is update for a caller that holds mu.
func (s *Store) updateLocked(change func(map[string]json.RawMessage) bool) error {
	all, err := s.read()
	if err != nil {
		return err
	}
	if !change(all) {
		return nil
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp") // created 0600, so the key is never readable by others, even briefly
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
