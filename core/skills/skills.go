// Package skills discovers SKILL.md skills using the conventions of Pi,
// opencode, Claude and the shared ~/.agents layout, so existing skills work
// unchanged.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"koine/core/frontmatter"
	"koine/core/tools"
)

type Skill struct {
	Name        string
	Description string
	Location    string // absolute path of the SKILL.md
	Dir         string
	Body        string
}

type root struct {
	dir    string
	rootMD bool // Pi also accepts loose .md files directly in the root
}

// ancestors lists cwd and its parents (closest first), stopping at home or the
// filesystem root, so .agents/skills in a monorepo root applies to subfolders.
func ancestors(cwd, home string) []string {
	var out []string
	for d := cwd; ; d = filepath.Dir(d) {
		out = append(out, d)
		if d == home || filepath.Dir(d) == d {
			break
		}
	}
	return out
}

func roots(cwd, home string) []root {
	r := []root{
		{filepath.Join(cwd, ".koine", "skills"), true},
		{filepath.Join(cwd, ".pi", "skills"), true},
		{filepath.Join(cwd, ".opencode", "skill"), false},
		{filepath.Join(cwd, ".opencode", "skills"), false},
		{filepath.Join(cwd, ".claude", "skills"), false},
	}
	for _, d := range ancestors(cwd, home) {
		r = append(r, root{filepath.Join(d, ".agents", "skills"), false})
	}
	if home != "" {
		r = append(r,
			root{filepath.Join(home, ".koine", "skills"), true},
			root{filepath.Join(home, ".pi", "agent", "skills"), true},
			root{filepath.Join(home, ".agents", "skills"), false},
			root{filepath.Join(home, ".config", "opencode", "skill"), false},
			root{filepath.Join(home, ".config", "opencode", "skills"), false},
			root{filepath.Join(home, ".claude", "skills"), false},
		)
	}
	return r
}

// Skipped explains a skill that was found but refused.
type Skipped struct{ Path, Reason string }

// Audit returns a reason if text contains characters that hide instructions
// from a human reviewer but are still read by the model: bidirectional
// overrides, zero-width characters, variation selectors and Unicode "tag"
// characters. A BOM at the very start of the file is allowed.
func Audit(text string) string {
	for i, r := range text {
		switch {
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			return fmt.Sprintf("bidirectional control character U+%04X", r)
		case r >= 0x200B && r <= 0x200F, r == 0x2060, r == 0xFEFF && i > 0:
			return fmt.Sprintf("zero-width character U+%04X", r)
		case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
			return fmt.Sprintf("variation selector U+%04X", r)
		case r >= 0xE0000 && r <= 0xE007F:
			return fmt.Sprintf("tag character U+%04X", r)
		}
	}
	return ""
}

// Discover returns skills, earlier (project-level) roots winning name clashes.
func Discover(cwd, home string) []Skill {
	ss, _ := DiscoverReport(cwd, home)
	return ss
}

// DiscoverReport is Discover plus the skills that failed the hidden-text audit.
func DiscoverReport(cwd, home string) ([]Skill, []Skipped) {
	seen := map[string]bool{}
	var out []Skill
	var skipped []Skipped
	add := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if why := Audit(string(data)); why != "" {
			skipped = append(skipped, Skipped{path, why})
			return
		}
		meta, body := frontmatter.Parse(string(data))
		name := meta["name"]
		if name == "" {
			if strings.EqualFold(filepath.Base(path), "SKILL.md") {
				name = filepath.Base(filepath.Dir(path))
			} else {
				name = strings.TrimSuffix(filepath.Base(path), ".md")
			}
		}
		if meta["description"] == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, Skill{Name: name, Description: meta["description"], Location: path, Dir: filepath.Dir(path), Body: strings.TrimSpace(body)})
	}
	for _, r := range roots(cwd, home) {
		if st, err := os.Stat(r.dir); err != nil || !st.IsDir() {
			continue
		}
		base := strings.Count(r.dir, string(filepath.Separator))
		_ = filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || strings.Count(p, string(filepath.Separator))-base > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			switch {
			case d.Name() == "SKILL.md":
				add(p)
			case r.rootMD && filepath.Dir(p) == r.dir && strings.HasSuffix(d.Name(), ".md"):
				add(p)
			}
			return nil
		})
	}
	return out, skipped
}

// PromptSection renders the available-skills block for the system prompt.
func PromptSection(ss []Skill) string {
	if len(ss) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The following skills provide specialised instructions. When a task matches a skill's description, load it with the `skill` tool (or read its file) before proceeding.\n<available_skills>\n")
	for _, s := range ss {
		fmt.Fprintf(&b, "  <skill>\n    <name>%s</name>\n    <description>%s</description>\n    <location>%s</location>\n  </skill>\n", s.Name, s.Description, s.Location)
	}
	b.WriteString("</available_skills>")
	return b.String()
}

func Find(ss []Skill, name string) (Skill, bool) {
	for _, s := range ss {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

func Render(s Skill) string {
	return fmt.Sprintf("<skill name=%q location=%q>\nReferences are relative to %s.\n\n%s\n</skill>", s.Name, s.Location, s.Dir, s.Body)
}

// Tool lets the model load a skill by name (opencode-style).
func Tool(ss []Skill) tools.Tool {
	return &tools.Func{N: "skill", D: "Load a skill's full instructions by name.",
		S: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Skill name"}},"required":["name"]}`),
		F: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Name string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			s, ok := Find(ss, p.Name)
			if !ok {
				return "", fmt.Errorf("unknown skill %q", p.Name)
			}
			return Render(s), nil
		}}
}
