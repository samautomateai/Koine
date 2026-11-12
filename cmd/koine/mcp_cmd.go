package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"koine/core/mcp"
	"koine/core/tools"
	"koine/core/trust"
)

const mcpUsage = `usage: koine mcp <command> [options]

  add NAME [options] [-- COMMAND [ARGS...]]   add a stdio server (after --) or an HTTP one (--url)
        --url URL                      streamable HTTP endpoint
        --bearer-token-env-var VAR     send "Authorization: Bearer ${VAR}"
        --header NAME=VALUE            extra HTTP header (repeatable; ${VAR} and !command allowed)
        --env NAME=VALUE               environment of a stdio server (repeatable)
        --cwd DIR                      working directory of a stdio server
        --exposure direct|deferred|hidden
        --timeout SECONDS              per request (progress notifications start it again)
        --description TEXT             one sentence for the system prompt
    -l, --local                        write to .koine/mcp.json in the project instead of the user file
  remove NAME [-l]                     delete a server
  list                                 connect to every enabled server and show its state; exit 1 if one is invalid or not connected
  login NAME                           sign in to an OAuth server in the browser
  logout NAME                          forget the stored tokens

Project servers (.koine/mcp.json, .pi/mcp.json, .mcp.json, opencode.json) run programs the repository names, so list and login only use them in a
trusted folder (-a / --trust, $KOINE_TRUST, or /trust in a session). -C DIR selects the project.
`

// runMCP is `koine mcp ...`. It is a subcommand of its own and loads no extensions, skills or models.
func runMCP(args []string, st streams) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(st.Out, mcpUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	o, err := parseMCPArgs(args[1:])
	if err != nil {
		fmt.Fprintln(st.Err, "koine mcp:", err)
		return 2
	}
	home, _ := os.UserHomeDir()
	if o.cwd == "" {
		o.cwd, _ = os.Getwd()
	}
	switch args[0] {
	case "add":
		return mcpAdd(o, home, st)
	case "remove", "rm":
		return mcpRemove(o, home, st)
	case "list", "ls":
		return mcpList(o, home, st)
	case "login":
		return mcpLogin(o, home, st)
	case "logout":
		return mcpLogout(o, home, st)
	}
	fmt.Fprintf(st.Err, "koine mcp: unknown command %q\n\n%s", args[0], mcpUsage)
	return 2
}

type mcpOpts struct {
	positional []string
	command    []string // after --
	url        string
	bearer     string
	headers    []string
	env        []string
	cwd        string
	serverCwd  string
	exposure   string
	timeout    float64
	desc       string
	local      bool
	trust      bool
}

func parseMCPArgs(args []string) (mcpOpts, error) {
	var o mcpOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			o.command = args[i+1:]
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			o.positional = append(o.positional, a)
			continue
		}
		name, val, hasVal := strings.Cut(a, "=")
		value := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch name {
		case "-l", "--local":
			o.local = true
		case "-a", "--trust":
			o.trust = true
		case "-C":
			o.cwd, err = value()
		case "--url":
			o.url, err = value()
		case "--bearer-token-env-var":
			o.bearer, err = value()
		case "--header":
			if v, err = value(); err == nil {
				o.headers = append(o.headers, v)
			}
		case "--env":
			if v, err = value(); err == nil {
				o.env = append(o.env, v)
			}
		case "--cwd":
			o.serverCwd, err = value()
		case "--exposure":
			o.exposure, err = value()
		case "--description":
			o.desc, err = value()
		case "--timeout":
			if v, err = value(); err == nil {
				if o.timeout, err = strconv.ParseFloat(v, 64); err != nil || o.timeout < 0 {
					err = fmt.Errorf("--timeout wants a number of seconds, got %q", v)
				}
			}
		default:
			err = fmt.Errorf("unknown option %s", name)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

func splitKV(list []string, what string) (map[string]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%s %q must look like NAME=VALUE", what, kv)
		}
		out[k] = v
	}
	return out, nil
}

