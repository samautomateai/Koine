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
	"koine/core/auth"
	"koine/core/events"
	"koine/core/ext"
	"koine/core/hooks"
	"koine/core/llm"
	"koine/core/pkgs"
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
	// Mode is what the process runs as, told to extensions as ctx.mode: "tui", "rpc", "json" or "print" (the default). tui and rpc have a
	// user interface that can answer extension dialogs (ctx.hasUI).
	Mode string
	RunOptions
	Load LoadOptions // tools, resources and models chosen on the command line
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
	mcpRT    mcpRuntime // MCP servers of this session (mcpwire.go)
	Trusted  bool
	Info     []string // startup info lines
	Warnings []string
	// Choose lets an interactive frontend ask the user to pick one of options (for /resume and the /tree summary question). It returns the
	// index, or -1 to cancel. Nil in print, JSON and server runs, where those commands fall back to listing and flags.
	Choose func(title string, options []string) int
	// Prompt asks the user for a line of input; secret says it must not be echoed. /login uses it for API keys. Nil = ask on the terminal.
	Prompt func(question string, secret bool) (string, error)
	// NoUserBash turns "!cmd" and "!!cmd" into ordinary prompts. Print, JSON and server runs set it: text piped in or sent over HTTP must not
	// become a shell command.
	NoUserBash bool
	// PrefillEditor, when set by a frontend with an editor, receives the text of the user message a /tree move returned to, instead of a
	// notice that prints it.
	PrefillEditor func(text string)
	// OnDialog, when set, answers extension dialogs (select, confirm, input, editor) instead of Choose and Prompt: the RPC frontend
	// sends them to its client. OnSurface receives every fire-and-forget request (notify, setStatus, ...) as it comes. UIChanged is
	// called after the state behind Surfaces changed, so a frontend can redraw. OnShutdown is called when an extension asks to quit.
	OnDialog   func(ctx context.Context, r ext.UIRequest) ext.UIResponse
	OnSurface  func(r ext.UIRequest)
	UIChanged  func()
	OnShutdown func()
	// UntrustedCode counts the project extensions, hooks and providers that were not loaded because the folder is not trusted.
	UntrustedCode int

	// OnMessage, when set, is told of every message added to the conversation, in order, before it is saved (--mode json uses it).
	OnMessage   func(llm.Message)
	emit        func(agent.Event) // the emit of the HandleInput call in progress (pievents.go)
	Scope       []string          // models of --models / enabledModels, in order: the startup choice when nothing else names a model
	opts        Options
	thinking    string // the explicit thinking level (--thinking, a model suffix or /thinking); "" = from settings
	router      *llm.Router
	providers   *ProviderSetup
	secrets     *auth.Resolver  // shared by every router, so a command cached for auth.json runs once per process
	apiKey      string          // --api-key
	apiKeyProv  string          // the provider it was given for (the first model's)
	ephemeralID string          // session id reported to the shell when sessions are off
	pkgRes      pkgs.LoadResult // the packages in use (packages.go)
	extui       extUI           // what extensions set through ctx.ui (extui.go)
	nextTurn    []events.CustomMessage
	sink        func(agent.Event) // receives events raised outside HandleInput (SetEventSink)
	trigger     bool              // an extension asked for a model turn (sendMessage with triggerTurn) that has not started yet
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
	a := &App{Cwd: cwd, Home: home, opts: opts, secrets: newResolver()}
	a.Trusted = opts.Trust || trust.FromEnv() || trust.Open(home).IsTrusted(cwd)

	a.Agent = &agent.Agent{Cwd: cwd, Provider: lazyProvider{a}, Catalog: loadCatalog(home)}
	a.Agent.OnCustom = a.persistCustom
	a.load(ctx)

	model, err := a.startupModel()
	if err != nil {
		a.Close()
		return nil, err
	}
	model = a.chooseThinking(model)
	a.Agent.Model = model
	if opts.Load.APIKey != "" {
		a.apiKey = opts.Load.APIKey
		a.apiKeyProv, _, _ = a.router.ProviderOf(model)
		a.applyRuntimeKey()
	}
	if err := a.initSession(); err != nil {
		a.Close()
		return nil, err
	}
	a.Agent.Dispatch.Emit(ctx, events.SessionStart, map[string]any{"cwd": cwd, "reason": "startup"})
	a.pushHostState()
	a.discoverResources(ctx, "startup")
	return a, nil
}

