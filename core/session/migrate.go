package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// migrateData brings a version 1 or 2 session file up to version 3, as Pi does on load:
//   - v1 (no id/parentId): every entry gets an id and the previous entry as its parent; a compaction's firstKeptEntryIndex becomes
//     firstKeptEntryId.
//   - v2 to v3: the message role hookMessage is renamed custom.
//
// Version 3 data is returned untouched. With persist, the original is first copied to <path>.v<N>.bak and the file is replaced atomically,
// so later appends never mix versions in one file. Migrated entries are re-encoded; everything in them is kept, only key order changes.
func migrateData(path string, data []byte, persist bool) ([]byte, error) {
	lines := bytes.Split(data, []byte("\n"))
	first := -1
	for i, l := range lines {
		if len(bytes.TrimSpace(l)) > 0 {
			first = i
			break
		}
	}
	if first < 0 {
		return data, nil
	}
	var h map[string]json.RawMessage
	if json.Unmarshal(lines[first], &h) != nil || string(h["type"]) != `"session"` {
		return data, nil // Open reports the bad header
	}
	var ver int
	_ = json.Unmarshal(h["version"], &ver)
	if ver >= Version {
		return data, nil
	}
	if ver < 1 {
		ver = 1
	}
	out, err := upgrade(h, lines[first+1:], ver)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if persist {
		if err := replaceFile(path, ver, data, out); err != nil {
			return nil, fmt.Errorf("%s: migrating from version %d: %w", path, ver, err)
		}
	}
	return out, nil
}

func upgrade(h map[string]json.RawMessage, rest [][]byte, ver int) ([]byte, error) {
	h["version"] = json.RawMessage("3")
	hb, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(hb)
	out.WriteByte('\n')
	var ids []string
	taken := map[string]bool{}
	for n, l := range rest {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		var e map[string]json.RawMessage
		if err := json.Unmarshal(l, &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", n+2, err)
		}
		if ver < 2 {
			var id string
			_ = json.Unmarshal(e["id"], &id)
			for id == "" || taken[id] {
				id = newID(4)
			}
			e["id"] = quote(id)
			if len(ids) == 0 {
				e["parentId"] = json.RawMessage("null")
			} else {
				e["parentId"] = quote(ids[len(ids)-1])
			}
			ids, taken[id] = append(ids, id), true
			var idx int
			if raw, ok := e["firstKeptEntryIndex"]; ok && json.Unmarshal(raw, &idx) == nil {
				if idx >= 0 && idx < len(ids) {
					e["firstKeptEntryId"] = quote(ids[idx])
				}
				delete(e, "firstKeptEntryIndex")
			}
		}
		if string(e["type"]) == `"message"` {
			e["message"] = renameHookRole(e["message"])
		}
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

func quote(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

// renameHookRole turns role hookMessage into custom (the version 3 change); other messages are returned as they were.
func renameHookRole(msg json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(msg, &m) != nil || string(m["role"]) != `"hookMessage"` {
		return msg
	}
	m["role"] = quote("custom")
	b, err := json.Marshal(m)
	if err != nil {
		return msg
	}
	return b
}

func replaceFile(path string, ver int, orig, migrated []byte) error {
	bak := fmt.Sprintf("%s.v%d.bak", path, ver)
	if _, err := os.Stat(bak); err != nil {
		if err := os.WriteFile(bak, orig, 0o600); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, migrated, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
