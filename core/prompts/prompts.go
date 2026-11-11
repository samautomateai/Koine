// Package prompts loads slash-command prompt templates: Pi prompts, opencode
// commands and Claude commands (markdown with $ARGUMENTS / $1 placeholders).
package prompts

import (
	"fmt"
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"koine/core/frontmatter"
)

type Prompt struct {
	Name, Description, Body, Path string
}

func Discover(cwd, home string) []Prompt {
	dirs := []string{
		filepath.Join(cwd, ".koine", "prompts"), filepath.Join(cwd, ".pi", "prompts"),
		filepath.Join(cwd, ".opencode", "command"), filepath.Join(cwd, ".opencode", "commands"),
		filepath.Join(cwd, ".claude", "commands"),
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(agentdir.Of(home), "prompts"), filepath.Join(home, ".pi", "agent", "prompts"),
			filepath.Join(home, ".config", "opencode", "command"), filepath.Join(home, ".config", "opencode", "commands"),
			filepath.Join(home, ".claude", "commands"))
	}
	seen := map[string]bool{}
	var out []Prompt
	for _, d := range dirs {
		out = append(out, loadDir(d, seen)...)
	}
	return out
}

// LoadPaths loads the prompt templates named on the command line: .md files, or directories of them. Each path that cannot be used is
// reported. Templates found by Discover with the same name lose to these (see Merge).
func LoadPaths(paths []string) (out []Prompt, problems []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		st, err := os.Stat(p)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("prompt template %s: %v", p, err))
		case st.IsDir():
			found := loadDir(p, seen)
			if len(found) == 0 {
				problems = append(problems, "prompt template "+p+": no .md files found")
			}
			out = append(out, found...)
		default:
			if pr, ok := loadFile(p, seen); ok {
				out = append(out, pr)
			} else {
				problems = append(problems, "prompt template "+p+": not a readable .md file or the name is already used")
			}
		}
	}
	return out, problems
}

// Merge returns first followed by the templates of rest whose names first does not already use.
func Merge(first, rest []Prompt) []Prompt {
	seen := map[string]bool{}
	for _, p := range first {
		seen[p.Name] = true
	}
	out := append([]Prompt(nil), first...)
	for _, p := range rest {
		if !seen[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

func loadDir(d string, seen map[string]bool) []Prompt {
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	var out []Prompt
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if p, ok := loadFile(filepath.Join(d, e.Name()), seen); ok {
			out = append(out, p)
		}
	}
	return out
}

func loadFile(p string, seen map[string]bool) (Prompt, bool) {
	if !strings.HasSuffix(p, ".md") {
		return Prompt{}, false
	}
	name := strings.TrimSuffix(filepath.Base(p), ".md")
	if seen[name] {
		return Prompt{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Prompt{}, false
	}
	meta, body := frontmatter.Parse(string(data))
	seen[name] = true
	return Prompt{Name: name, Description: meta["description"], Body: strings.TrimSpace(body), Path: p}, true
}

var posRe = regexp.MustCompile(`\$(\d)`)

// Expand substitutes $ARGUMENTS, $@ and $1..$9.
func (p Prompt) Expand(args string) string {
	s := strings.ReplaceAll(p.Body, "$ARGUMENTS", args)
	s = strings.ReplaceAll(s, "$@", args)
	f := strings.Fields(args)
	return posRe.ReplaceAllStringFunc(s, func(m string) string {
		i := int(m[1] - '1')
		if i >= 0 && i < len(f) {
			return f[i]
		}
		return ""
	})
}
