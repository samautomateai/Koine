package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Suggestion is one completion candidate.
type Suggestion struct {
	Value string // text that replaces the token (a path ends in "/" for a directory)
	Label string
	Desc  string
}

// Completion is a set of candidates for the token at [Start,End) of the editor's current line.
type Completion struct {
	Start, End int
	Items      []Suggestion
	Sel        int
	Kind       string // "command", "file" or "path"
}

// Command describes a slash command for the popup.
type Command struct{ Name, Desc string }

// BuiltinCommands are Koine's slash commands, as /help lists them.
var BuiltinCommands = []Command{
	{"help", "list commands"}, {"new", "start a new session"}, {"session", "show the session and usage"}, {"name", "name the session"},
	{"resume", "resume a session"}, {"fork", "fork the session"}, {"clone", "copy this branch to a new session"},
	{"tree", "navigate the session tree"}, {"label", "label an entry"}, {"export", "export the session as HTML"},
	{"model", "choose the model"}, {"thinking", "set the thinking level"}, {"compact", "compact the context"},
	{"settings", "change settings"}, {"hotkeys", "show keyboard shortcuts"}, {"copy", "copy the last answer"},
	{"tools", "list tools"}, {"skills", "list skills"}, {"agents", "list agents"}, {"commands", "list commands"},
	{"trust", "trust this folder"}, {"reload", "reload settings, themes and extensions"}, {"compat", "show compatibility notes"},
	{"exit", "quit"},
}

// Sources feed the completer.
type Sources struct {
	Commands []Command
	Cwd      string
	Files    *FileIndex
	// Args suggests arguments for the command typed so far (an extension's getArgumentCompletions); nil = none.
	Args func(command, prefix string) []Suggestion
}

// Complete works out what to offer for the line up to col (in runes). row is the editor row; slash commands only complete on the first.
// forced is Tab pressed with no popup open, which also completes plain paths.
func Complete(line []rune, row, col int, src Sources, forced bool) *Completion {
	col = min(col, len(line))
	start := col
	for start > 0 && line[start-1] != ' ' && line[start-1] != '\t' {
		start--
	}
	tok := string(line[start:col])
	if c := completeArgs(line, row, col, src); c != nil {
		return c
	}
	switch {
	case row == 0 && start == 0 && strings.HasPrefix(tok, "/") && !strings.Contains(tok[1:], "/"):
		return completeCommand(tok[1:], src.Commands, start, col)
	case strings.HasPrefix(tok, "@"):
		return completeFile(strings.TrimPrefix(tok[1:], `"`), src, start, col)
	case forced && (strings.ContainsAny(tok, "/") || strings.HasPrefix(tok, ".") || strings.HasPrefix(tok, "~")):
		return completePath(tok, src.Cwd, start, col)
	}
	return nil
}

