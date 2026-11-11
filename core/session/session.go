// Package session persists conversations as Pi v3 JSONL files (a header line, then entries linked by id/parentId), so a session can be
// resumed by Koine and Pi session files can be read. Only what Koine understands is interpreted; every line is preserved on disk.
package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Version is the session file format version written and understood.
const Version = 3

type header struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

// Entry is one non-header line. Fields not listed here are ignored on read; the file keeps them.
type Entry struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	ParentID         *string         `json:"parentId"`
	Timestamp        string          `json:"timestamp"`
	Message          json.RawMessage `json:"message,omitempty"`
	Name             string          `json:"name,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int             `json:"tokensBefore,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	ModelID          string          `json:"modelId,omitempty"`
	TargetID         string          `json:"targetId,omitempty"` // label: the entry being labelled
	Label            string          `json:"label,omitempty"`    // label: empty (absent) clears the label
	FromID           string          `json:"fromId,omitempty"`   // branch_summary: the leaf the summary was written from
	CustomType       string          `json:"customType,omitempty"`
	Content          json.RawMessage `json:"content,omitempty"`       // custom_message
	Data             json.RawMessage `json:"data,omitempty"`          // custom: the extension's own data
	ThinkingLevel    string          `json:"thinkingLevel,omitempty"` // thinking_level_change

	raw []byte // the line as read, so copies keep fields Koine does not know
}

// Session is an open session file. The zero file is created lazily on the first Append, so empty sessions leave nothing behind.
type Session struct {
	Path    string
	ID      string
	Cwd     string
	Parent  string // parentSession of a fork
	entries []Entry
	leaf    string
	started bool // header written (or loaded)

	// OnAppend, if set, is called with every entry written to this session after it was written (not for entries copied by a fork).
	OnAppend func(Entry)
}

func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Create makes a new, not yet written session for cwd in dir.
func Create(dir, cwd string) *Session {
	id := uuid()
	ts := strings.NewReplacer(":", "-", ".", "-").Replace(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	return &Session{Path: filepath.Join(dir, ts+"_"+id+".jsonl"), ID: id, Cwd: cwd}
}

// Open reads a session file, migrating a version 1 or 2 file to version 3 first (the original is kept as <file>.v<N>.bak). Unknown entry
// types are kept out of the model's context but never rewritten.
func Open(path string) (*Session, error) { return openFile(path, true) }

func openFile(path string, persistMigration bool) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if data, err = migrateData(path, data, persistMigration); err != nil {
		return nil, err
	}
	s := &Session{Path: path, started: true}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 0; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if n == 0 {
			var h header
			if err := json.Unmarshal([]byte(line), &h); err != nil || h.Type != "session" {
				return nil, fmt.Errorf("%s: not a session file (first line must be a session header)", path)
			}
			if h.Version > Version {
				return nil, fmt.Errorf("%s: session version %d is newer than the supported version %d", path, h.Version, Version)
			}
			s.ID, s.Cwd, s.Parent = h.ID, h.Cwd, h.ParentSession
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		e.raw = []byte(line)
		s.entries = append(s.entries, e)
		s.leaf = e.ID
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if s.ID == "" {
		return nil, fmt.Errorf("%s: empty session file", path)
	}
	return s, nil
}

// Fork copies the active branch of s into a new session in dir whose header names s as its parent. The session name is not copied.
func (s *Session) Fork(dir, cwd string) (*Session, error) { return s.copyBranch(dir, cwd, s.leaf) }

// rawLine is a line written as is.
type rawLine string

func lineBytes(l any) ([]byte, error) {
	if r, ok := l.(rawLine); ok {
		return []byte(r), nil
	}
	return json.Marshal(l)
}

func endsWithNewline(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return true
	}
	b := make([]byte, 1)
	_, err = f.ReadAt(b, fi.Size()-1)
	return err != nil || b[0] == '\n'
}

func (s *Session) write(lines ...any) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	pre := !endsWithNewline(s.Path) // a hand-edited Pi file may lack its final newline; never glue our line onto its last one
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if pre {
		lines = append([]any{rawLine("")}, lines...)
	}
	for _, l := range lines {
		b, err := lineBytes(l)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

func (s *Session) appendEntry(typ string, fill func(*Entry)) error {
	var lines []any
	if !s.started {
		lines = append(lines, header{Type: "session", Version: Version, ID: s.ID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Cwd: s.Cwd, ParentSession: s.Parent})
	}
	e := Entry{Type: typ, ID: s.freshID(), Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	if s.leaf != "" {
		p := s.leaf
		e.ParentID = &p
	}
	fill(&e)
	if e.ParentID == nil && s.leaf != "" {
		p := s.leaf
		e.ParentID = &p
	}
	lines = append(lines, e)
	if err := s.write(lines...); err != nil {
		return err
	}
	s.started = true
	s.entries = append(s.entries, e)
	s.leaf = e.ID
	if s.OnAppend != nil {
		s.OnAppend(e)
	}
	return nil
}

func (s *Session) freshID() string {
	for {
		id := newID(4)
		clash := false
		for _, e := range s.entries {
			if e.ID == id {
				clash = true
				break
			}
		}
		if !clash {
			return id
		}
	}
}

// SetName records a display name (a session_info entry). The latest name wins.
func (s *Session) SetName(name string) error {
	return s.appendEntry("session_info", func(e *Entry) { e.Name = name })
}

// Name is the latest display name, or "".
func (s *Session) Name() string {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].Type == "session_info" {
			return s.entries[i].Name
		}
	}
	return ""
}

// RecordModel notes a model switch so a resumed session can show what it last used.
func (s *Session) RecordModel(provider, model string) error {
	return s.appendEntry("model_change", func(e *Entry) { e.Provider, e.ModelID = provider, model })
}

// Model is the last recorded model id, or "".
func (s *Session) Model() string {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].Type == "model_change" {
			return s.entries[i].ModelID
		}
	}
	return ""
}

// path is the active branch, root first: from the leaf up through parentId.
func (s *Session) path() []Entry {
	byID := make(map[string]Entry, len(s.entries))
	for _, e := range s.entries {
		byID[e.ID] = e
	}
	var rev []Entry
	for id := s.leaf; id != ""; {
		e, ok := byID[id]
		if !ok || len(rev) > len(s.entries) {
			break
		}
		rev = append(rev, e)
		if e.ParentID == nil {
			break
		}
		id = *e.ParentID
	}
	out := make([]Entry, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}

// Count is the number of message entries in the file.
func (s *Session) Count() int {
	n := 0
	for _, e := range s.entries {
		if e.Type == "message" {
			n++
		}
	}
	return n
}
