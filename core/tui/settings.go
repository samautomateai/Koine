package tui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"koine/core/agentdir"
	"koine/core/app"
)

func base64Std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// Settings are the terminal UI settings in effect, resolved from the app's config and the environment.
type Settings struct {
	app.TUIConfig
	TrueColor    bool   // 24-bit colour is used
	Links        bool   // OSC 8 hyperlinks are used
	FullscreenOn bool   // fullscreen mode is in use (tuiMode, and a terminal that can show it)
	ImageProto   string // "kitty", "iterm2" or "" (images are described in text)
	HideThinking bool
	DoubleEscape string
	TreeFilter   string
}

// Env reads environment variables; tests replace it.
type Env func(string) string

// ResolveSettings reads the config of a and turns the "auto" choices into booleans using the environment.
func ResolveSettings(a *app.App, env Env) Settings {
	c := a.Cfg.TUI()
	s := Settings{TUIConfig: c, HideThinking: a.Cfg.HideThinkingBlock.V, DoubleEscape: a.DoubleEscapeAction(), TreeFilter: a.TreeFilterMode()}
	switch c.TrueColor {
	case "true":
		s.TrueColor = true
	case "auto":
		s.TrueColor = strings.Contains(env("COLORTERM"), "truecolor") || strings.Contains(env("COLORTERM"), "24bit")
	}
	switch c.Hyperlinks {
	case "true":
		s.Links = true
	case "auto":
		s.Links = supportsLinks(env)
	}
	s.FullscreenOn = fullscreenAllowed(c.Fullscreen, env)
	s.ImageProto = DetectImageProtocol(c.ImageProtoIn, env)
	return s
}

// supportsLinks guesses OSC 8 support from the terminal's environment variables. Pi documents "auto" detection without saying how it
// decides, so this is Koine's own list of terminals known to support the sequence.
func supportsLinks(env Env) bool {
	switch env("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty":
		return true
	}
	if env("KITTY_WINDOW_ID") != "" || env("WT_SESSION") != "" {
		return true
	}
	v, _ := strconv.Atoi(env("VTE_VERSION"))
	return v >= 5000
}

// SaveSetting writes one key (dotted for nested ones, "terminal.hyperlinks") into the user's settings.json, keeping the other keys. A file
// that is not plain JSON (comments, say) is left alone and reported rather than rewritten.
func SaveSetting(home, key string, value any) error {
	dir := agentdir.Of(home)
	if dir == "" {
		return fmt.Errorf("no agent directory to save settings in")
	}
	path := filepath.Join(dir, "settings.json")
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s is not plain JSON, not changing it: %w", path, err)
		}
	}
	cur := doc
	parts := strings.Split(key, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	if value == nil {
		delete(cur, parts[len(parts)-1])
	} else {
		cur[parts[len(parts)-1]] = value
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
