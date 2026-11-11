package app

import (
	"encoding/json"
	"koine/compat"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"koine/core/ext"
	"koine/core/hooks"
	"koine/core/llm"
)

// Config is read from ~/.koine/config.json and ./.koine/config.json. Hooks are
// also read from .claude/settings.json so existing Claude hooks keep working.
type Config struct {
	Model      string                   `json:"model,omitempty"` // Koine's older name for defaultProvider/defaultModel
	MaxTurns   int                      `json:"maxTurns,omitempty"`
	Compaction CompactionConfig         `json:"compaction,omitempty"`
	Hooks      map[string][]hooks.Entry `json:"hooks,omitempty"`
	Extensions []ext.Spec               `json:"-"` // any-language extensions: the objects of "extensions"
	// Names Pi uses (see compat/settings.json). DefaultProvider and DefaultModel are folded into Model, and the strings of "extensions" into
	// Plugin, by normalize.
	DefaultProvider string            `json:"defaultProvider,omitempty"`
	DefaultModel    string            `json:"defaultModel,omitempty"`
	SessionDir      string            `json:"sessionDir,omitempty"`
	DefaultTools    []string          `json:"defaultTools,omitempty"`
	ExtensionsRaw   []json.RawMessage `json:"extensions,omitempty"`
	Plugin          []string          `json:"plugin,omitempty"` // opencode.json style: paths or npm names
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

// normalize folds the Pi spellings into the fields the rest of Koine reads: defaultProvider/defaultModel become Model ("provider/model"),
// and "extensions" entries that are strings become plugin paths while objects stay extension specs.
func (c *Config) normalize() {
	for _, raw := range c.ExtensionsRaw {
		var path string
		if json.Unmarshal(raw, &path) == nil {
			c.Plugin = append(c.Plugin, path)
			continue
		}
		var spec ext.Spec
		if json.Unmarshal(raw, &spec) == nil {
			c.Extensions = append(c.Extensions, spec)
		}
	}
	c.ExtensionsRaw = nil
	if c.DefaultModel != "" {
		c.Model = c.DefaultModel
		if c.DefaultProvider != "" {
			c.Model = c.DefaultProvider + "/" + c.DefaultModel
		}
	}
	c.DefaultProvider, c.DefaultModel = "", ""
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

func onlyAdjustments(l []string) bool {
	for _, e := range l {
		if !strings.HasPrefix(e, "+") && !strings.HasPrefix(e, "-") {
			return false
		}
	}
	return len(l) > 0
}

func mergeConfig(dst *Config, src Config) {
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.MaxTurns != 0 {
		dst.MaxTurns = src.MaxTurns
	}
	if src.SessionDir != "" {
		dst.SessionDir = src.SessionDir
	}
	if src.DefaultTools != nil {
		if onlyAdjustments(src.DefaultTools) && dst.DefaultTools != nil {
			dst.DefaultTools = append(dst.DefaultTools, src.DefaultTools...) // a project's +name/-name list adjusts the user's selection
		} else {
			dst.DefaultTools = src.DefaultTools
		}
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
		c.normalize()
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
		read(&user, filepath.Join(home, ".koine", "config.json"), false) // the older name of settings.json
		read(&user, filepath.Join(home, ".koine", "settings.json"), false)
	}
	read(&project, filepath.Join(cwd, ".claude", "settings.json"), true)
	read(&project, filepath.Join(cwd, "opencode.json"), false)
	read(&project, filepath.Join(cwd, ".koine", "config.json"), false)
	read(&project, filepath.Join(cwd, ".koine", "settings.json"), false)
	return
}

// LoadConfig returns everything merged (project last), as if the project were trusted.
func LoadConfig(cwd, home string) (Config, []string) {
	u, p, used := LoadConfigScoped(cwd, home)
	mergeConfig(&u, p)
	return u, used
}

// SettingsDiagnostics checks Koine's settings files against compat/settings.json (Pi's schema as mapped onto Koine) and returns one line
// per unknown key, wrong type, key that is not acted on yet, or global-only key in a project file. The older config.json name gets a
// pointer to settings.json.
func SettingsDiagnostics(cwd, home string) []string {
	m, err := compat.LoadSettings()
	if err != nil {
		return []string{"settings manifest: " + err.Error()}
	}
	var out []string
	files := []struct {
		path    string
		project bool
	}{{filepath.Join(home, ".koine", "config.json"), false}, {filepath.Join(home, ".koine", "settings.json"), false},
		{filepath.Join(cwd, ".koine", "config.json"), true}, {filepath.Join(cwd, ".koine", "settings.json"), true}}
	for _, f := range files {
		if home == "" && !f.project {
			continue
		}
		var doc map[string]any
		if !readJSON(f.path, &doc) {
			continue
		}
		if filepath.Base(f.path) == "config.json" {
			out = append(out, f.path+": config.json is the old name; rename it to settings.json")
		}
		out = append(out, m.Check(f.path, f.project, doc)...)
	}
	return out
}
