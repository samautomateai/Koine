// Package agent is the model <-> tools loop.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"strings"
	"time"

	"koine/core/agents"
	"koine/core/compact"
	"koine/core/events"
	"koine/core/llm"
	"koine/core/skills"
	"koine/core/tools"
)

// Event is what frontends render.
type Event struct {
	Type    string          `json:"type"` // text | tool_start | tool_end | notice | error | usage | done
	Text    string          `json:"text,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	CallID  string          `json:"callId,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	IsError bool            `json:"isError,omitempty"`
	Usage   *UsageInfo      `json:"usage,omitempty"` // on "usage" events: one per model response
}

type Agent struct {
	Provider     llm.Provider
	Model        string
	Cwd          string
	Tools        *tools.Registry
	Dispatch     events.Dispatcher
	Skills       []skills.Skill
	Defs         []agents.Def
	ContextFiles []string // contents of AGENTS.md / CLAUDE.md style files
	Base         string   // overrides the default system prompt (sub-agents)
	MaxTurns     int
	MaxTokens    int
	History      []llm.Message
	// Compaction. Sizes are in tokens. ContextWindow 0 = take it from the Catalog, else DefaultContextWindow.
	ContextWindow int
	Catalog       *llm.Catalog // model metadata for prices and context windows (nil = none)
	// Usage so far in this conversation. CostTotal covers only responses whose model had a price; Unpriced says some did not.
	Totals      llm.Usage
	CostTotal   float64
	Unpriced    bool
	lastCtx     int  // tokens in the context at the last response (0 = unknown, estimate instead)
	ctxAt       int  // len(History) after that response
	AutoCompact bool // compact before a turn would overflow the window (the manual Compact always works)
	Reserve     int  // room kept free for the answer (default 16384)
	KeepRecent  int  // recent conversation kept verbatim (default 20000)
	// OnCompact is told the old history index of the first kept message, the summary and the size before compaction.
	OnCompact func(firstKept int, summary string, tokensBefore int)
	Persist   func(llm.Message) // called for every message added to History, in order (nil = none)

	depth int
}

const defaultBase = `You are koine, a coding agent working in the user's project through tools.
Be concise. Inspect before you change; prefer small, verifiable edits; run the project's tests or build when relevant.
Use absolute or project-relative paths. When a task is complete, say what changed.`

func (a *Agent) dispatch(ctx context.Context, name string, payload any) events.Decision {
	if a.Dispatch == nil {
		return events.Decision{}
	}
	return a.Dispatch.Emit(ctx, name, payload)
}

func (a *Agent) systemPrompt(ctx context.Context) string {
	var b strings.Builder
	if a.Base != "" {
		b.WriteString(a.Base)
	} else {
		b.WriteString(defaultBase)
	}
	fmt.Fprintf(&b, "\n\nWorking directory: %s\nDate: %s\n", a.Cwd, time.Now().Format("2006-01-02"))
	for _, c := range a.ContextFiles {
		b.WriteString("\n" + c + "\n")
	}
	if s := skills.PromptSection(a.Skills); s != "" {
		b.WriteString("\n" + s + "\n")
	}
	if a.depth == 0 && len(a.Defs) > 0 {
		b.WriteString("\nSub-agents available through the `task` tool:\n")
		for _, d := range a.Defs {
			fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Description)
		}
	}
	d := a.dispatch(ctx, events.SystemPrompt, map[string]any{"system": b.String()})
	if d.SystemAppend != "" {
		b.WriteString("\n" + d.SystemAppend + "\n")
	}
	return b.String()
}

// ExpandInput turns "/skill:name args" into the skill body plus args.
func (a *Agent) ExpandInput(in string) string {
	rest, ok := strings.CutPrefix(in, "/skill:")
	if !ok {
		return in
	}
	name, args, _ := strings.Cut(rest, " ")
	s, found := skills.Find(a.Skills, name)
	if !found {
		return in
	}
	out := skills.Render(s)
	if strings.TrimSpace(args) != "" {
		out += "\n\n" + strings.TrimSpace(args)
	}
	return out
}