// lazyProvider lets the router be rebuilt on /reload without touching the agent.
type lazyProvider struct{ a *App }

func (l lazyProvider) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	return l.a.router.Complete(ctx, r)
}

// Stream makes lazyProvider an llm.Streamer, so the agent streams through the router.
func (l lazyProvider) Stream(ctx context.Context, r llm.Request, fn func(llm.StreamEvent)) (*llm.Response, error) {
	return l.a.router.Stream(ctx, r, fn)
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
		mergeConfig(&cfg, Config{Model: proj.Model, MaxTurns: proj.MaxTurns, Compaction: proj.Compaction,
			LLMSettings: proj.LLMSettings, ImageSettings: proj.ImageSettings, EnabledModels: proj.EnabledModels})
		skippedCode = proj.codeCount()
	}
	a.Cfg = cfg
	a.Warnings = append(a.Warnings, SettingsDiagnostics(a.Cwd, a.Home)...)
	a.providers = NewProviderSetup(a.Cwd, a.Home, a.Trusted, cfg.Providers, a.secrets)
	a.Warnings = append(a.Warnings, a.providers.Warnings...)
	skippedCode += a.providers.Skipped
	a.router = a.providers.Router
	a.applyRuntimeKey()
	a.Agent.Catalog = buildCatalog(a.Home, a.providers.Custom)
	a.loadScope()

	a.Tools = tools.NewRegistry()
	tools.RegisterWith(a.Tools, a.toolOptions())
	a.markBuiltin()
	a.loadPackages()
	skippedCode += a.pkgRes.SkippedProject

	sk := a.loadSkills()
	if len(sk) > 0 {
		a.Tools.Register(skills.Tool(sk))
		a.markBuiltin("skill")
	}
	defs := agents.Discover(a.Cwd, a.Home)
	a.Prompts = a.loadPrompts()

	a.Hooks = &hooks.Runner{Cwd: a.Cwd, SessionID: "koine", ByEvent: cfg.Hooks,
		Logf: func(f string, args ...any) { a.Warnings = append(a.Warnings, fmt.Sprintf(f, args...)) }}

	a.Host = ext.NewHost(a.Cwd, a.Tools)
	a.Host.Trusted = a.Trusted
	a.wireHost()
	a.Host.Logf = func(f string, args ...any) {
		if os.Getenv("KOINE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, f+"\n", args...)
		}
	}
	targets, skippedDirs := a.extensionTargets()
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
		spec.Source = a.targetSource(t)
		if err := a.Host.Start(ctx, spec); err != nil {
			a.Warnings = append(a.Warnings, fmt.Sprintf("extension %s: %v", t.name, err))
		}
	}
	a.Host.SetFlags(a.opts.Load.ExtFlags)
	mcpSkipped := a.startMCP(ctx)
	a.UntrustedCode = skippedCode + mcpSkipped // project MCP servers are code too, so the startup prompt offers to trust the folder
	if skippedCode > 0 {
		a.Warnings = append(a.Warnings, fmt.Sprintf(
			"untrusted folder: %d project extension/hook/provider/package entr%s NOT loaded — run /trust to enable them (or start with -trust)",
			skippedCode, plural(skippedCode)))
	}

	if mcpSkipped > 0 {
		a.Warnings = append(a.Warnings, fmt.Sprintf("untrusted folder: %d project MCP server(s) NOT started — run /trust to enable them", mcpSkipped))
	}
	a.Agent.Tools = a.Tools
	a.Agent.Dispatch = events.Multi{a.Hooks, a.Host}
	a.Agent.Skills = sk
	a.Agent.Defs = defs
	a.Agent.ContextFiles = nil
	if !a.opts.Load.NoContextFiles {
		a.Agent.ContextFiles = agent.LoadContextFiles(a.Cwd, a.Home)
	}
	a.Agent.MaxTurns = cfg.MaxTurns
	a.configureCompaction(cfg.Compaction)
	a.applyLLMSettings()
	if len(defs) > 0 {
		a.Tools.Register(a.Agent.TaskTool())
		a.markBuiltin("task")
	}
	a.applyRunOptions()
	a.pushHostState()
}

