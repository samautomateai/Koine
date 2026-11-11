package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type grepParams struct {
	Pattern, Path, Glob string
	IgnoreCase, Literal bool
	Context, Limit      int
}

// A hit is one matching line: the file (absolute) and its 1-based line number.
type hit struct {
	file string
	line int
}

// ripgrep is the executable to search with: Options.Ripgrep, "" to look for rg on the PATH, or "-" for the built-in Go search.
func (o Options) ripgrep() string {
	switch o.Ripgrep {
	case "-":
		return ""
	case "":
		p, _ := exec.LookPath("rg")
		return p
	}
	return o.Ripgrep
}

func (o Options) grep(ctx context.Context, a json.RawMessage) (string, error) {
	var p grepParams
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
	if p.Limit <= 0 {
		p.Limit = defaultGrepLimit
	}
	p.Context = max(p.Context, 0)
	var hits []hit
	if rg := o.ripgrep(); rg != "" {
		hits, err = rgSearch(ctx, rg, p, root)
	} else {
		hits, err = goSearch(ctx, p, root)
	}
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "No matches found", nil
	}
	out, truncatedLines := formatHits(hits, root, p.Context)
	var ns []string
	if len(hits) >= p.Limit {
		ns = append(ns, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", p.Limit, p.Limit*2))
	}
	out = headLimit(out, &ns)
	if truncatedLines {
		ns = append(ns, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLen))
	}
	return notes(out, ns), nil
}

// rgSearch asks ripgrep for the matching lines (it applies .gitignore, skips binary files and includes dotfiles).
func rgSearch(ctx context.Context, rg string, p grepParams, root string) ([]hit, error) {
	args := []string{"--json", "--line-number", "--color=never", "--hidden", "--no-require-git", "--sort", "path", "-g", "!.git"}
	if p.IgnoreCase {
		args = append(args, "-i")
	}
	if p.Literal {
		args = append(args, "-F")
	}
	if p.Glob != "" {
		args = append(args, "-g", p.Glob)
	}
	args = append(args, "-e", p.Pattern, "--", root)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, rg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var hits []hit
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() && len(hits) < p.Limit {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path       struct{ Text string } `json:"path"`
				LineNumber int                   `json:"line_number"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "match" && ev.Data.Path.Text != "" {
			hits = append(hits, hit{ev.Data.Path.Text, ev.Data.LineNumber})
		}
	}
	cancel() // enough matches, or the output ended: stop rg
	_ = cmd.Wait()
	if len(hits) == 0 && ctx.Err() == nil && stderr.Len() > 0 {
		return nil, fmt.Errorf("ripgrep: %s", strings.TrimSpace(stderr.String()))
	}
	return hits, ctx.Err()
}

// goSearch is the fallback without ripgrep: same options, regular expressions in Go (RE2) syntax.
func goSearch(ctx context.Context, p grepParams, root string) ([]hit, error) {
	expr := p.Pattern
	if p.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	if p.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	var glob *regexp.Regexp
	if p.Glob != "" {
		if glob, err = globMatcher(p.Glob); err != nil {
			return nil, err
		}
	}
	var hits []hit
	scan := func(file, rel string) bool {
		if glob != nil && !glob.MatchString(rel) {
			return true
		}
		return scanFile(file, re, p.Limit, &hits)
	}
	st, _ := os.Stat(root)
	if !st.IsDir() {
		scan(root, filepath.Base(root))
		return hits, nil
	}
	newWalker(root, true).walk(func(rel string, d fs.DirEntry) bool {
		if ctx.Err() != nil {
			return false
		}
		if d.IsDir() {
			return true
		}
		return len(hits) >= p.Limit || scan(filepath.Join(root, filepath.FromSlash(rel)), rel)
	})
	return hits, ctx.Err()
}

// scanFile appends the matching lines of one text file and reports whether to go on (false once the limit is reached).
func scanFile(file string, re *regexp.Regexp, limit int, hits *[]hit) bool {
	f, err := os.Open(file)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Bytes()
		if n == 1 && bytes.IndexByte(line[:min(len(line), 8000)], 0) >= 0 {
			return true // binary
		}
		if re.Match(line) {
			*hits = append(*hits, hit{file, n})
			if len(*hits) >= limit {
				return false
			}
		}
	}
	return true
}

// globMatcher compiles a file filter. Like ripgrep's -g, a glob without "/" matches names at any depth and one with "/" is relative to the root.
func globMatcher(g string) (*regexp.Regexp, error) {
	alts := expandBraces(strings.TrimPrefix(g, "./"))
	for i, a := range alts {
		if !strings.Contains(a, "/") {
			a = "**/" + a
		}
		alts[i] = ignoreRegexp(strings.TrimPrefix(a, "/"))
	}
	return regexp.Compile("^(?:" + strings.Join(alts, "|") + ")$")
}

// expandBraces expands one level of {a,b} alternatives, which ripgrep and fd globs allow.
func expandBraces(g string) []string {
	open := strings.IndexByte(g, '{')
	if open < 0 {
		return []string{g}
	}
	closeAt := strings.IndexByte(g[open:], '}')
	if closeAt < 0 {
		return []string{g}
	}
	closeAt += open
	var out []string
	for _, alt := range strings.Split(g[open+1:closeAt], ",") {
		out = append(out, expandBraces(g[:open]+alt+g[closeAt+1:])...)
	}
	return out
}

// formatHits prints each hit as "path:line: text" with context lines as "path-line- text", reading the files for the text. Context shared by
// neighbouring hits is printed once.
func formatHits(hits []hit, root string, ctxLines int) (string, bool) {
	var b strings.Builder
	cutLines := false
	var cacheFile string
	var cache []string
	printed := map[int]bool{}
	for _, h := range hits {
		if h.file != cacheFile {
			cacheFile, cache, printed = h.file, readLines(h.file), map[int]bool{}
		}
		rel := h.file
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			if r, err := filepath.Rel(root, h.file); err == nil {
				rel = r
			}
		} else {
			rel = filepath.Base(h.file)
		}
		rel = filepath.ToSlash(rel)
		for n := max(h.line-ctxLines, 1); n <= min(h.line+ctxLines, len(cache)); n++ {
			if printed[n] {
				continue
			}
			printed[n] = true
			text, cut := TruncateLine(strings.TrimRight(cache[n-1], "\r"), GrepMaxLineLen)
			cutLines = cutLines || cut
			sep := "-"
			if n == h.line {
				sep = ":"
			}
			fmt.Fprintf(&b, "%s%s%d%s %s\n", rel, sep, n, sep, text)
		}
	}
	return strings.TrimSuffix(b.String(), "\n"), cutLines
}

func readLines(file string) []string {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}
