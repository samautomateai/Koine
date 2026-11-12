package app

import (
	"context"
	"fmt"
	"strings"

	"koine/core/llm"
	"koine/core/tools"
)

// What the RPC frontend (cmd/koine, REQ-RPC-001) needs from the app beyond HandleInput: Pi's Model object, model and thinking-level
// choices, user shell commands and usage statistics.

// ModelInfo is Pi's Model object (https://pi.dev/docs/latest/rpc-commands, "Model Object").
type ModelInfo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	API           string    `json:"api"`
	Provider      string    `json:"provider"`
	BaseURL       string    `json:"baseUrl"`
	Reasoning     bool      `json:"reasoning"`
	Input         []string  `json:"input"`
	ContextWindow int       `json:"contextWindow"`
	MaxTokens     int       `json:"maxTokens"`
	Cost          llm.Price `json:"cost"`
}

func (a *App) modelInfo(provider, id string) ModelInfo {
	m, _ := a.Agent.Catalog.Lookup(id)
	if provider == "" {
		provider = m.Provider
	}
	if provider == "" {
		provider, _, _ = a.router.ProviderOf(id)
	}
	if provider == "" {
		provider = inferProvider(id)
	}
	info := ModelInfo{ID: id, Name: m.Name, Provider: provider, API: "openai-completions", Reasoning: m.Reasoning != nil, Input: m.Input,
		ContextWindow: m.ContextWindow, MaxTokens: m.MaxOutput}
	if provider == "anthropic" {
		info.API = "anthropic-messages"
	}
	if info.Name == "" {
		info.Name = id
	}
	if len(info.Input) == 0 {
		info.Input = []string{"text", "image"}
	}
	if m.Cost != nil {
		info.Cost = *m.Cost
	}
	return info
}

// CurrentModel describes the model in use.
func (a *App) CurrentModel() ModelInfo {
	provider, id := a.ProviderAndModel()
	return a.modelInfo(provider, id)
}

// AvailableModels lists the models whose credentials are set.
func (a *App) AvailableModels() []ModelInfo {
	out := []ModelInfo{}
	for _, c := range catalogCandidates(a.Agent.Catalog) {
		if a.hasCredentials(c.full()) {
			out = append(out, a.modelInfo(c.Provider, c.ID))
		}
	}
	return out
}

// SetModel switches to provider/id, as /model does.
func (a *App) SetModel(provider, id string) (ModelInfo, error) {
	full := id
	if provider != "" {
		full = provider + "/" + id
	}
	if _, _, err := a.router.Resolve(full); err != nil {
		return ModelInfo{}, err
	}
	a.Agent.Model = full
	return a.CurrentModel(), nil
}

// CycleModel moves to the next of the scoped models (--models, enabledModels), or of the available ones. ok is false when there is only
// one to choose from.
func (a *App) CycleModel() (info ModelInfo, scoped, ok bool) {
	var ids []string
	if len(a.Scope) > 1 {
		ids, scoped = a.Scope, true
	} else {
		for _, m := range a.AvailableModels() {
			ids = append(ids, m.Provider+"/"+m.ID)
		}
	}
	if len(ids) < 2 {
		return ModelInfo{}, false, false
	}
	next := ids[0]
	for i, id := range ids {
		if id == a.Agent.Model || strings.HasSuffix(id, "/"+a.Agent.Model) {
			next = ids[(i+1)%len(ids)]
			break
		}
	}
	provider, id, _ := strings.Cut(next, "/")
	if id == "" {
		provider, id = "", next
	}
	info, err := a.SetModel(provider, id)
	return info, scoped, err == nil
}

// ThinkingLevelsFor lists the levels the current model accepts: only "off" when it does not think, otherwise up to its highest level
// (high when the catalog does not say).
func (a *App) ThinkingLevelsFor() []string {
	if !a.CurrentModel().Reasoning {
		return []string{"off"}
	}
	top := "high"
	if m, ok := a.Agent.Catalog.Lookup(a.Agent.Model); ok && m.Reasoning != nil && m.Reasoning.Max != "" {
		top = m.Reasoning.Max
	}
	var out []string
	for _, l := range llm.ThinkingLevels {
		out = append(out, l)
		if l == top {
			break
		}
	}
	return out
}

// SetThinkingLevel sets the thinking level, as /thinking does.
func (a *App) SetThinkingLevel(level string) error {
	if !llm.ValidLevel(level) {
		return fmt.Errorf("thinking level must be one of %s", strings.Join(llm.ThinkingLevels, ", "))
	}
	a.thinking, a.Agent.Thinking.Level = level, level
	a.thinkingChanged(level)
	return nil
}

// CycleThinkingLevel moves to the next level the model accepts. ok is false when the model does not think.
func (a *App) CycleThinkingLevel() (level string, ok bool) {
	levels := a.ThinkingLevelsFor()
	if len(levels) < 2 {
		return "", false
	}
	cur := a.Agent.ThinkingLevel()
	next := levels[0]
	for i, l := range levels {
		if l == cur {
			next = levels[(i+1)%len(levels)]
			break
		}
	}
	return next, a.SetThinkingLevel(next) == nil
}

// BashResult is what a user shell command produced.
type BashResult struct {
	Output         string `json:"output"`
	ExitCode       int    `json:"exitCode"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// RunBash runs a shell command for the user. Unless exclude is set, its output is added to the conversation before the next prompt, as
// "!cmd" does. update, when set, receives the output so far.
func (a *App) RunBash(ctx context.Context, cmd string, exclude bool, update func(string)) (BashResult, error) {
	sh := tools.Shell{Path: a.Cfg.ShellPath, Prefix: a.Cfg.ShellCommandPrefix, Cwd: a.Cwd, Home: a.Home, Env: a.shellEnv}
	res, err := sh.Run(ctx, cmd, 0, update)
	if err != nil {
		return BashResult{}, err
	}
	if !exclude {
		status := ""
		switch {
		case res.Cancelled:
			status = "cancelled"
		case res.ExitCode != 0:
			status = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		a.Agent.AddUserContext(userBashContext(cmd, res.Output, status))
	}
	return BashResult{Output: res.Output, ExitCode: res.ExitCode, Cancelled: res.Cancelled, Truncated: res.Truncated,
		FullOutputPath: res.FullOutputPath}, nil
}

// SessionStats are the figures of get_session_stats.
type SessionStats struct {
	UserMessages, AssistantMessages, ToolCalls, ToolResults, TotalMessages int
	Tokens                                                                 llm.Usage
	Cost                                                                   float64
	ContextTokens, ContextWindow                                           int
}

// Stats counts the conversation and its token use.
func (a *App) Stats() SessionStats {
	var s SessionStats
	for _, m := range a.Agent.History {
		s.TotalMessages++
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				s.ToolCalls++
			case "tool_result":
				s.ToolResults++
			}
		}
		switch {
		case m.Role == "assistant":
			s.AssistantMessages++
		case len(m.Content) > 0 && m.Content[0].Type != "tool_result":
			s.UserMessages++
		}
	}
	s.Tokens, s.Cost = a.Agent.Totals, a.Agent.CostTotal
	s.ContextWindow = a.Agent.ContextWindowSize()
	s.ContextTokens = a.Agent.ContextTokens()
	return s
}
