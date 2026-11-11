package llm

import (
	"encoding/json"
	"strings"
)

// ThinkingLevels are Pi's thinking level names, from least to most effort.
var ThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// DefaultThinkingLevel is what a reasoning model uses when nothing else is set (Pi's default).
const DefaultThinkingLevel = "medium"

// Thinking modes say how a provider is asked to think.
const (
	ModeAdaptive = "adaptive" // Anthropic: thinking {"type":"adaptive"} plus output_config.effort
	ModeBudget   = "budget"   // Anthropic: thinking {"type":"enabled","budget_tokens":N}
	ModeEffort   = "effort"   // OpenAI-compatible: reasoning_effort
)

// defaultBudgets are the budget_tokens for the levels that have one. They are Koine's values: Pi's built-in budgets are not documented.
var defaultBudgets = map[string]int{"minimal": 1024, "low": 2048, "medium": 8192, "high": 16384}

// minBudget is the smallest budget_tokens Anthropic accepts.
const minBudget = 1024

// ValidLevel reports whether s is one of Pi's thinking level names.
func ValidLevel(s string) bool { return levelIndex(s) >= 0 }

func levelIndex(s string) int {
	for i, l := range ThinkingLevels {
		if l == s {
			return i
		}
	}
	return -1
}

// SplitThinkingSuffix splits "model:level" into the model and the level. Only a trailing Pi level name counts, so ids such as
// "llama3:8b" stay whole.
func SplitThinkingSuffix(s string) (model, level string) {
	if i := strings.LastIndex(s, ":"); i > 0 && ValidLevel(s[i+1:]) {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Reasoning describes how a model thinks. In a catalog file `"reasoning": true` means a model that thinks, with the defaults for its provider.
type Reasoning struct {
	Mode string `json:"mode,omitempty"` // adaptive | budget | effort ("" = budget for Anthropic, effort for others)
	Max  string `json:"max,omitempty"`  // highest level the model accepts; higher ones are lowered to it ("" = the mode's default)
	Off  string `json:"off,omitempty"`  // thinking.type sent for "off" in adaptive/budget modes: "disabled" (default), another type, or "omit"

	none bool // the catalog said `"reasoning": false`
}

// UnmarshalJSON accepts true or false (Pi's flag) as well as the object form.
func (r *Reasoning) UnmarshalJSON(b []byte) error {
	var flag bool
	if json.Unmarshal(b, &flag) == nil {
		*r = Reasoning{none: !flag}
		return nil
	}
	type plain Reasoning
	return json.Unmarshal(b, (*plain)(r))
}

// Thinking is the resolved request for one call: what to ask the provider for.
type Thinking struct {
	Level  string // requested level, after lowering to what the model accepts ("off" is possible)
	Mode   string
	Budget int    // budget_tokens for ModeBudget
	Off    string // see Reasoning.Off
}

// ThinkingSettings are the inputs for choosing a Thinking, from the command line, settings and the session.
type ThinkingSettings struct {
	Level    string            // explicit choice (--thinking, a ":level" suffix, /thinking): beats every setting, and applies to unknown models
	Default  string            // defaultThinkingLevel
	PerModel map[string]string // modelThinkingLevels, keyed by "provider/modelId"
	Budgets  map[string]int    // thinkingBudgets: minimal, low, medium, high
}

// Effective is the level that would be requested for model, before it is lowered to what the model accepts: the explicit level, then
// the per-model setting, then the default setting, then "medium". explicit says the user chose it for this model, so it also applies to
// models the catalog does not know.
func (s ThinkingSettings) Effective(c *Catalog, model string) (level string, explicit bool) {
	if s.Level != "" {
		return s.Level, true
	}
	keys := []string{model}
	if m, ok := c.Lookup(model); ok && m.Provider != "" && !strings.Contains(model, "/") {
		keys = append(keys, m.Provider+"/"+m.ID, m.Provider+"/"+model)
	}
	for _, k := range keys {
		if l := s.PerModel[k]; ValidLevel(l) {
			return l, true
		}
	}
	if ValidLevel(s.Default) {
		return s.Default, false
	}
	return DefaultThinkingLevel, false
}

// Resolve decides what to ask model for. nil means send no thinking parameters: the catalog says the model does not think, or the model
// is unknown and nobody asked for a level for it.
func (s ThinkingSettings) Resolve(c *Catalog, model string) *Thinking {
	level, explicit := s.Effective(c, model)
	m, known := c.Lookup(model)
	var r Reasoning
	switch {
	case known && (m.Reasoning == nil || m.Reasoning.none):
		return nil
	case known:
		r = *m.Reasoning
	case !explicit:
		return nil
	}
	mode := r.Mode
	if mode == "" {
		mode = ModeEffort
		if m.Provider == "anthropic" || strings.HasPrefix(model, "claude") || strings.HasPrefix(model, "anthropic/") {
			mode = ModeBudget
		}
	}
	top := r.Max
	if top == "" {
		top = "high"
		if mode == ModeAdaptive {
			top = "max"
		}
	}
	if levelIndex(level) > levelIndex(top) && levelIndex(top) >= 0 {
		level = top
	}
	t := &Thinking{Level: level, Mode: mode, Off: r.Off}
	if mode == ModeBudget && level != "off" {
		t.Budget = s.budget(level)
	}
	return t
}

// budget is budget_tokens for level. Levels above "high" use high's budget, because thinkingBudgets has no entries for them.
func (s ThinkingSettings) budget(level string) int {
	if levelIndex(level) > levelIndex("high") {
		level = "high"
	}
	b := s.Budgets[level]
	if b <= 0 {
		b = defaultBudgets[level]
	}
	return max(b, minBudget)
}
