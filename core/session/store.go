package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirFor is where sessions for cwd live under root: root/--<path with separators as dashes>--, as Pi lays them out.
func DirFor(root, cwd string) string {
	p := strings.TrimLeft(cwd, `/\`)
	p = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(p)
	return filepath.Join(root, "--"+p+"--")
}

// Info summarises a session file for listings.
type Info struct {
	Path     string
	ID       string
	Name     string
	Preview  string // first user message
	Messages int
	Modified time.Time
}

// List returns the sessions in dir, most recently modified first. Files that are not sessions are skipped.
func List(dir string) []Info {
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	var out []Info
	for _, f := range files {
		s, err := openFile(f, false) // listing never rewrites files
		if err != nil {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		preview := ""
		if msgs := s.Messages(); len(msgs) > 0 && len(msgs[0].Content) > 0 {
			preview = strings.Join(strings.Fields(msgs[0].Content[0].Text), " ")
			if len(preview) > 70 {
				preview = preview[:70] + "…"
			}
		}
		out = append(out, Info{Path: f, ID: s.ID, Name: s.Name(), Preview: preview, Messages: s.Count(), Modified: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// Latest opens the most recently modified session in dir.
func Latest(dir string) (*Session, error) {
	l := List(dir)
	if len(l) == 0 {
		return nil, fmt.Errorf("no sessions in %s", dir)
	}
	return Open(l[0].Path)
}

// Resolve finds a session by file path, by id (or unique id prefix), or by exact name, looking in dir.
func Resolve(dir, ref string) (*Session, error) {
	if fi, err := os.Stat(ref); err == nil && !fi.IsDir() {
		return Open(ref)
	}
	var hits []Info
	for _, i := range List(dir) {
		if i.ID == ref || i.Name == ref || (len(ref) >= 4 && strings.HasPrefix(i.ID, ref)) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("no session matches %q in %s", ref, dir)
	case 1:
		return Open(hits[0].Path)
	}
	return nil, fmt.Errorf("%q matches %d sessions; use more of the id or the file path", ref, len(hits))
}
