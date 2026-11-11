// Package app wires everything together: config, discovery, extensions,
// hooks, provider and agent. Every frontend (TUI, desktop, HTTP) uses it.
package app

import (
	"context"
	"fmt"
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"koine/core/agent"
	"koine/core/agents"
	"koine/core/events"
	"koine/core/ext"
	"koine/core/hooks"
	"koine/core/llm"
	"koine/core/prompts"
	"koine/core/session"
	"koine/core/skills"
	"koine/core/tools"
	"koine/core/trust"
)

const DefaultModel = "claude-sonnet-5-5"

// Options control startup. The zero value is safe: project code stays off
// until the folder is trusted.
type Options struct {
	Trust bool // trust this project for this run (the -trust flag)
	RunOptions
}

type App struct {
	Cwd      string
	Home     string
	Cfg      Config
	Agent    *agent.Agent
	Session  *session.Session // nil with --no-session
	Host     *ext.Host
	Hooks    *hooks.Runner
	Prompts  []prompts.Prompt
	Tools    *tools.Registry
	Trusted  bool
	Info     []string // startup info lines
	Warnings []string

	opts   Options
	router *llm.Router
}

type Info struct {
	Cwd      string   `json:"cwd"`
	Model    string   `json:"model"`
	Trusted  bool     `json:"trusted"`
	Tools    []string `json:"tools"`
	Skills   []string `json:"skills"`
	Agents   []string `json:"agents"`
	Commands []string `json:"commands"`
	Warnings []string `json:"warnings"`
}

func New(ctx context.Context, cwd string) (*App, error) { return NewWithOptions(ctx, cwd, Options{}) }

func NewWithOptions(ctx context.Context, cwd string, opts Options) (*App, error) {
	home, _ := os.UserHomeDir()
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	a := &App{Cwd: cwd, Home: home, opts: opts}
	a.Trusted = opts.Trust || trust.FromEnv() || trust.Open(home).IsTrusted(cwd)

	a.Agent = &agent.Agent{Cwd: cwd, Provider: lazyProvider{a}, Catalog: loadCatalog(home)}
	a.load(ctx)

	model := a.Cfg.Model
	if m := os.Getenv("KOINE_MODEL"); m != "" {
		model = m
	}
	if opts.Model != "" {
		model = opts.Model
	}
	if model == "" {
		model = DefaultModel
	}
	if opts.Model != "" {
		if _, _, err := a.router.Resolve(model); err != nil {
			a.Close()
			return nil, err
		}
	}
	a.Agent.Model = model
	if err := a.initSession(); err != nil {
		a.Close()
		return nil, err
	}
	a.Agent.Dispatch.Emit(ctx, events.SessionStart, map[string]any{"cwd": cwd, "reason": "startup"})
	return a, nil
}

// lazyProvider lets the router be rebuilt on /reload without touching the agent.
type lazyProvider struct{ a *App }

func (l lazyProvider) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	return l.a.router.Complete(ctx, r)
}

