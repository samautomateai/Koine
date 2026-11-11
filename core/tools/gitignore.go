package tools

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// An ignoreRule is one line of a .gitignore file.
type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// ignoreSet holds the rules of one .gitignore file, relative to the directory that holds it.
type ignoreSet struct {
	base   string // slash-separated path of that directory relative to the walk root ("" = the root)
	prefix string // for the ignore file of a directory above the root: the root's path relative to that directory
	rules  []ignoreRule
}

// parseIgnore reads gitignore syntax: comments, negation with "!", "dir/" patterns, anchored patterns, "*", "?", "[…]" and "**".
func parseIgnore(path, base string) *ignoreSet {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	set := &ignoreSet{base: base}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		line = strings.TrimPrefix(line, `\`) // an escaped leading "#" or "!"
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimSuffix(line, "/")
		}
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		expr := ignoreRegexp(line)
		if !anchored {
			expr = "(?:.*/)?" + expr
		}
		re, err := regexp.Compile("^" + expr + "$")
		if err != nil {
			continue
		}
		r.re = re
		set.rules = append(set.rules, r)
	}
	return set
}

// ignoreRegexp translates the glob part of a gitignore pattern to a regular expression.
func ignoreRegexp(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(g[i:], "/**") && i+3 == len(g):
			b.WriteString("/.*")
			i += 2
		case c == '*' && strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(g[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				break
			}
			class := g[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end + 1
		case c == '\\' && i+1 < len(g):
			i++
			b.WriteString(regexp.QuoteMeta(string(g[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// match reports the verdict of this file for rel (slash-separated, relative to the walk root): ignored, and whether any rule decided.
func (s *ignoreSet) match(rel string, isDir bool) (ignored, decided bool) {
	if s.prefix != "" {
		rel = s.prefix + "/" + rel
	}
	if s.base != "" {
		rest, ok := strings.CutPrefix(rel, s.base+"/")
		if !ok {
			return false, false
		}
		rel = rest
	}
	for _, r := range s.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(rel) {
			ignored, decided = !r.negate, true
		}
	}
	return ignored, decided
}

// walker visits a tree in lexical order, skipping .git and whatever the .gitignore files on the way ignore. Ignore files of the directories above
// the root are honoured up to the repository root (the directory that holds .git).
type walker struct {
	root string
	sets []*ignoreSet
}

func newWalker(root string, useIgnore bool) *walker {
	w := &walker{root: root}
	if !useIgnore {
		return w
	}
	// Ancestors: the directories from the repository root down to root's parent.
	var chain []string
	for dir := filepath.Dir(root); dir != root && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		chain = append([]string{dir}, chain...)
		if exists(filepath.Join(dir, ".git")) {
			break
		}
	}
	if len(chain) > 0 && !exists(filepath.Join(chain[0], ".git")) {
		chain = nil // not inside a repository: parent ignore files do not apply
	}
	for _, d := range chain {
		if s := parseIgnore(filepath.Join(d, ".gitignore"), ""); s != nil {
			if up, err := filepath.Rel(d, root); err == nil {
				s.prefix = filepath.ToSlash(up)
			}
			w.sets = append(w.sets, s)
		}
	}
	return w
}

func (w *walker) ignored(rel string, isDir bool) bool {
	ignored := false
	for _, s := range w.sets {
		if i, decided := s.match(rel, isDir); decided {
			ignored = i
		}
	}
	return ignored
}

// walk calls fn for every file and directory below the root (not the root itself). fn returns false to stop the whole walk.
func (w *walker) walk(fn func(rel string, d fs.DirEntry) bool) {
	w.dir("", fn)
}

func (w *walker) dir(rel string, fn func(string, fs.DirEntry) bool) bool {
	abs := filepath.Join(w.root, filepath.FromSlash(rel))
	pushed := false
	if s := parseIgnore(filepath.Join(abs, ".gitignore"), rel); s != nil {
		w.sets = append(w.sets, s)
		pushed = true
	}
	defer func() {
		if pushed {
			w.sets = w.sets[:len(w.sets)-1]
		}
	}()
	ents, err := os.ReadDir(abs)
	if err != nil {
		return true
	}
	for _, e := range ents {
		name := e.Name()
		if name == ".git" {
			continue
		}
		child := name
		if rel != "" {
			child = rel + "/" + name
		}
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(abs, name)); err == nil && st.IsDir() {
				isDir = false // symlinked directories are listed but never followed
			}
		}
		if w.ignored(child, isDir) {
			continue
		}
		if !fn(child, e) {
			return false
		}
		if isDir && !w.dir(child, fn) {
			return false
		}
	}
	return true
}