// Reset forgets the conversation and the usage so far.
func (a *Agent) Reset() { a.SetHistory(nil) }

// SetHistory replaces the conversation, as when a session is loaded, and rebuilds the usage totals from the messages that carry them.
func (a *Agent) SetHistory(h []llm.Message) {
	a.History, a.Totals, a.CostTotal, a.Unpriced, a.lastCtx, a.ctxAt = h, llm.Usage{}, 0, false, 0, 0
	for _, m := range h {
		if m.Usage == nil {
			continue
		}
		a.Totals.Add(*m.Usage)
		if m.Cost != nil {
			a.CostTotal += m.Cost.Total
		} else {
			a.Unpriced = true
		}
	}
}

// add appends m to the history and hands it to Persist.
func (a *Agent) add(m llm.Message) {
	a.History = append(a.History, m)
	if a.Persist != nil {
		a.Persist(m)
	}
}

// Run executes one user prompt to completion, streaming events through emit.
func (a *Agent) Run(ctx context.Context, prompt string, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}
	d := a.dispatch(ctx, events.Input, map[string]any{"text": prompt})
	if d.Block {
		emit(Event{Type: "notice", Text: "input blocked: " + d.Reason})
		return nil
	}
	if d.Text != nil {
		prompt = *d.Text
	}
	prompt = a.ExpandInput(prompt)
	a.add(llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: prompt}}})

	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}
	defer a.dispatch(context.WithoutCancel(ctx), events.AgentEnd, map[string]any{})

	overflowed := false
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.dispatch(ctx, events.TurnStart, map[string]any{"turn": turn})
		sys := a.systemPrompt(ctx)
		if a.needsCompaction(sys) {
			a.autoCompact(ctx, emit)
		}
		req := llm.Request{Model: a.Model, System: sys, Messages: a.History, MaxTokens: a.MaxTokens}
		for _, n := range a.Tools.Names() {
			t, _ := a.Tools.Get(n)
			req.Tools = append(req.Tools, llm.ToolDef{Name: t.Name(), Description: t.Description(), InputSchema: t.Schema()})
		}
		resp, err := a.Provider.Complete(ctx, req)
		if err != nil && compact.IsOverflow(err) && !overflowed && a.autoCompact(ctx, emit) {
			overflowed = true // one recovery per prompt
			req.Messages = a.History
			resp, err = a.Provider.Complete(ctx, req)
		}
		if err != nil {
			emit(Event{Type: "error", Text: err.Error()})
			return err
		}
		a.add(a.record(resp, emit))
		var uses []llm.Block
		for _, b := range resp.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					emit(Event{Type: "text", Text: b.Text})
				}
			case "tool_use":
				uses = append(uses, b)
			}
		}
		a.dispatch(ctx, events.TurnEnd, map[string]any{"turn": turn})
		if len(uses) == 0 {
			emit(Event{Type: "done"})
			return nil
		}
		var results []llm.Block
		for _, u := range uses {
			results = append(results, a.execTool(ctx, u, emit))
		}
		a.add(llm.Message{Role: "user", Content: results})
	}
	emit(Event{Type: "notice", Text: fmt.Sprintf("stopped after %d turns", maxTurns)})
	emit(Event{Type: "done"})
	return nil
}

