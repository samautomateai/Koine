package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"koine/core/ext"
	"koine/core/hooks"
	"koine/core/llm"
)

// Config is read from ~/.koine/config.json and ./.koine/config.json. Hooks are
// also read from .claude/settings.json so existing Claude hooks keep working.
type Config struct {
	Model      string                   `json:"model,omitempty"`
	MaxTurns   int                      `json:"maxTurns,omitempty"`
	Compaction CompactionConfig         `json:"compaction,omitempty"`
	Hooks      map[string][]hooks.Entry `json:"hooks,omitempty"`
	Extensions []ext.Spec               `json:"extensions,omitempty"` // any-language extensions
	Plugin     []string                 `json:"plugin,omitempty"`     // opencode.json style: paths or npm names
	// Providers declares extra OpenAI-compatible (or anthropic-type) endpoints,
	// addressed as "<name>/<model>". Project-level entries need trust: an
	// endpoint + key env var from an untrusted repo could exfiltrate your key.
	Providers map[string]llm.ProviderConfig `json:"providers,omitempty"`
}

// CompactionConfig tunes automatic compaction. Zero values mean the defaults (a 200000-token window, 16384 reserved, 20000 kept).
type CompactionConfig struct {
	Enabled          *bool `json:"enabled,omitempty"` // false turns automatic compaction off; /compact still works
	ContextWindow    int   `json:"contextWindow,omitempty"`
	ReserveTokens    int   `json:"reserveTokens,omitempty"`
	KeepRecentTokens int   `json:"keepRecentTokens,omitempty"`
}

var jsonc = regexp.MustCompile(`(?m)^\s*//.*$|,\s*([}\]])`)

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	b = jsonc.ReplaceAll(b, []byte("$1"))
	return json.Unmarshal(b, v) == nil
}

func mergeConfig(dst *Config, src Config) {
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.MaxTurns != 0 {
		dst.MaxTurns = src.MaxTurns
	}
	c := src.Compaction
	if c.Enabled != nil {
		dst.Compaction.Enabled = c.Enabled
	}
	if c.ContextWindow != 0 {
		dst.Compaction.ContextWindow = c.ContextWindow
	}
	if c.ReserveTokens != 0 {
		dst.Compaction.ReserveTokens = c.ReserveTokens
	}
	if c.KeepRecentTokens != 0 {
		dst.Compaction.KeepRecentTokens = c.KeepRecentTokens
	}
	if dst.Hooks == nil {
		dst.Hooks = map[string][]hooks.Entry{}
	}
	for k, v := range src.Hooks {
		dst.Hooks[k] = append(dst.Hooks[k], v...)
	}
	dst.Extensions = append(dst.Extensions, src.Extensions...)
	dst.Plugin = append(dst.Plugin, src.Plugin...)
	if len(src.Providers) > 0 && dst.Providers == nil {
		dst.Providers = map[string]llm.ProviderConfig{}
	}
	for k, v := range src.Providers {
		dst.Providers[k] = v
	}
}

// codeCount counts the parts of a config that can execute code or redirect
// credentials — what an untrusted project is not allowed to contribute.
func (c Config) codeCount() int {
	n := len(c.Extensions) + len(c.Plugin) + len(c.Providers)
	for _, es := range c.Hooks {
		n += len(es)
	}
	return n
}

// LoadConfigScoped returns user-level and project-level config separately,
// so callers can refuse the project's code parts when the folder is untrusted.
func LoadConfigScoped(cwd, home string) (user, project Config, used []string) {
	read := func(dst *Config, path string, hooksOnly bool) {
		var c Config
		if !readJSON(path, &c) {
			return
		}
		if hooksOnly {
			c = Config{Hooks: c.Hooks}
			if len(c.Hooks) == 0 {
				return
			}
		}
		mergeConfig(dst, c)
		used = append(used, path)
	}
	if home != "" {
		read(&user, filepath.Join(home, ".claude", "settings.json"), true)
		read(&user, filepath.Join(home, ".config", "opencode", "opencode.json"), false)
		read(&user, filepath.Join(home, ".koine", "config.json"), false)
	}
	read(&project, filepath.Join(cwd, ".claude", "settings.json"), true)
	read(&project, filepath.Join(cwd, "opencode.json"), false)
	read(&project, filepath.Join(cwd, ".koine", "config.json"), false)
	return
}

// LoadConfig returns everything merged (project last), as if the project were trusted.
func LoadConfig(cwd, home string) (Config, []string) {
	u, p, used := LoadConfigScoped(cwd, home)
	mergeConfig(&u, p)
	return u, used
}