func completeCommand(q string, cmds []Command, start, end int) *Completion {
	type hit struct {
		c     Command
		score int
	}
	var hits []hit
	for _, c := range cmds {
		if s, ok := FuzzyScore(q, c.Name); ok {
			hits = append(hits, hit{c, s})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	c := &Completion{Start: start, End: end, Kind: "command"}
	for _, h := range hits {
		c.Items = append(c.Items, Suggestion{Value: "/" + h.c.Name + " ", Label: "/" + h.c.Name, Desc: h.c.Desc})
	}
	return c
}

func quoteRef(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `@"` + p + `"`
	}
	return "@" + p
}

func completeFile(q string, src Sources, start, end int) *Completion {
	if src.Files == nil {
		return nil
	}
	paths := src.Files.List(src.Cwd)
	var items []Suggestion
	if q == "" {
		for _, p := range paths {
			if !strings.Contains(strings.TrimSuffix(p, "/"), "/") {
				items = append(items, Suggestion{Value: quoteRef(p), Label: p})
			}
		}
	} else {
		type hit struct {
			p     string
			score int
		}
		var hits []hit
		for _, p := range paths {
			s1, ok1 := FuzzyScore(q, p)
			s2, ok2 := FuzzyScore(q, filepath.Base(strings.TrimSuffix(p, "/")))
			if !ok1 && !ok2 {
				continue
			}
			s := s1
			if ok2 && s2*2 > s || !ok1 {
				s = s2 * 2
			}
			hits = append(hits, hit{p, s})
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
		for _, h := range hits[:min(len(hits), 50)] {
			items = append(items, Suggestion{Value: quoteRef(h.p), Label: h.p})
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &Completion{Start: start, End: end, Items: items, Kind: "file"}
}

// completePath completes tok as a path prefix: the entries of its directory whose names start with the rest.
func completePath(tok, cwd string, start, end int) *Completion {
	dirPart, base := "", tok
	if i := strings.LastIndex(tok, "/"); i >= 0 {
		dirPart, base = tok[:i+1], tok[i+1:]
	}
	dir := dirPart
	switch {
	case strings.HasPrefix(dir, "~"):
		h, _ := os.UserHomeDir()
		dir = filepath.Join(h, dir[1:])
	case !filepath.IsAbs(dir):
		dir = filepath.Join(cwd, dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	c := &Completion{Start: start, End: end, Kind: "path"}
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(strings.ToLower(n), strings.ToLower(base)) || (strings.HasPrefix(n, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		if e.IsDir() {
			n += "/"
		}
		c.Items = append(c.Items, Suggestion{Value: dirPart + n, Label: n})
	}
	sort.SliceStable(c.Items, func(i, j int) bool {
		di, dj := strings.HasSuffix(c.Items[i].Value, "/"), strings.HasSuffix(c.Items[j].Value, "/")
		return di && !dj
	})
	if len(c.Items) == 0 {
		return nil
	}
	return c
}

// Apply replaces the completed token in the editor with the selected suggestion.
func (c *Completion) Apply(e *Editor) {
	if c == nil || c.Sel >= len(c.Items) || e.Row >= len(e.lines) {
		return
	}
	line := e.lines[e.Row]
	if c.End > len(line) || c.Start > c.End {
		return
	}
	v := c.Items[c.Sel].Value
	if !strings.HasSuffix(v, "/") && !strings.HasSuffix(v, " ") && c.Kind == "file" {
		v += " "
	}
	e.begin("")
	e.lines[e.Row] = append(append(append([]rune{}, line[:c.Start]...), []rune(v)...), line[c.End:]...)
	e.Col = c.Start + len([]rune(v))
}

// Move changes the selection by d, wrapping around.
func (c *Completion) Move(d int) { c.Sel = ((c.Sel+d)%len(c.Items) + len(c.Items)) % len(c.Items) }

// View draws up to max candidates around the selection.
func (c *Completion) View(width, maxVisible int, th *Theme) []string {
	maxVisible = max(3, min(20, maxVisible))
	first := max(0, min(c.Sel-maxVisible/2, len(c.Items)-maxVisible))
	last := min(len(c.Items), first+maxVisible)
	lw := 0
	for _, it := range c.Items[first:last] {
		lw = max(lw, Width(it.Label))
	}
	var out []string
	for i := first; i < last; i++ {
		it := c.Items[i]
		line := " " + Pad(it.Label, lw)
		if it.Desc != "" {
			line += "  " + th.Fg("muted", it.Desc)
		}
		line = Truncate(line, width, "…")
		if i == c.Sel {
			line = th.Bg("selectedBg", Pad(line, width))
		}
		out = append(out, line)
	}
	if len(c.Items) > maxVisible {
		out = append(out, th.Fg("dim", Truncate(" ("+strconv.Itoa(c.Sel+1)+"/"+strconv.Itoa(len(c.Items))+")", width, "")))
	}
	return out
}

// FileIndex lists the files under a directory for @ completion, cached for a few seconds. Directories end in "/".
type FileIndex struct {
	mu    sync.Mutex
	cwd   string
	at    time.Time
	paths []string
	Limit int
}

// List walks cwd (skipping .git, node_modules and plain names from the top-level .gitignore) and returns relative paths.
func (f *FileIndex) List(cwd string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cwd == cwd && time.Since(f.at) < 5*time.Second {
		return f.paths
	}
	limit := f.Limit
	if limit == 0 {
		limit = 20000
	}
	skip := map[string]bool{".git": true, "node_modules": true}
	if b, err := os.ReadFile(filepath.Join(cwd, ".gitignore")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.Trim(strings.TrimSpace(l), "/")
			if l != "" && !strings.ContainsAny(l, "*?[!#/") {
				skip[l] = true
			}
		}
	}
	var out []string
	_ = filepath.WalkDir(cwd, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == cwd {
			return nil
		}
		if len(out) >= limit {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(cwd, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			out = append(out, rel+"/")
			return nil
		}
		if skip[d.Name()] {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	f.cwd, f.at, f.paths = cwd, time.Now(), out
	return out
}
