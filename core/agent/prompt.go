package agent

import (
	"fmt"
	"strings"
	"time"

	"koine/core/skills"
)

// basePrompt assembles the default system prompt as XML-style sections, the way Pi does: preamble, <tools> (one line per tool that has a
// promptSnippet), <rules> (the promptGuidelines of the active tools plus general rules), <project_context>, the skills, and <environment>.
// A custom Base replaces the preamble and the <tools> and <rules> sections, as a SYSTEM.md does in Pi.
func (a *Agent) basePrompt() string {
	var b strings.Builder
	if a.Base != "" {
		b.WriteString(a.Base)
	} else {
		b.WriteString(defaultBase)
		b.WriteString(a.toolSections())
	}
	if ctx := a.projectContext(); ctx != "" {
		b.WriteString("\n\n" + ctx)
	}
	if s := skills.PromptSection(a.Skills); s != "" {
		b.WriteString("\n\n" + s)
	}
	fmt.Fprintf(&b, "\n\n<environment>\nWorking directory: %s\nDate: %s\n</environment>\n", a.Cwd, time.Now().Format("2006-01-02"))
	return b.String()
}

// toolSections renders <tools> and <rules> from the active tools.
func (a *Agent) toolSections() string {
	var lines, rules []string
	active := map[string]bool{}
	if a.Tools != nil {
		lines, rules = a.Tools.PromptParts()
		for _, n := range a.Tools.Names() {
			active[n] = true
		}
	}
	var general []string
	search := active["grep"] || active["find"] || active["ls"]
	switch {
	case active["bash"] && !search:
		general = append(general, "- Use bash for file operations like ls, rg, find")
	case active["bash"] && search:
		general = append(general, "- Prefer grep, find and ls over bash for file exploration (faster, respects .gitignore)")
	}
	rules = append(general, rules...)
	rules = append(rules, "- Be concise in your responses", "- Show file paths clearly when working with files")
	var b strings.Builder
	if len(lines) > 0 {
		b.WriteString("\n\n<tools>\nAvailable tools:\n" + strings.Join(lines, "\n") + "\n</tools>")
	}
	b.WriteString("\n\n<rules>\n" + strings.Join(rules, "\n") + "\n</rules>")
	return b.String()
}

// projectContext wraps the AGENTS.md style files in <project_context>. Other entries (an appended system prompt) follow it unwrapped.
func (a *Agent) projectContext() string {
	var files, other []string
	for _, c := range a.ContextFiles {
		if strings.HasPrefix(c, "<context") {
			files = append(files, c)
		} else {
			other = append(other, c)
		}
	}
	var parts []string
	if len(files) > 0 {
		parts = append(parts, "<project_context>\n"+strings.Join(files, "\n")+"\n</project_context>")
	}
	return strings.Join(append(parts, other...), "\n\n")
}