func plural(n int) string {
	if n == 1 {
		return "y was"
	}
	return "ies were"
}

// Reload re-reads config, skills, prompts, agents, hooks and extensions.
func (a *App) Reload(ctx context.Context) {
	a.leaving(ctx, "reload", "")
	a.load(ctx)
	a.Agent.Dispatch.Emit(ctx, events.SessionStart, map[string]any{"cwd": a.Cwd, "reason": "reload"})
	a.discoverResources(ctx, "reload")
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

func (a *App) Close() {
	sctx, cancel := shutdownContext()
	a.leaving(sctx, "quit", "")
	cancel()
	if a.Agent.Warm != nil {
		a.Agent.Warm.Stop()
	}
	a.Host.Close()
	a.closeMCP()
}

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
	return a.HandleInputImages(ctx, line, nil, emit)
}

// HandleInputImages is HandleInput for a prompt that comes with images (llm.ImageBlock). Images go with a prompt only, not a slash command.
func (a *App) HandleInputImages(ctx context.Context, line string, images []llm.Block, emit func(agent.Event)) (quit bool, err error) {
	quit, err = a.handleInput(ctx, line, images, emit)
	if err == nil && !quit && a.trigger && !a.Agent.Running() && ctx.Err() == nil { // an extension asked for a model turn (pi.sendMessage)
		a.trigger = false
		prev := a.emit
		a.emit = emit
		err = a.Agent.Continue(ctx, emit)
		a.emit = prev
	}
	return quit, err
}

func (a *App) handleInput(ctx context.Context, line string, images []llm.Block, emit func(agent.Event)) (quit bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" && len(images) == 0 {
		return false, nil
	}
	prev := a.emit
	a.emit = emit
	defer func() { a.emit = prev }()
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	if !a.NoUserBash && a.userBash(ctx, line, emit) {
		return false, nil
	}
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "/skill:") {
		a.flushNextTurn()
		return false, a.Agent.RunWithImages(ctx, line, images, emit)
	}
	if len(images) > 0 {
		notice("images are only sent with a prompt, not a slash command; " + fmt.Sprint(len(images)) + " dropped")
	}
	name, args, _ := strings.Cut(line[1:], " ")
	args = strings.TrimSpace(args)
	if a.sessionCommand(ctx, name, args, emit) || a.thinkingCommand(name, args, emit) || a.mcpCommand(ctx, name, args, emit) ||
		a.authCommand(name, args, emit) || a.packageCommand(name, args, emit) {
		return false, nil
	}
	info := a.Describe()
	switch name {
	case "exit", "quit":
		return true, nil
	case "help":
		notice("/help /new /session /name /resume /fork [id] /clone /tree [id] /label /export [file] /exit /tools /skills /agents /commands " +
			"/model [id[:level]] /thinking [level] /compact [focus] /login [provider] /logout [provider] /mcp /trust /reload /compat " +
			"/packages /sources [filter] /skill:<name> /<command> <args>")
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
			return false, nil
		}
		id, level := llm.SplitThinkingSuffix(args)
		if _, _, rerr := a.router.Resolve(id); rerr != nil {
			emit(agent.Event{Type: "error", Text: rerr.Error()})
			return false, nil
		}
		a.Agent.Model = id
		if level != "" {
			a.thinking, a.Agent.Thinking.Level = level, level
			a.thinkingChanged(level)
		}
		notice("model: " + id)
		return false, nil
	case "compact":
		r, cerr := a.Agent.CompactFor(ctx, args, agent.ReasonManual, emit)
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
			a.flushNextTurn()
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
	var custom map[string]llm.ProviderConfig
	if dir := agentdir.Of(home); dir != "" {
		custom, _, _ = llm.ReadModelsFile(filepath.Join(dir, "models.json"))
	}
	return buildCatalog(home, custom)
}
