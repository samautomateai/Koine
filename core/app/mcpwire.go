package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"koine/core/agent"
	"koine/core/mcp"
)

// mcpRuntime is what the app keeps of the MCP client between /reloads.
type mcpRuntime struct {
	mgr *mcp.Manager
	log *mcp.Log
}

// startMCP (re)connects the configured MCP servers and registers their tools in a.Tools (DES-050). User-level servers start as they are; project
// ones only when the folder is trusted, because a server is a program the repository could name. It returns how many project servers were
// left out, for the untrusted-folder warning. Servers that fail are reported as warnings and the others carry on.
func (a *App) startMCP(ctx context.Context) (skipped int) {
	a.closeMCP()
	cfg := mcp.Load(a.Cwd, a.Home, a.Trusted)
	for _, p := range cfg.Problems {
		a.Warnings = append(a.Warnings, "mcp: "+p.String())
	}
	if len(cfg.Servers) == 0 {
		return cfg.Skipped
	}
	m := mcp.NewManager(a.Tools, a.Cwd, a.Home)
	m.Resolver.Getenv = a.getenv
	m.Store = &mcp.TokenStore{Path: mcp.AuthFile(a.Home)}
	if l, err := mcp.OpenLog(mcp.LogFile(a.Home)); err == nil {
		m.Log = l
		a.mcpRT.log = l
	}
	m.Notice = func(s string) {
		if os.Getenv("KOINE_DEBUG") != "" { // late events arrive off the main goroutine, so they are not added to a.Warnings
			fmt.Fprintln(os.Stderr, "mcp:", s)
		}
	}
	a.mcpRT.mgr = m
	m.Start(ctx, cfg.Servers, mcp.ConnectWait)
	for _, s := range m.Statuses() {
		switch s.State {
		case mcp.StateFailed, mcp.StateNeedsLogin:
			a.Warnings = append(a.Warnings, fmt.Sprintf("mcp: server %s: %s", s.Name, s.Err))
		case mcp.StateConnecting:
			a.Info = append(a.Info, fmt.Sprintf("mcp: server %s is still connecting; its tools appear when it is ready", s.Name))
		}
	}
	return cfg.Skipped
}

func (a *App) closeMCP() {
	if a.mcpRT.mgr != nil {
		a.mcpRT.mgr.Close()
	}
	if a.mcpRT.log != nil {
		_ = a.mcpRT.log.Close()
	}
	a.mcpRT = mcpRuntime{}
}

// MCPStatuses describes the MCP servers of this session (empty when none are configured).
func (a *App) MCPStatuses() []mcp.Status {
	if a.mcpRT.mgr == nil {
		return nil
	}
	return a.mcpRT.mgr.Statuses()
}

// mcpCommand handles /mcp: the state of each server, and /mcp reconnect <server>. Signing in and out needs a browser and is done with
// `koine mcp login|logout`.
func (a *App) mcpCommand(ctx context.Context, name, args string, emit func(agent.Event)) bool {
	if name != "mcp" {
		return false
	}
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	verb, rest, _ := strings.Cut(args, " ")
	rest = strings.TrimSpace(rest)
	switch verb {
	case "":
		st := a.MCPStatuses()
		if len(st) == 0 {
			notice("no MCP servers configured (koine mcp add, or edit mcp.json)")
		}
		for _, s := range st {
			line := fmt.Sprintf("%s: %s, %d tools, exposure %s, %s", s.Name, s.State, s.Tools, s.Exposure, s.Source)
			if s.Err != "" {
				line += " — " + s.Err
			}
			notice(line)
		}
	case "reconnect":
		if rest == "" || a.mcpRT.mgr == nil {
			notice("usage: /mcp reconnect <server> (needs a configured server)")
		} else if err := a.mcpRT.mgr.Reconnect(ctx, rest); err != nil {
			notice("mcp: " + err.Error())
		} else {
			notice("mcp: " + rest + " reconnected")
		}
	default:
		notice("usage: /mcp [reconnect <server>]; sign in with `koine mcp login <server>`")
	}
	return true
}
