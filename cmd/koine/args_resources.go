package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"koine/core/app"
)

// resourceArgs are the options that choose tools, resources and models beyond the basics in args.go (REQ-CLI-002).
type resourceArgs struct {
	ExcludeTools      []string // -xt
	NoBuiltinTools    bool     // -nbt
	Extensions        []string // -e
	NoExtensions      bool     // -ne
	Skills            []string // --skill
	NoSkills          bool     // -ns
	PromptTemplates   []string // --prompt-template
	NoPromptTemplates bool     // -np
	NoContextFiles    bool     // -nc
	Themes            []string // --theme
	UseTheme          string   // --use-theme
	NoThemes          bool     // --no-themes
	Provider          string   // --provider
	APIKey            string   // --api-key
	Models            []string // --models
	ListModels        bool     // --list-models [search]
	ListSearch        string

	ExtFlags    map[string]any // options an extension registered; filled in by the second parse
	UnknownLong []string       // long options the first parse did not know: candidates for extension flags
}

var resourceFlagSpecs = []flagSpec{
	{"exclude-tools", "xt", valueFlag},
	{"no-builtin-tools", "nbt", boolFlag},
	{"extension", "e", valueFlag},
	{"no-extensions", "ne", boolFlag},
	{"skill", "", valueFlag},
	{"no-skills", "ns", boolFlag},
	{"prompt-template", "", valueFlag},
	{"no-prompt-templates", "np", boolFlag},
	{"no-context-files", "nc", boolFlag},
	{"theme", "", valueFlag},
	{"use-theme", "", valueFlag},
	{"no-themes", "", boolFlag},
	{"provider", "", valueFlag},
	{"api-key", "", valueFlag},
	{"models", "", valueFlag},
	{"list-models", "", optionalFlag},
}

func splitList(val string) (out []string) {
	for _, t := range strings.Split(val, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// setResource stores one of the options above and reports whether long was one of them.
func (c *cliArgs) setResource(long, val string) bool {
	switch long {
	case "exclude-tools":
		c.ExcludeTools = append(c.ExcludeTools, splitList(val)...)
	case "no-builtin-tools":
		c.NoBuiltinTools = true
	case "extension":
		c.Extensions = append(c.Extensions, val)
	case "no-extensions":
		c.NoExtensions = true
	case "skill":
		c.Skills = append(c.Skills, val)
	case "no-skills":
		c.NoSkills = true
	case "prompt-template":
		c.PromptTemplates = append(c.PromptTemplates, val)
	case "no-prompt-templates":
		c.NoPromptTemplates = true
	case "no-context-files":
		c.NoContextFiles = true
	case "theme":
		c.Themes = append(c.Themes, val)
	case "use-theme":
		c.UseTheme = val
	case "no-themes":
		c.NoThemes = true
	case "provider":
		c.Provider = val
	case "api-key":
		c.APIKey = val
	case "models":
		c.Models = append(c.Models, splitList(val)...)
	case "list-models":
		c.ListModels, c.ListSearch = true, val
	default:
		return false
	}
	return true
}

// validateResources checks combinations Pi rejects: --provider and --api-key need a model, and a tool list is either names or +/- changes.
func (c cliArgs) validateResources() error {
	switch {
	case c.Provider != "" && c.Model == "":
		return fmt.Errorf("--provider needs --model")
	case c.APIKey != "" && c.Model == "" && len(c.Models) == 0:
		return fmt.Errorf("--api-key needs --model or --models")
	}
	plain, adj := 0, 0
	for _, t := range c.Tools {
		if strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-") {
			adj++
		} else {
			plain++
		}
	}
	if plain > 0 && adj > 0 {
		return fmt.Errorf("--tools cannot mix plain names with +name/-name entries")
	}
	return nil
}

// extOption handles a long option that koine itself does not define. On the first parse (ext == nil) it is remembered and, when it has a
// value (--name=value) or is a plain switch, passed on; the second parse, with the types the extensions registered, reads the value of
// string flags and rejects every name no extension registered. Short options are never extension options.
func (c *cliArgs) extOption(arg, name, val string, hasVal bool, args []string, i *int, ext map[string]string) (bool, error) {
	if !strings.HasPrefix(arg, "--") {
		return false, nil
	}
	if ext == nil {
		c.UnknownLong = append(c.UnknownLong, name)
		if c.ExtFlags == nil {
			c.ExtFlags = map[string]any{}
		}
		if hasVal {
			c.ExtFlags[name] = val
		} else {
			c.ExtFlags[name] = true
		}
		return true, nil
	}
	kind, ok := ext[name]
	if !ok {
		return false, nil
	}
	if c.ExtFlags == nil {
		c.ExtFlags = map[string]any{}
	}
	switch {
	case kind == "string" && hasVal:
		c.ExtFlags[name] = val
	case kind == "string":
		if *i+1 >= len(args) {
			return true, fmt.Errorf("option %s needs a value", arg)
		}
		*i++
		c.ExtFlags[name] = args[*i]
	case hasVal:
		c.ExtFlags[name] = val != "false" && val != "0"
	default:
		c.ExtFlags[name] = true
	}
	return true, nil
}

// loadOptions is what the app needs to know of these options.
func (c cliArgs) loadOptions() app.LoadOptions {
	themes := c.Themes
	if c.UseTheme != "" {
		themes = append(append([]string(nil), themes...), c.UseTheme)
	}
	return app.LoadOptions{
		ExcludeTools: c.ExcludeTools, NoBuiltinTools: c.NoBuiltinTools,
		Extensions: c.Extensions, NoExtensions: c.NoExtensions, Skills: c.Skills, NoSkills: c.NoSkills,
		PromptTemplates: c.PromptTemplates, NoPromptTemplates: c.NoPromptTemplates, NoContextFiles: c.NoContextFiles,
		Themes: themes, NoThemes: c.NoThemes, Provider: c.Provider, APIKey: c.APIKey, Models: c.Models, ExtFlags: c.ExtFlags,
	}
}

// extensionFlagHelp starts the extensions the way a run would (without a session) and lists the options they registered. It returns
// "" when there are none or the app cannot start.
func extensionFlagHelp(c cliArgs) string {
	cwd := c.Dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	a, err := app.NewWithOptions(context.Background(), cwd, app.Options{Trust: c.Trust, RunOptions: app.RunOptions{NoSession: true}, Load: c.loadOptions()})
	if err != nil {
		return ""
	}
	defer a.Close()
	var b strings.Builder
	for _, f := range a.ExtFlags() {
		arg := "--" + f.Name
		if f.Type == "string" {
			arg += " VALUE"
		}
		desc := f.Description
		if f.Default != nil {
			desc += fmt.Sprintf(" (default %v)", f.Default)
		}
		fmt.Fprintf(&b, "  %-28s %s [%s]\n", arg, strings.TrimSpace(desc), f.Extension)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\nExtension options:\n" + b.String()
}
