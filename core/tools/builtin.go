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
	"sort"
	"strings"
	"time"
)

const maxOut = 30000

func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func flag(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

func clip(s string) string {
	if len(s) <= maxOut {
		return s
	}
	return s[:maxOut/2] + "\n… [output truncated] …\n" + s[len(s)-maxOut/2:]
}

// RegisterBuiltins adds read, write, edit, bash, ls, glob and grep rooted at cwd.
func RegisterBuiltins(r *Registry, cwd string) { RegisterBuiltinsEnv(r, cwd, nil) }

// RegisterBuiltinsEnv is RegisterBuiltins with extra environment for the bash tool. shellEnv is called for every command, so it can report
// the current session and model; the entries are "NAME=value" and come after the process environment.
func RegisterBuiltinsEnv(r *Registry, cwd string, shellEnv func() []string) {
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(cwd, p)
	}

	r.Register(&Func{N: "read", D: "Read a text file or an image (jpg, png, gif, webp, bmp). Text is numbered; use offset/limit for large files.",
		S: schema(map[string]any{"path": str("File path"), "offset": num("1-based first line"), "limit": num("Max lines (default 2000)")}, "path"),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct {
				Path          string
				Offset, Limit int
			}
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			if text, ok, err := readImage(ctx, abs(p.Path)); ok || err != nil { // images become attachments (readimage.go)
				return text, err
			}
			data, err := os.ReadFile(abs(p.Path))
			if err != nil {
				return "", err
			}
			lines := strings.Split(string(data), "\n")
			start := max(p.Offset-1, 0)
			lim := p.Limit
			if lim <= 0 {
				lim = 2000
			}
			if start > len(lines) {
				start = len(lines)
			}
			end := min(start+lim, len(lines))
			var b strings.Builder
			for i := start; i < end; i++ {
				fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
			}
			if end < len(lines) {
				fmt.Fprintf(&b, "… %d more lines (use offset=%d)\n", len(lines)-end, end+1)
			}
			return clip(b.String()), nil
		}})

	r.Register(&Func{N: "write", D: "Create or overwrite a file with the given content.",
		S: schema(map[string]any{"path": str("File path"), "content": str("Full file content")}, "path", "content"),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Path, Content string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			full := abs(p.Path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.Path), nil
		}})

	r.Register(&Func{N: "edit", D: "Replace an exact string in a file. old must match exactly once unless replaceAll is true.",
		S: schema(map[string]any{
			"path": str("File path"), "old": str("Exact text to replace"), "new": str("Replacement text"),
			"replaceAll": flag("Replace every occurrence"),
		}, "path", "old", "new"),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct {
				Path, Old, New string
				ReplaceAll     bool
			}
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			full := abs(p.Path)
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			s := string(data)
			n := strings.Count(s, p.Old)
			switch {
			case p.Old == "":
				return "", errors.New("old must not be empty")
			case n == 0:
				return "", errors.New("old text not found")
			case n > 1 && !p.ReplaceAll:
				return "", fmt.Errorf("old text matches %d places; add context or set replaceAll", n)
			}
			s = strings.ReplaceAll(s, p.Old, p.New)
			if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("edited %s (%d replacement(s))", p.Path, n), nil
		}})

	r.Register(&Func{N: "bash", D: "Run a shell command in the project directory. Default timeout 120s.",
		S: schema(map[string]any{"command": str("Shell command"), "timeout": num("Timeout in seconds")}, "command"),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct {
				Command string
				Timeout int
			}
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			t := time.Duration(p.Timeout) * time.Second
			if t <= 0 {
				t = 120 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, t)
			defer cancel()
			cmd := exec.CommandContext(cctx, "bash", "-c", p.Command)
			cmd.Dir = cwd
			if shellEnv != nil {
				cmd.Env = append(os.Environ(), shellEnv()...)
			}
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			err := cmd.Run()
			out := clip(buf.String())
			if cctx.Err() == context.DeadlineExceeded {
				return out + "\n[timed out]", errors.New("command timed out")
			}
			if err != nil {
				return fmt.Sprintf("%s\n[%v]", out, err), err
			}
			return out, nil
		}})

	r.Register(&Func{N: "ls", D: "List a directory.",
		S: schema(map[string]any{"path": str("Directory (default .)")}),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Path string }
			_ = json.Unmarshal(a, &p)
			if p.Path == "" {
				p.Path = "."
			}
			ents, err := os.ReadDir(abs(p.Path))
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, e := range ents {
				n := e.Name()
				if e.IsDir() {
					n += "/"
				}
				b.WriteString(n + "\n")
			}
			return b.String(), nil
		}})

	r.Register(&Func{N: "glob", D: "Find files by glob pattern (supports **). Skips .git and node_modules.",
		S: schema(map[string]any{"pattern": str("e.g. **/*.go"), "path": str("Root dir (default .)")}, "pattern"),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Pattern, Path string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			re, err := globRegexp(p.Pattern)
			if err != nil {
				return "", err
			}
			root := abs(orDot(p.Path))
			var hits []string
			_ = walk(root, func(rel string, d fs.DirEntry) {
				if d.IsDir() {
					return
				}
				m := rel
				if !strings.Contains(p.Pattern, "/") {
					m = filepath.Base(rel)
				}
				if re.MatchString(m) {
					hits = append(hits, rel)
				}
			})
			sort.Strings(hits)
			if len(hits) > 500 {
				hits = append(hits[:500], "… truncated")
			}
			return strings.Join(hits, "\n"), nil
		}})

	r.Register(&Func{N: "grep", D: "Search file contents with a regular expression.",
		S: schema(map[string]any{"pattern": str("Go regexp"), "path": str("Root dir (default .)"), "glob": str("Only files matching this glob")}, "pattern"),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Pattern, Path, Glob string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return "", err
			}
			var gre *regexp.Regexp
			if p.Glob != "" {
				if gre, err = globRegexp(p.Glob); err != nil {
					return "", err
				}
			}
			root := abs(orDot(p.Path))
			var out []string
			_ = walk(root, func(rel string, d fs.DirEntry) {
				if d.IsDir() || len(out) >= 200 {
					return
				}
				if gre != nil && !gre.MatchString(filepath.Base(rel)) && !gre.MatchString(rel) {
					return
				}
				f, err := os.Open(filepath.Join(root, rel))
				if err != nil {
					return
				}
				defer func() { _ = f.Close() }()
				sc := bufio.NewScanner(f)
				sc.Buffer(make([]byte, 64*1024), 1<<20)
				for i := 1; sc.Scan() && len(out) < 200; i++ {
					line := sc.Text()
					if strings.IndexByte(line, 0) >= 0 {
						return // binary
					}
					if re.MatchString(line) {
						out = append(out, fmt.Sprintf("%s:%d:%s", rel, i, strings.TrimSpace(line)))
					}
				}
			})
			return clip(strings.Join(out, "\n")), nil
		}})
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

func walk(root string, fn func(rel string, d fs.DirEntry)) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		fn(filepath.ToSlash(rel), d)
		return nil
	})
}

func globRegexp(g string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case g[i] == '*':
			b.WriteString("[^/]*")
		case g[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(g[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
