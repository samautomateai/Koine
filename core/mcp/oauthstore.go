package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"koine/core/agentdir"
)

// TokenRecord is what is kept for one signed-in server: the tokens and what is needed to refresh them without discovering anything again.
type TokenRecord struct {
	ServerURL     string    `json:"serverUrl"`
	ClientID      string    `json:"clientId"`
	ClientSecret  string    `json:"clientSecret,omitempty"`
	TokenEndpoint string    `json:"tokenEndpoint"`
	AccessToken   string    `json:"accessToken"`
	RefreshToken  string    `json:"refreshToken,omitempty"`
	TokenType     string    `json:"tokenType,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"` // zero: the server did not say
}

// Expired says whether the access token is, or within a minute will be, out of date.
func (r TokenRecord) Expired(now time.Time) bool {
	return !r.ExpiresAt.IsZero() && now.Add(time.Minute).After(r.ExpiresAt)
}

// TokenStore is the file of tokens, <agent dir>/mcp-auth.json, readable by its owner only. A record belongs to a server name and URL: a
// record whose URL differs from the configured one is ignored, so repointing a name never sends the old server's token elsewhere.
type TokenStore struct {
	Path string
	mu   sync.Mutex
}

// AuthFile is the default token file.
func AuthFile(home string) string { return filepath.Join(agentdir.Of(home), "mcp-auth.json") }

type tokenFile struct {
	Servers map[string]TokenRecord `json:"servers"`
}

func (s *TokenStore) read() (tokenFile, error) {
	var f tokenFile
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s: %w", s.Path, err)
	}
	return f, nil
}

// Get returns the record for a server, if one exists for the same URL.
func (s *TokenStore) Get(name, url string) (TokenRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return TokenRecord{}, false
	}
	r, ok := f.Servers[name]
	if !ok || r.ServerURL != url {
		return TokenRecord{}, false
	}
	return r, true
}

// Put stores a record. The file is replaced atomically and is only ever created with mode 0600.
func (s *TokenStore) Put(name string, r TokenRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		f = tokenFile{} // an unreadable file is replaced rather than blocking sign-in
	}
	if f.Servers == nil {
		f.Servers = map[string]TokenRecord{}
	}
	f.Servers[name] = r
	return s.write(f)
}

// Delete removes a server's record (sign out). It reports whether there was one.
func (s *TokenStore) Delete(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return false, err
	}
	if _, ok := f.Servers[name]; !ok {
		return false, nil
	}
	delete(f.Servers, name)
	return true, s.write(f)
}

func (s *TokenStore) write(f tokenFile) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".mcp-auth-*") // CreateTemp makes the file 0600
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