func (o mcpOpts) file(home string) string {
	if o.local {
		return mcp.ProjectFile(o.cwd)
	}
	return mcp.UserFile(home)
}

func mcpAdd(o mcpOpts, home string, st streams) int {
	if len(o.positional) != 1 {
		fmt.Fprintln(st.Err, "koine mcp add: needs exactly one NAME (then --url URL, or -- COMMAND [ARGS...])")
		return 2
	}
	name := o.positional[0]
	cfg := mcp.ServerConfig{Exposure: o.exposure, Timeout: o.timeout, Description: o.desc}
	var err error
	if cfg.Headers, err = splitKV(o.headers, "--header"); err == nil {
		cfg.Env, err = splitKV(o.env, "--env")
	}
	if err != nil {
		fmt.Fprintln(st.Err, "koine mcp add:", err)
		return 2
	}
	switch {
	case o.url != "" && len(o.command) > 0:
		fmt.Fprintln(st.Err, "koine mcp add: give --url or a command after --, not both")
		return 2
	case o.url != "":
		cfg.URL = o.url
		if o.bearer != "" {
			if cfg.Headers == nil {
				cfg.Headers = map[string]string{}
			}
			cfg.Headers["Authorization"] = "Bearer ${" + o.bearer + "}"
		}
	case len(o.command) > 0:
		cfg.Command, cfg.Args, cfg.Cwd = o.command[0], o.command[1:], o.serverCwd
	default:
		fmt.Fprintln(st.Err, "koine mcp add: give --url URL, or the command to run after --")
		return 2
	}
	path := o.file(home)
	if err := mcp.AddServer(path, name, cfg); err != nil {
		fmt.Fprintln(st.Err, "koine mcp add:", err)
		return 1
	}
	fmt.Fprintf(st.Out, "added %s to %s\n", name, path)
	return 0
}

func mcpRemove(o mcpOpts, home string, st streams) int {
	if len(o.positional) != 1 {
		fmt.Fprintln(st.Err, "koine mcp remove: needs exactly one NAME")
		return 2
	}
	path := o.file(home)
	gone, err := mcp.RemoveServer(path, o.positional[0])
	switch {
	case err != nil:
		fmt.Fprintln(st.Err, "koine mcp remove:", err)
		return 1
	case !gone:
		fmt.Fprintf(st.Err, "koine mcp remove: no server named %q in %s\n", o.positional[0], path)
		return 1
	}
	fmt.Fprintf(st.Out, "removed %s from %s\n", o.positional[0], path)
	return 0
}

// mcpTrusted is whether project-level servers may run: -a, $KOINE_TRUST, or a folder trusted before.
func mcpTrusted(o mcpOpts, home string) bool {
	return o.trust || trust.FromEnv() || trust.Open(home).IsTrusted(o.cwd)
}

func mcpList(o mcpOpts, home string, st streams) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg := mcp.Load(o.cwd, home, mcpTrusted(o, home))
	failed := len(cfg.Problems) > 0
	for _, p := range cfg.Problems {
		fmt.Fprintln(st.Err, "koine mcp: invalid:", p.String())
	}
	if cfg.Skipped > 0 {
		fmt.Fprintf(st.Err, "koine mcp: %d project server(s) not started: the folder is not trusted (use -a to trust it for this run)\n", cfg.Skipped)
	}
	if len(cfg.Servers) == 0 {
		fmt.Fprintln(st.Out, "no MCP servers configured")
		return exitFor(failed)
	}
	reg := tools.NewRegistry()
	m := mcp.NewManager(reg, o.cwd, home)
	m.Store = &mcp.TokenStore{Path: mcp.AuthFile(home)}
	if l, err := mcp.OpenLog(mcp.LogFile(home)); err == nil {
		m.Log = l
		defer func() { _ = l.Close() }()
	}
	defer m.Close()
	m.Start(ctx, cfg.Servers, 0)
	statuses := m.Statuses()
	for _, s := range statuses { // slow servers get the same chance as in a session, then are reported as they are
		if s.State == mcp.StateConnecting {
			m.Wait()
			break
		}
	}
	tw := tabwriter.NewWriter(st.Out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tTOOLS\tEXPOSURE\tSOURCE")
	statuses = m.Statuses()
	for _, s := range statuses {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", s.Name, s.State, s.Tools, s.Exposure, s.Source)
		if s.State != mcp.StateConnected && s.State != mcp.StateDisabled {
			failed = true
		}
	}
	_ = tw.Flush()
	for _, s := range statuses {
		if s.Err != "" {
			fmt.Fprintf(st.Err, "koine mcp: %s: %s\n", s.Name, s.Err)
		}
		sort.Strings(s.Overrides)
		for _, ov := range s.Overrides {
			fmt.Fprintf(st.Out, "  %s: %s (differs from the server's %s)\n", s.Name, ov, s.Exposure)
		}
	}
	return exitFor(failed)
}