func (a *Agent) execTool(ctx context.Context, u llm.Block, emit func(Event)) llm.Block {
	args := u.Input
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	res := llm.Block{Type: "tool_result", ToolUseID: u.ID}
	d := a.dispatch(ctx, events.ToolCall, map[string]any{"toolName": u.Name, "callId": u.ID, "input": args})
	if d.Block {
		res.Content, res.IsError = "Blocked: "+orDefault(d.Reason, "denied by extension"), true
		emit(Event{Type: "tool_start", Tool: u.Name, CallID: u.ID, Args: args})
		emit(Event{Type: "tool_end", Tool: u.Name, CallID: u.ID, Text: res.Content, IsError: true})
		return res
	}
	if len(d.Args) > 0 {
		args = d.Args
	}
	emit(Event{Type: "tool_start", Tool: u.Name, CallID: u.ID, Args: args})

	var out string
	var err error
	if t, ok := a.Tools.Get(u.Name); !ok {
		err = fmt.Errorf("unknown tool %q", u.Name)
	} else {
		out, err = t.Run(ctx, args)
	}
	isErr := err != nil
	if isErr && out == "" {
		out = err.Error()
	}
	d = a.dispatch(ctx, events.ToolResult, map[string]any{"toolName": u.Name, "callId": u.ID, "input": args, "output": out, "isError": isErr})
	if d.Output != nil {
		out = *d.Output
	}
	res.Content, res.IsError = out, isErr
	emit(Event{Type: "tool_end", Tool: u.Name, CallID: u.ID, Text: out, IsError: isErr})
	return res
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// TaskTool exposes the discovered sub-agent definitions as a `task` tool.
func (a *Agent) TaskTool() tools.Tool {
	return &tools.Func{N: "task",
		D: "Delegate a self-contained task to a named sub-agent. It runs in a fresh context and returns its final answer.",
		S: json.RawMessage(`{"type":"object","properties":{"agent":{"type":"string","description":"Sub-agent name"},` +
			`"prompt":{"type":"string","description":"Complete task description"}},"required":["agent","prompt"]}`),
		F: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var p struct{ Agent, Prompt string }
			if err := json.Unmarshal(raw, &p); err != nil {
				return "", err
			}
			def, ok := agents.Find(a.Defs, p.Agent)
			if !ok {
				return "", fmt.Errorf("unknown agent %q", p.Agent)
			}
			if a.depth >= 2 {
				return "", errors.New("sub-agent nesting limit reached")
			}
			reg := a.Tools.Filtered(def.Allow, append(def.Deny, "task"))
			model := a.Model
			if def.Model != "" {
				model = def.Model
			}
			sub := &Agent{Provider: a.Provider, Model: model, Cwd: a.Cwd, Tools: reg, Dispatch: a.Dispatch,
				Skills: a.Skills, ContextFiles: a.ContextFiles, Base: orDefault(def.Prompt, defaultBase),
				MaxTurns: a.MaxTurns, MaxTokens: a.MaxTokens, depth: a.depth + 1}
			var last strings.Builder
			err := sub.Run(ctx, p.Prompt, func(e Event) {
				if e.Type == "text" {
					last.Reset()
					last.WriteString(e.Text)
				}
			})
			return last.String(), err
		}}
}

// LoadContextFiles reads AGENTS.md / CLAUDE.md style project context.
func LoadContextFiles(cwd, home string) []string {
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(agentdir.Of(home), "AGENTS.md"), home+"/.pi/agent/AGENTS.md")
		paths = append(paths, home+"/.config/opencode/AGENTS.md", home+"/.claude/CLAUDE.md")
	}
	paths = append(paths, cwd+"/AGENTS.md", cwd+"/CLAUDE.md", cwd+"/.koine/AGENTS.md")
	var out []string
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			out = append(out, fmt.Sprintf("<context file=%q>\n%s\n</context>", p, strings.TrimSpace(string(b))))
		}
	}
	return out
}

// autoCompact compacts the history and says so. It reports whether the history changed.
func (a *Agent) autoCompact(ctx context.Context, emit func(Event)) bool {
	r, err := a.Compact(ctx, "")
	if err != nil {
		if !errors.Is(err, ErrNothingToCompact) {
			emit(Event{Type: "notice", Text: err.Error()})
		}
		return false
	}
	emit(Event{Type: "notice", Text: compactNotice(r)})
	return true
}
