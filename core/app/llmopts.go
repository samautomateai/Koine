package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"koine/core/agent"
	"koine/core/llm"
)

// opt is a settings value that may be absent. A value of the wrong type is treated as absent, not as an error that would discard the whole
// settings file; SettingsDiagnostics reports it.
type opt[T any] struct {
	V   T
	Set bool
}

func (o *opt[T]) UnmarshalJSON(b []byte) error {
	var v T
	if json.Unmarshal(b, &v) == nil {
		o.V, o.Set = v, true
	}
	return nil
}

// timeoutMs is httpIdleTimeoutMs: milliseconds, or "disabled" (same as 0).
type timeoutMs struct {
	D   time.Duration
	Set bool
}

func (t *timeoutMs) UnmarshalJSON(b []byte) error {
	var n float64
	if json.Unmarshal(b, &n) == nil {
		t.D, t.Set = time.Duration(n*float64(time.Millisecond)), true
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil && s == "disabled" {
		t.D, t.Set = 0, true
	}
	return nil
}

// LLMSettings are the Pi settings about thinking, retries and timeouts. They are embedded in Config, so the JSON keys are Pi's.
type LLMSettings struct {
	DefaultThinkingLevel opt[string]            `json:"defaultThinkingLevel"`
	ModelThinkingLevels  opt[map[string]string] `json:"modelThinkingLevels"`
	ThinkingBudgets      opt[map[string]int]    `json:"thinkingBudgets"`
	HideThinkingBlock    opt[bool]              `json:"hideThinkingBlock"`
	HTTPIdleTimeoutMs    timeoutMs              `json:"httpIdleTimeoutMs"`
	Retry                struct {
		Enabled         opt[bool]    `json:"enabled"`
		MaxRetries      opt[float64] `json:"maxRetries"`
		BaseDelayMs     opt[float64] `json:"baseDelayMs"`
		MaxAgentDelayMs opt[float64] `json:"maxAgentDelayMs"`
		Provider        struct {
			TimeoutMs       opt[float64] `json:"timeoutMs"`
			MaxRetries      opt[float64] `json:"maxRetries"` // Koine has one retry loop, so this is accepted and not used
			MaxRetryDelayMs opt[float64] `json:"maxRetryDelayMs"`
		} `json:"provider"`
	} `json:"retry"`
}

// merge lets the values set in o win.
func (s *LLMSettings) merge(o LLMSettings) {
	if o.DefaultThinkingLevel.Set {
		s.DefaultThinkingLevel = o.DefaultThinkingLevel
	}
	if o.ModelThinkingLevels.Set { // per-model levels from both files apply; the later file wins for a model they both name
		m := map[string]string{}
		for k, v := range s.ModelThinkingLevels.V {
			m[k] = v
		}
		for k, v := range o.ModelThinkingLevels.V {
			m[k] = v
		}
		s.ModelThinkingLevels = opt[map[string]string]{V: m, Set: true}
	}
	if o.ThinkingBudgets.Set {
		m := map[string]int{}
		for k, v := range s.ThinkingBudgets.V {
			m[k] = v
		}
		for k, v := range o.ThinkingBudgets.V {
			m[k] = v
		}
		s.ThinkingBudgets = opt[map[string]int]{V: m, Set: true}
	}
	if o.HideThinkingBlock.Set {
		s.HideThinkingBlock = o.HideThinkingBlock
	}
	if o.HTTPIdleTimeoutMs.Set {
		s.HTTPIdleTimeoutMs = o.HTTPIdleTimeoutMs
	}
	r, or := &s.Retry, o.Retry
	for _, p := range []struct{ d, s *opt[float64] }{
		{&r.MaxRetries, &or.MaxRetries}, {&r.BaseDelayMs, &or.BaseDelayMs}, {&r.MaxAgentDelayMs, &or.MaxAgentDelayMs},
		{&r.Provider.TimeoutMs, &or.Provider.TimeoutMs}, {&r.Provider.MaxRetries, &or.Provider.MaxRetries},
		{&r.Provider.MaxRetryDelayMs, &or.Provider.MaxRetryDelayMs},
	} {
		if p.s.Set {
			*p.d = *p.s
		}
	}
	if or.Enabled.Set {
		r.Enabled = or.Enabled
	}
}

func ms(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

// retryPolicy maps Pi's retry settings onto Koine's single retry loop (see llm.RetryPolicy). retry.maxRetries counts retries, so the
// attempts are one more. A retry.provider.maxRetryDelayMs of 0 removes the limit on a server's Retry-After.
func (s LLMSettings) retryPolicy() llm.RetryPolicy {
	r := s.Retry
	var p llm.RetryPolicy
	if r.Enabled.Set && !r.Enabled.V {
		p.Disabled = true
	}
	if r.MaxRetries.Set {
		if r.MaxRetries.V < 1 {
			p.Disabled = true
		} else {
			p.Attempts = int(r.MaxRetries.V) + 1
		}
	}
	if r.BaseDelayMs.Set && r.BaseDelayMs.V > 0 {
		p.BaseDelay = ms(r.BaseDelayMs.V)
	}
	if r.MaxAgentDelayMs.Set && r.MaxAgentDelayMs.V > 0 {
		p.MaxDelay = ms(r.MaxAgentDelayMs.V)
	}
	if v := r.Provider.MaxRetryDelayMs; v.Set {
		p.MaxServerDelay = ms(v.V)
		if v.V <= 0 {
			p.MaxServerDelay = -1
		}
	}
	switch {
	case r.Provider.TimeoutMs.Set:
		p.Timeout = max(ms(r.Provider.TimeoutMs.V), 0)
	case s.HTTPIdleTimeoutMs.Set:
		p.Timeout = s.HTTPIdleTimeoutMs.D
	}
	return p
}

// thinkingSettings are the thinking choices for the agent: the explicit level, then the settings.
func (s LLMSettings) thinkingSettings(explicit string) llm.ThinkingSettings {
	return llm.ThinkingSettings{Level: explicit, Default: s.DefaultThinkingLevel.V, PerModel: s.ModelThinkingLevels.V, Budgets: s.ThinkingBudgets.V}
}

// applyLLMSettings hands the settings to the agent and the provider router. It runs on every (re)load.
func (a *App) applyLLMSettings() {
	a.router.Retry = a.Cfg.retryPolicy()
	a.Agent.Thinking = a.Cfg.thinkingSettings(a.thinking)
	a.Agent.HideThinking = a.Cfg.HideThinkingBlock.V
}

// chooseThinking takes a ":level" suffix off model and settles the explicit thinking level: --thinking beats the suffix. It returns the
// model without the suffix. An invalid --thinking value is ignored with a warning.
func (a *App) chooseThinking(model string) string {
	model, suffix := llm.SplitThinkingSuffix(model)
	a.thinking = suffix
	if t := a.opts.Thinking; t != "" {
		if llm.ValidLevel(t) {
			a.thinking = t
		} else {
			a.Warnings = append(a.Warnings, fmt.Sprintf("--thinking %q is not one of %s; ignored", t, strings.Join(llm.ThinkingLevels, ", ")))
		}
	}
	a.Agent.Thinking.Level = a.thinking
	return model
}

// thinkingCommand handles /thinking [level]: it shows or sets the explicit thinking level. It reports whether it handled name.
func (a *App) thinkingCommand(name, args string, emit func(agent.Event)) bool {
	if name != "thinking" {
		return false
	}
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	switch {
	case args == "":
	case llm.ValidLevel(args):
		a.thinking = args
		a.Agent.Thinking.Level = args
		a.thinkingChanged(args)
	default:
		emit(agent.Event{Type: "error", Text: fmt.Sprintf("thinking level must be one of %s", strings.Join(llm.ThinkingLevels, ", "))})
		return true
	}
	if lvl := a.Agent.ThinkingLevel(); lvl != "" {
		notice(fmt.Sprintf("thinking: %s (%s)", lvl, a.Agent.Model))
	} else {
		notice(fmt.Sprintf("thinking: not used with %s", a.Agent.Model))
	}
	return true
}