// load (re)discovers config, skills, prompts, agents, hooks and extensions. It
// keeps conversation history and the selected model.
func (a *App) load(ctx context.Context) {
	a.Warnings, a.Info = nil, nil
	if a.Host != nil {
		a.Host.Close()
	}
	user, proj, used := LoadConfigScoped(a.Cwd, a.Home)
	for _, u := range used {
		a.Info = append(a.Info, "config: "+u)
	}
	cfg := user
	skippedCode := 0
	if a.Trusted {
		mergeConfig(&cfg, proj)
	} else {
		// harmless scalars still apply; code and credential routing do not
		mergeConfig(&cfg, Config{Model: proj.Model, MaxTurns: proj.MaxTurns, Compaction: proj.Compaction})
		skippedCode = proj.codeCount()
	}
	a.Cfg = cfg
	a.Warnings = append(a.Warnings, SettingsDiagnostics(a.Cwd, a.Home)...)
	a.router = &llm.Router{Custom: cfg.Providers}

	a.Tools = tools.NewRegistry()
	tools.RegisterBuiltins(a.Tools, a.Cwd)

	sk, bad := skills.DiscoverReport(a.Cwd, a.Home)
	for _, b := range bad {
		a.Warnings = append(a.Warnings, fmt.Sprintf("skill %s refused: %s", b.Path, b.Reason))
	}
	if len(sk) > 0 {
		a.Tools.Register(skills.Tool(sk))
	}
	defs := agents.Discover(a.Cwd, a.Home)
	a.Prompts = prompts.Discover(a.Cwd, a.Home)

	a.Hooks = &hooks.Runner{Cwd: a.Cwd, SessionID: "koine", ByEvent: cfg.Hooks,
		Logf: func(f string, args ...any) { a.Warnings = append(a.Warnings, fmt.Sprintf(f, args...)) }}

	a.Host = ext.NewHost(a.Cwd, a.Tools)
	a.Host.Trusted = a.Trusted
	a.Host.Logf = func(f string, args ...any) {
		if os.Getenv("KOINE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, f+"\n", args...)
		}
	}
	targets, skippedDirs := discoverExtensions(a.Cwd, a.Home, cfg, a.Trusted)
	skippedCode += skippedDirs
	for _, t := range targets {
		var spec ext.Spec
		if t.spec != nil {
			spec = *t.spec
		} else {
			var err error
			if spec, err = ext.JSSpec(t.name, t.js); err != nil {
				a.Warnings = append(a.Warnings, fmt.Sprintf("extension %s: %v", t.name, err))
				continue
			}
		}
		if spec.Name == "" {
			spec.Name = t.name
		}
		if err := a.Host.Start(ctx, spec); err != nil {
			a.Warnings = append(a.Warnings, fmt.Sprintf("extension %s: %v", t.name, err))
		}
	}
	if skippedCode > 0 {
		a.Warnings = append(a.Warnings, fmt.Sprintf(
			"untrusted folder: %d project extension/hook/provider entr%s NOT loaded — run /trust to enable them (or start with -trust)",
			skippedCode, plural(skippedCode)))
	}

	a.Agent.Tools = a.Tools
	a.Agent.Dispatch = events.Multi{a.Hooks, a.Host}
	a.Agent.Skills = sk
	a.Agent.Defs = defs
	a.Agent.ContextFiles = agent.LoadContextFiles(a.Cwd, a.Home)
	a.Agent.MaxTurns = cfg.MaxTurns
	a.configureCompaction(cfg.Compaction)
	if len(defs) > 0 {
		a.Tools.Register(a.Agent.TaskTool())
	}
	a.applyRunOptions()
}

func plural(n int) string {
	if n == 1 {
		return "y was"
	}
	return "ies were"
}

// Reload re-reads config, skills, prompts, agents, hooks and extensions.
func (a *App) Reload(ctx context.Context) {
	a.load(ctx)
	a.Agent.Dispatch.Emit(ctx, events.SessionStart, map[string]any{"cwd": a.Cwd, "reason": "reload"})
}

// TrustProject records trust for the current folder and reloads.
func (a *App) TrustProject(ctx context.Context) error {
	if err := trust.Open(a.Home).Add(a.Cwd); err != nil {
		return err
	}
	a.Trusted = true
	a.Reload(ctx)
	return nil
}

func (a *App) Close() { a.Host.Close() }

func (a *App) Describe() Info {
	i := Info{Cwd: a.Cwd, Model: a.Agent.Model, Trusted: a.Trusted, Tools: a.Tools.Names(), Warnings: a.Warnings}
	for _, s := range a.Agent.Skills {
		i.Skills = append(i.Skills, s.Name)
	}
	for _, d := range a.Agent.Defs {
		i.Agents = append(i.Agents, d.Name)
	}
	for _, c := range a.Host.Commands() {
		i.Commands = append(i.Commands, c.Name)
	}
	for _, p := range a.Prompts {
		i.Commands = append(i.Commands, p.Name)
	}
	sort.Strings(i.Commands)
	return i
}

