package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"koine/core/llm"
	"os"
	"strings"
)

// Environment variables Koine sets for the programs it starts. They are Pi's, renamed (see compat/DIVERGENCES.md):
//
//	AI_AGENT=koine, KOINE_CODING_AGENT=true     set once by the command line for itself and every child
//	KOINE_SESSION_ID, KOINE_SESSION_FILE,       given to the model's bash tool for each command (not to hooks or extensions)
//	KOINE_SESSION_PROVIDER, KOINE_SESSION_MODEL,
//	KOINE_SESSION_REASONING_LEVEL (only when the model is asked to think)
//
// The session variables are not called KOINE_MODEL and KOINE_PROVIDER because KOINE_MODEL already means "start with this model".

// SetProcessMarkers tells the programs Koine starts, and any agent they run, that they are inside Koine. The command line calls it; a
// program that embeds the app package does not.
func SetProcessMarkers() {
	_ = os.Setenv("AI_AGENT", "koine")
	_ = os.Setenv("KOINE_CODING_AGENT", "true")
}

// shellEnv is the environment added to each command of the bash tool: the session and the model in use right now.
func (a *App) shellEnv() []string {
	id := a.ephemeralID
	file := ""
	if a.Session != nil {
		id, file = a.Session.ID, a.Session.Path
	} else if id == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		id = hex.EncodeToString(b)
		a.ephemeralID = id
	}
	provider, model := a.ProviderAndModel()
	env := []string{"KOINE_SESSION_ID=" + id, "KOINE_SESSION_PROVIDER=" + provider, "KOINE_SESSION_MODEL=" + model}
	if file != "" {
		env = append(env, "KOINE_SESSION_FILE="+file)
	}
	if lvl := a.Agent.ThinkingLevel(); lvl != "" {
		env = append(env, "KOINE_SESSION_REASONING_LEVEL="+lvl)
	}
	return env
}

// ProviderAndModel splits the model in use into the provider that serves it and the id that provider knows it by.
func (a *App) ProviderAndModel() (provider, model string) {
	m := a.Agent.Model
	if pre, rest, ok := strings.Cut(m, "/"); ok && a.providerKnown(pre) {
		return pre, rest
	}
	if mod, ok := a.Agent.Catalog.Lookup(m); ok && mod.Provider != "" {
		return mod.Provider, m
	}
	return inferProvider(m), m
}

// startupModel picks the model the run starts with: --model (matched against the catalog), else $KOINE_MODEL, else the settings, else the
// first model of the scope (--models or enabledModels), else the built-in default.
func (a *App) startupModel() (string, error) {
	o := a.opts
	if o.Load.Provider != "" && o.Model == "" {
		return "", fmt.Errorf("--provider needs --model")
	}
	if o.Model != "" { // match the model first, then keep the optional ":level" for chooseThinking
		base, level := llm.SplitThinkingSuffix(o.Model)
		m, err := a.resolveModel(base, o.Load.Provider)
		if err != nil || level == "" {
			return m, err
		}
		return m + ":" + level, nil
	}
	model := a.Cfg.Model
	if m := os.Getenv("KOINE_MODEL"); m != "" {
		model = m
	}
	if model == "" && len(a.Scope) > 0 {
		model = a.Scope[0]
	}
	if model == "" {
		model = DefaultModel
	}
	return model, nil
}

// loadScope expands --models, else the enabledModels setting, into a.Scope.
func (a *App) loadScope() {
	patterns := a.opts.Load.Models
	if len(patterns) == 0 {
		patterns = a.Cfg.EnabledModels
	}
	var warns []string
	a.Scope, warns = a.expandScope(patterns)
	a.Warnings = append(a.Warnings, warns...)
}
