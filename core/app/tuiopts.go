package app

import "os"

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TUISettings are the Pi settings about the interactive terminal UI (REQ-TUI-001). They are embedded in Config, so the JSON keys are Pi's.
// Only the user file and trusted projects set them: externalEditor names a program to run.
type TUISettings struct {
	Theme                  opt[string]  `json:"theme"`
	QuietStartup           opt[any]     `json:"quietStartup"` // true, false or "header"
	EditorPaddingX         opt[float64] `json:"editorPaddingX"`
	OutputPad              opt[float64] `json:"outputPad"`
	AutocompleteMaxVisible opt[float64] `json:"autocompleteMaxVisible"`
	ShowHardwareCursor     opt[bool]    `json:"showHardwareCursor"`
	ExternalEditor         opt[string]  `json:"externalEditor"`
	Terminal               struct {
		ShowTerminalProgress opt[bool] `json:"showTerminalProgress"`
		Hyperlinks           opt[any]  `json:"hyperlinks"` // true, false or "auto"
		TrueColor            opt[any]  `json:"trueColor"`  // true, false or "auto"
	} `json:"terminal"`
	Markdown struct {
		CodeBlockIndent opt[string] `json:"codeBlockIndent"`
	} `json:"markdown"`
}

// TUIConfig is TUISettings with defaults applied and numbers clamped to the ranges Pi documents.
type TUIConfig struct {
	Theme                 string
	Quiet                 string // "" (show everything), "true" (hide the header and listing) or "header" (hide the listing)
	EditorPaddingX        int    // 0-3
	OutputPad             int    // 0 or 1
	AutocompleteMax       int    // 3-20
	HardwareCursor        bool
	ExternalEditor        string
	TerminalProgress      bool
	Hyperlinks, TrueColor string // "auto", "true" or "false"
	CodeIndent            string
}

func tri(o opt[any]) string {
	switch v := o.V.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		if v == "true" || v == "false" || v == "auto" {
			return v
		}
	}
	return "auto"
}

func clampInt(o opt[float64], def, lo, hi int) int {
	if !o.Set {
		return def
	}
	return max(lo, min(hi, int(o.V)))
}

// TUI resolves the terminal UI settings. Pi's defaults: theme "system", editorPaddingX 0, outputPad 1, autocompleteMaxVisible 5,
// markdown.codeBlockIndent one space.
func (c Config) TUI() TUIConfig {
	t := c.TUISettings
	r := TUIConfig{
		Theme: "system", EditorPaddingX: clampInt(t.EditorPaddingX, 0, 0, 3), OutputPad: clampInt(t.OutputPad, 1, 0, 1),
		AutocompleteMax: clampInt(t.AutocompleteMaxVisible, 5, 3, 20), HardwareCursor: t.ShowHardwareCursor.V,
		ExternalEditor: t.ExternalEditor.V, TerminalProgress: t.Terminal.ShowTerminalProgress.V,
		Hyperlinks: tri(t.Terminal.Hyperlinks), TrueColor: tri(t.Terminal.TrueColor), CodeIndent: " ",
	}
	if t.Theme.Set && t.Theme.V != "" {
		r.Theme = t.Theme.V
	}
	switch v := t.QuietStartup.V.(type) {
	case bool:
		if v {
			r.Quiet = "true"
		}
	case string:
		if v == "header" {
			r.Quiet = "header"
		}
	}
	if t.Markdown.CodeBlockIndent.Set {
		r.CodeIndent = t.Markdown.CodeBlockIndent.V
	}
	return r
}

// merge lets the values set in o win.
func (s *TUISettings) mergeTUI(o TUISettings) {
	if o.Theme.Set {
		s.Theme = o.Theme
	}
	if o.QuietStartup.Set {
		s.QuietStartup = o.QuietStartup
	}
	if o.EditorPaddingX.Set {
		s.EditorPaddingX = o.EditorPaddingX
	}
	if o.OutputPad.Set {
		s.OutputPad = o.OutputPad
	}
	if o.AutocompleteMaxVisible.Set {
		s.AutocompleteMaxVisible = o.AutocompleteMaxVisible
	}
	if o.ShowHardwareCursor.Set {
		s.ShowHardwareCursor = o.ShowHardwareCursor
	}
	if o.ExternalEditor.Set {
		s.ExternalEditor = o.ExternalEditor
	}
	if o.Terminal.ShowTerminalProgress.Set {
		s.Terminal.ShowTerminalProgress = o.Terminal.ShowTerminalProgress
	}
	if o.Terminal.Hyperlinks.Set {
		s.Terminal.Hyperlinks = o.Terminal.Hyperlinks
	}
	if o.Terminal.TrueColor.Set {
		s.Terminal.TrueColor = o.Terminal.TrueColor
	}
	if o.Markdown.CodeBlockIndent.Set {
		s.Markdown.CodeBlockIndent = o.Markdown.CodeBlockIndent
	}
}

// ThemeArgs are the --theme / --use-theme / --no-themes choices of the command line: entries that name an existing file or directory are
// theme locations, the last other entry is the theme to use.
func (a *App) ThemeArgs() (paths []string, use string, noThemes bool) {
	for _, t := range a.opts.Load.Themes {
		if p := a.resolveUser(t); fileExists(p) {
			paths = append(paths, p)
		} else if t != "" {
			use = t
		}
	}
	return paths, use, a.opts.Load.NoThemes
}