// CompatReport lists Pi/opencode APIs that extensions used but koine only
// stubs (inert) or does not implement (unsupported).
func (a *App) CompatReport() []string {
	var out []string
	for _, n := range a.Host.Compat() {
		out = append(out, fmt.Sprintf("%-11s %s  (%s)", n.Kind, n.API, n.Extension))
	}
	return out
}

// HandleInput processes one line: slash commands, templates, or a prompt.
// It reports quit=true for /exit.
func (a *App) HandleInput(ctx context.Context, line string, emit func(agent.Event)) (quit bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return false, nil
	}
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "/skill:") {
		return false, a.Agent.Run(ctx, line, emit)
	}
	name, args, _ := strings.Cut(line[1:], " ")
	args = strings.TrimSpace(args)
	if a.sessionCommand(name, args, emit) {
		return false, nil
	}
	info := a.Describe()
	switch name {
	case "exit", "quit":
		return true, nil
	case "help":
		notice("/help /new /session /name /resume /fork /exit /tools /skills /agents /commands /model [id] /compact [focus] /trust /reload /compat " +
			"/skill:<name> /<command> <args>")
		return false, nil
	case "tools":
		notice(strings.Join(info.Tools, ", "))
		return false, nil
	case "skills":
		notice(orNone(info.Skills))
		return false, nil
	case "agents":
		notice(orNone(info.Agents))
		return false, nil
	case "commands":
		notice(orNone(info.Commands))
		return false, nil
	case "model":
		if args == "" {
			notice(a.Agent.Model)
		} else if _, _, rerr := a.router.Resolve(args); rerr != nil {
			emit(agent.Event{Type: "error", Text: rerr.Error()})
		} else {
			a.Agent.Model = args
			notice("model: " + args)
		}
		return false, nil
	case "compact":
		r, cerr := a.Agent.Compact(ctx, args)
		if cerr != nil {
			notice(cerr.Error())
		} else {
			notice(fmt.Sprintf("compacted: %d messages summarised, about %dk → %dk tokens", r.Dropped, r.TokensBefore/1000, r.TokensAfter/1000))
		}
		return false, nil
	case "reload":
		a.Reload(ctx)
		notice(fmt.Sprintf("reloaded: %d tools, %d skills, %d commands", len(a.Tools.Names()), len(a.Agent.Skills), len(a.Describe().Commands)))
		for _, w := range a.Warnings {
			notice("warning: " + w)
		}
		return false, nil
	case "trust":
		if a.Trusted {
			notice("this folder is already trusted")
			return false, nil
		}
		if terr := a.TrustProject(ctx); terr != nil {
			emit(agent.Event{Type: "error", Text: terr.Error()})
			return false, nil
		}
		notice(fmt.Sprintf("trusted %s and reloaded: %d tools", a.Cwd, len(a.Tools.Names())))
		return false, nil
	case "compat":
		notice(orNone(a.CompatReport()))
		return false, nil
	}
	if out, ok, err := a.Host.RunCommand(ctx, name, args); ok {
		if err != nil {
			emit(agent.Event{Type: "error", Text: err.Error()})
		} else if out != "" {
			notice(out)
		}
		return false, nil
	}
	for _, p := range a.Prompts {
		if p.Name == name {
			return false, a.Agent.Run(ctx, p.Expand(args), emit)
		}
	}
	notice("unknown command /" + name + " (try /help)")
	return false, nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}

// loadCatalog is the built-in model catalog plus the user's own ~/.koine/model-catalog.json, whose entries replace built-in ones with the same id.
// A broken user file is ignored: prices and windows are conveniences, and the built-in data still applies.
func loadCatalog(home string) *llm.Catalog {
	c := llm.DefaultCatalog()
	if home != "" {
		_ = c.Merge(filepath.Join(agentdir.Of(home), "model-catalog.json"))
	}
	return c
}
