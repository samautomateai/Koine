package agent

import (
	"strings"
	"testing"

	"koine/core/llm"
	"koine/core/skills"
	"koine/core/tools"
)

func promptOf(t *testing.T, a *Agent) string {
	t.Helper()
	s := &script{}
	a.Provider = s
	if _, err := collect(a, "hi"); err != nil {
		t.Fatal(err)
	}
	return s.reqs[0].System
}

func TestSystemPromptHasToolsRulesAndEnvironmentSections(t *testing.T) {
	a := newAgent(nil)
	a.Tools = tools.NewRegistry()
	tools.RegisterBuiltins(a.Tools, "/w")
	a.Tools.Register(&tools.Func{N: "zap", Snippet: "Zaps things", Guidelines: []string{"Use zap for zapping."}})
	a.ContextFiles = []string{"<context file=\"/w/AGENTS.md\">\nRULES\n</context>", "APPENDED"}
	a.Skills = []skills.Skill{{Name: "sk", Description: "ds", Location: "/l"}}
	sys := promptOf(t, a)
	for _, want := range []string{
		"<tools>\nAvailable tools:\n- read: Read file contents\n- bash: Execute bash commands (ls, grep, find, etc.)\n",
		"- grep: Search file contents for patterns (respects .gitignore)\n- find: Find files by glob pattern (respects .gitignore)\n" +
			"- ls: List directory contents\n- zap: Zaps things\n</tools>",
		"<rules>\n- Prefer grep, find and ls over bash for file exploration (faster, respects .gitignore)\n",
		"- Use read to examine files instead of cat or sed.", "- Use zap for zapping.",
		"- Be concise in your responses\n- Show file paths clearly when working with files\n</rules>",
		"<project_context>\n<context file=\"/w/AGENTS.md\">\nRULES\n</context>\n</project_context>\n\nAPPENDED", "<name>sk</name>",
		"<environment>\nWorking directory: /w\nDate: ",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q\n%s", want, sys)
		}
	}
	// order: preamble, tools, rules, project context, skills, environment
	last := -1
	for _, marker := range []string{"You are koine", "<tools>", "<rules>", "<project_context>", "<available_skills>", "<environment>"} {
		i := strings.Index(sys, marker)
		if i <= last {
			t.Fatalf("%s is out of order in:\n%s", marker, sys)
		}
		last = i
	}
}

func TestSystemPromptRulesFollowTheActiveTools(t *testing.T) {
	a := newAgent(nil)
	a.Tools = tools.NewRegistry()
	tools.RegisterBuiltins(a.Tools, "/w")
	a.Tools = a.Tools.Filtered([]string{"read", "bash"}, nil) // Pi's default selection has no search tools
	sys := promptOf(t, a)
	if !strings.Contains(sys, "- Use bash for file operations like ls, rg, find") || strings.Contains(sys, "- grep:") || strings.Contains(sys, "Prefer grep") {
		t.Errorf("bash-only rules wrong:\n%s", sys)
	}
	if strings.Contains(sys, "Use edit for precise changes") {
		t.Error("the guidelines of an inactive tool must not appear")
	}
	a.Tools = a.Tools.Filtered([]string{"read"}, nil)
	if sys = promptOf(t, a); strings.Contains(sys, "bash") {
		t.Errorf("no bash rule without bash:\n%s", sys)
	}
	sys = promptOf(t, &Agent{Model: "m", Cwd: "/w", Tools: tools.NewRegistry()}) // nothing registered
	if !strings.Contains(sys, "<rules>") || strings.Contains(sys, "<tools>") {
		t.Errorf("no tools: %s", sys)
	}
}

func TestCustomBaseReplacesThePreambleAndToolSections(t *testing.T) {
	a := newAgent(nil)
	a.Tools = tools.NewRegistry()
	tools.RegisterBuiltins(a.Tools, "/w")
	a.Base = "MY SYSTEM"
	sys := promptOf(t, a)
	if !strings.HasPrefix(sys, "MY SYSTEM") || strings.Contains(sys, "<tools>") || strings.Contains(sys, "<rules>") || !strings.Contains(sys, "<environment>") {
		t.Errorf("%s", sys)
	}
}

func TestAddUserContextIsPersistedAndSentToTheModel(t *testing.T) {
	a := newAgent(nil)
	var saved []string
	a.Persist = func(m llm.Message) { saved = append(saved, m.Role+":"+m.Content[0].Text) }
	a.AddUserContext("Ran `ls`")
	if len(a.History) != 1 || len(saved) != 1 || saved[0] != "user:Ran `ls`" {
		t.Fatalf("%v %v", a.History, saved)
	}
}
