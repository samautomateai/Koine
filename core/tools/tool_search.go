package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultGrepLimit = 100
	defaultFindLimit = 1000
	defaultLsLimit   = 500
)

// notes joins truncation notices the way Pi does: one bracketed sentence list after the output.
func notes(out string, ns []string) string {
	if len(ns) == 0 {
		return out
	}
	return out + "\n\n[" + strings.Join(ns, ". ") + "]"
}

// headLimit applies the byte limit to a result and records the notice.
func headLimit(out string, ns *[]string) string {
	t := TruncateHead(out, 1<<30, DefaultMaxBytes)
	if t.Truncated {
		*ns = append(*ns, FormatSize(DefaultMaxBytes)+" limit reached")
	}
	return t.Content
}

func (o Options) searchRoot(p string) (string, error) {
	root := ResolvePath(orDot(p), o.Cwd, o.Home)
	if _, err := os.Stat(root); err != nil {
		return "", fmt.Errorf("path not found: %s", root)
	}
	return root, nil
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

func (o Options) ls(_ context.Context, a json.RawMessage) (string, error) {
	var p struct {
		Path  string
		Limit int
	}
	_ = json.Unmarshal(a, &p)
	dir := ResolvePath(orDot(p.Path), o.Cwd, o.Home)
	st, err := os.Stat(dir)
	switch {
	case err != nil:
		return "", fmt.Errorf("path not found: %s", dir)
	case !st.IsDir():
		return "", fmt.Errorf("not a directory: %s", dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		n := e.Name()
		if s, err := os.Stat(filepath.Join(dir, n)); err == nil && s.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		li, lj := strings.ToLower(names[i]), strings.ToLower(names[j])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
	limit := p.Limit
	if limit <= 0 {
		limit = defaultLsLimit
	}
	var ns []string
	if len(names) > limit {
		names = names[:limit]
		ns = append(ns, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", limit, limit*2))
	}
	if len(names) == 0 {
		return "(empty directory)", nil
	}
	return notes(headLimit(strings.Join(names, "\n"), &ns), ns), nil
}

func (o Options) find(_ context.Context, a json.RawMessage) (string, error) {
	var p struct {
		Pattern, Path string
		Limit         int
	}
	if err := json.Unmarshal(a, &p); err != nil {
		return "", err
	}
	if p.Pattern == "" {
		return "", errors.New("pattern must not be empty")
	}
	root, err := o.searchRoot(p.Path)
	if err != nil {
		return "", err
	}
	// A pattern without "/" matches names at any depth; one with "/" matches the path below the root (a leading "/" anchors it).
	re, err := globMatcher(p.Pattern)
	if err != nil {
		return "", err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultFindLimit
	}
	var hits []string
	limited := false
	newWalker(root, true).walk(func(rel string, d fs.DirEntry) bool {
		if !re.MatchString(rel) {
			return true
		}
		if len(hits) >= limit {
			limited = true
			return false
		}
		if d.IsDir() {
			rel += "/"
		}
		hits = append(hits, rel)
		return true
	})
	if len(hits) == 0 {
		return "No files found matching pattern", nil
	}
	sort.Strings(hits)
	var ns []string
	if limited {
		ns = append(ns, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", limit, limit*2))
	}
	return notes(headLimit(strings.Join(hits, "\n"), &ns), ns), nil
}
