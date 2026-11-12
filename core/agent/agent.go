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

	"koine/core/agents"
	"koine/core/events"
	"koine/core/llm"
	"koine/core/skills"
	"koine/core/tools"
)

// Event is what frontends render.
type Event struct {
	// Type is one of: text | thinking | text_delta | thinking_delta | tool_delta | tool_start | tool_end | user_message | queue_update |
	// aborted | notice | error | usage | done. The *_delta events stream a response as it is generated; "text" and "thinking" still
	// carry the whole block once the response is complete.
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"` // text, thinking, a delta, a tool result, a notice, an error, a queued message
	Tool    string          `json:"tool,omitempty"`
	CallID  string          `json:"callId,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	IsError bool            `json:"isError,omitempty"`
	Usage   *UsageInfo      `json:"usage,omitempty"` // on "usage" events: one per model response
	// Data holds the fields of the Pi-named events in pievents.go (compaction_end, auto_retry_start, queue_update, ...), under Pi's names.
	Data map[string]any `json:"data,omitempty"`
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
	// Thinking chooses the thinking level per model; HideThinking keeps thinking text out of the events (the blocks stay in the history).
	Thinking     llm.ThinkingSettings
	HideThinking bool
	// CacheKey identifies the conversation to providers that route prompt-cache hits by it (the session id); CacheLong asks for the
	// provider's extended cache lifetime (KOINE_CACHE_RETENTION=long).
	CacheKey  string
	CacheLong bool
	// Warm keeps the provider's prompt cache from lapsing (cacheWarming); nil = never.
	Warm *llm.Warmer
	// Images are the images.* settings. pendingImages ride along with the next Run (RunWithImages); imageNotice is the last reason images were left out.
	Images        ImageOptions
	pendingImages []llm.Block
	imageNotice   string
	// KeptEntryID tells, right after OnCompact ran, the id of the first session entry the compaction kept ("" = no session).
	KeptEntryID func() string
	// SteeringMode and FollowUpMode say how queued messages are delivered: "one-at-a-time" (default, as in Pi) hands over one per
	// delivery point, "all" hands over every queued message at once. See Steer and FollowUp.
	SteeringMode, FollowUpMode string
	// ToolExecution is "parallel" (default) or "sequential"; see execTools.
	ToolExecution string
	// TransformContext, when set, may rewrite the messages sent with each request (the history itself is not changed).
	TransformContext func(ctx context.Context, msgs []llm.Message) []llm.Message
	// PrepareNextTurn, when set, is called before every model request and may switch the model and the thinking level from then on.
	PrepareNextTurn func(ctx context.Context, t TurnInfo) TurnPlan

	// InputSource is what the input event reports as the source of a prompt: SourceInteractive (default), SourceRPC or SourceExtension.
	InputSource string
	// OnInput is called once the input event of a prompt is settled, before any model request. handled says that no run follows because an
	// extension or a hook took the prompt.
	OnInput func(handled bool)
	// OnCustom stores a custom message an extension added to the conversation (see AddCustom).
	OnCustom func(events.CustomMessage)

	depth       int
	queue       queue
	sysOverride *string // the system prompt an extension chose for the run in progress (before_agent_start)
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

// baseSystem is the system prompt before extensions touch it: the base prompt and the sub-agents the task tool offers.
func (a *Agent) baseSystem() string {
	var b strings.Builder
	b.WriteString(a.basePrompt())
	if a.depth == 0 && len(a.Defs) > 0 {
		b.WriteString("\nSub-agents available through the `task` tool:\n")
		for _, d := range a.Defs {
			fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Description)
		}
	}
	return b.String()
}

func (a *Agent) systemPrompt(ctx context.Context) string {
	base := a.baseSystem()
	if a.sysOverride != nil {
		base = *a.sysOverride
	}
	var b strings.Builder
	b.WriteString(base)
	d := a.dispatch(ctx, events.SystemPrompt, map[string]any{"system": base})
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
	if a.Warm != nil {
		a.Warm.Forget() // the cached prompt belonged to the old conversation
	}
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
				MaxTurns: a.MaxTurns, MaxTokens: a.MaxTokens, depth: a.depth + 1, Catalog: a.Catalog, Thinking: a.Thinking, HideThinking: true}
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
func (a *Agent) autoCompact(ctx context.Context, emit func(Event), reason string) bool {
	r, err := a.CompactFor(ctx, "", reason, emit)
	if err != nil {
		if !errors.Is(err, ErrNothingToCompact) {
			emit(Event{Type: "notice", Text: err.Error()})
		}
		return false
	}
	emit(Event{Type: "notice", Text: compactNotice(r)})
	return true
}
