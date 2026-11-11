// Package agents loads sub-agent definitions from opencode (.opencode/agent),
// Claude (.claude/agents) and koine/Pi directories.
package agents

import (
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"strings"

	"koine/core/frontmatter"
	"koine/core/tools"
)

type Def struct {
	Name, Description, Model, Prompt, Path string
	Allow, Deny                            []string
}

func Discover(cwd, home string) []Def {
	dirs := []string{
		filepath.Join(cwd, ".koine", "agents"), filepath.Join(cwd, ".pi", "agents"),
		filepath.Join(cwd, ".opencode", "agent"), filepath.Join(cwd, ".opencode", "agents"),
		filepath.Join(cwd, ".claude", "agents"),
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(agentdir.Of(home), "agents"), filepath.Join(home, ".pi", "agent", "agents"),
			filepath.Join(home, ".config", "opencode", "agent"), filepath.Join(home, ".config", "opencode", "agents"),
			filepath.Join(home, ".claude", "agents"))
	}
	seen := map[string]bool{}
	var out []Def
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			p := filepath.Join(d, e.Name())
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			meta, body := frontmatter.Parse(string(data))
			if meta["mode"] == "primary" { // opencode primary agents are not delegates
				continue
			}
			name := meta["name"]
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".md")
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			def := Def{Name: name, Description: meta["description"], Prompt: strings.TrimSpace(body), Path: p}
			if m := meta["model"]; m != "" && m != "inherit" {
				if i := strings.LastIndex(m, "/"); i >= 0 {
					m = m[i+1:]
				}
				def.Model = m
			}
			def.Allow, def.Deny = parseTools(meta["tools"])
			out = append(out, def)
		}
	}
	return out
}

// parseTools understands Claude ("Read, Grep" / "[a, b]") and opencode
// ("write: false" maps) forms.
func parseTools(v string) (allow, deny []string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	if strings.Contains(v, ":") {
		for _, ln := range strings.Split(v, "\n") {
			k, val, ok := strings.Cut(ln, ":")
			if !ok {
				continue
			}
			k = tools.Canon(strings.Trim(k, " \"'"))
			switch strings.TrimSpace(val) {
			case "false":
				deny = append(deny, k)
			case "true":
				allow = append(allow, k)
			}
		}
		if len(deny) > 0 && len(allow) == 0 {
			return nil, deny
		}
		return
	}
	v = strings.Trim(v, "[]")
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' }) {
		f = strings.Trim(strings.TrimPrefix(strings.TrimSpace(f), "- "), " \"'")
		if c := tools.Canon(f); c != "" {
			allow = append(allow, c)
		}
	}
	return
}

func Find(ds []Def, name string) (Def, bool) {
	for _, d := range ds {
		if d.Name == name {
			return d, true
		}
	}
	return Def{}, false
}