func exitFor(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

// oauthFor finds the named HTTP server and builds its OAuth helper.
func oauthFor(o mcpOpts, home string, st streams, verb string) (*mcp.OAuth, mcp.Entry, int) {
	if len(o.positional) != 1 {
		fmt.Fprintf(st.Err, "koine mcp %s: needs exactly one server NAME\n", verb)
		return nil, mcp.Entry{}, 2
	}
	cfg := mcp.Load(o.cwd, home, mcpTrusted(o, home))
	for _, e := range cfg.Servers {
		if e.Name != o.positional[0] {
			continue
		}
		if !e.Config.IsHTTP() {
			fmt.Fprintf(st.Err, "koine mcp %s: %s is a stdio server; OAuth applies to HTTP servers\n", verb, e.Name)
			return nil, e, 1
		}
		store := &mcp.TokenStore{Path: mcp.AuthFile(home)}
		res := mcp.Resolver{Dir: o.cwd}
		url, err := res.Resolve(context.Background(), e.Config.URL)
		if err != nil {
			fmt.Fprintf(st.Err, "koine mcp %s: url: %v\n", verb, err)
			return nil, e, 1
		}
		return mcp.NewOAuth(e.Name, url, e.Config.OAuth, store, &http.Client{Timeout: 30 * time.Second}, res), e, 0
	}
	fmt.Fprintf(st.Err, "koine mcp %s: no server named %q (project servers need a trusted folder)\n", verb, o.positional[0])
	return nil, mcp.Entry{}, 1
}

func mcpLogin(o mcpOpts, home string, st streams) int {
	oa, e, code := oauthFor(o, home, st, "login")
	if oa == nil {
		return code
	}
	if hasAuthHeader(e.Config.Headers) {
		fmt.Fprintf(st.Err, "koine mcp login: %s sends its own Authorization header, so OAuth is not used\n", e.Name)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := oa.Login(ctx, mcp.LoginOptions{Notify: func(s string) { fmt.Fprintln(st.Err, s) }, Open: mcpOpenBrowser})
	if err != nil {
		fmt.Fprintln(st.Err, "koine mcp login:", err)
		return 1
	}
	fmt.Fprintf(st.Out, "signed in to %s\n", e.Name)
	return 0
}

// mcpOpenBrowser starts the user's browser for a sign-in; tests replace it.
var mcpOpenBrowser = mcp.OpenBrowser

func hasAuthHeader(h map[string]string) bool {
	for k := range h {
		if strings.EqualFold(k, "Authorization") {
			return true
		}
	}
	return false
}

func mcpLogout(o mcpOpts, home string, st streams) int {
	if len(o.positional) != 1 {
		fmt.Fprintln(st.Err, "koine mcp logout: needs exactly one server NAME")
		return 2
	}
	store := &mcp.TokenStore{Path: mcp.AuthFile(home)}
	gone, err := store.Delete(o.positional[0])
	switch {
	case err != nil:
		fmt.Fprintln(st.Err, "koine mcp logout:", err)
		return 1
	case !gone:
		fmt.Fprintf(st.Out, "%s was not signed in\n", o.positional[0])
	default:
		fmt.Fprintf(st.Out, "signed out of %s\n", o.positional[0])
	}
	return 0
}
