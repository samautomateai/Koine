package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// readRaw reads a config file as an untyped object so that edits keep every other key and server as written.
func readRaw(path string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &top); err != nil {
			return nil, nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("%s: mcpServers must be an object: %w", path, err)
		}
	}
	return top, servers, nil
}

func writeRaw(path string, top, servers map[string]json.RawMessage) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	top["mcpServers"] = raw
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// the file may hold tokens in headers or env, so it is private; 0600 also applies when it is replaced
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// AddServer writes a server into the mcp.json at path, replacing an entry of the same name. The entry is validated first.
func AddServer(path, name string, cfg ServerConfig) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("server name %q may only use letters, digits, _ and -", name)
	}
	if err := Validate(&cfg); err != nil {
		return err
	}
	top, servers, err := readRaw(path)
	if err != nil {
		return err
	}
	for other := range servers {
		if other != name && Normalize(other) == Normalize(name) {
			return fmt.Errorf("%s already has %q, which is the same server (names differing only in - and _ are the same)", path, other)
		}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	servers[name] = b
	return writeRaw(path, top, servers)
}

// RemoveServer deletes a server from the mcp.json at path and reports whether it was there.
func RemoveServer(path, name string) (bool, error) {
	top, servers, err := readRaw(path)
	if err != nil {
		return false, err
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	delete(servers, name)
	return true, writeRaw(path, top, servers)
}
