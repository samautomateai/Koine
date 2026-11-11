// Package prompts loads slash-command prompt templates: Pi prompts, opencode
// commands and Claude commands (markdown with $ARGUMENTS / $1 placeholders).
package prompts

import (
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
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			if seen[name] {
				continue
			}
			p := filepath.Join(d, e.Name())
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			meta, body := frontmatter.Parse(string(data))
			seen[name] = true
			out = append(out, Prompt{Name: name, Description: meta["description"], Body: strings.TrimSpace(body), Path: p})
		}
	}
	return out
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
