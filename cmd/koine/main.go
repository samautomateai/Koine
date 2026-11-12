package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"koine/compat"
	"koine/core/app"
	"koine/core/server"
	"koine/core/tui"
)

// Set at build time: -ldflags "-X main.version=1.2.3 -X main.piBaseline=1.5.2".
// piBaseline is the upstream Pi release koine's compatibility data is pinned to.
var (
	version    = "dev"
	piBaseline = "1.5.2"
)

func main() {
	app.SetProcessMarkers()
	os.Exit(run(os.Args[1:], stdStreams()))
}

// run is main without the process globals, so it can be tested end to end.
func run(args []string, st streams) int {
	if len(args) > 0 && args[0] == "mcp" { // koine mcp add|remove|list|login|logout (mcp_cmd.go)
		return runMCP(args[1:], st)
	}
	c, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(st.Err, "koine:", err)
		return 2
	}
	switch {
	case c.Help:
		fmt.Fprint(st.Out, usage+extensionFlagHelp(c))
		return 0
	case c.ListModels:
		home, _ := os.UserHomeDir()
		fmt.Fprintln(st.Out, app.ListModels(home, c.ListSearch))
		return 0
	case c.Version:
		fmt.Fprintf(st.Out, "koine %s+pi%s\n", version, piBaseline)
		return 0
	case c.Compat:
		m, err := compat.Load()
		if err != nil {
			fmt.Fprintln(st.Err, "koine:", err)
			return 1
		}
		fmt.Fprint(st.Out, m.Report())
		return 0
	}
	cwd := c.Dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a, err := app.NewWithOptions(ctx, cwd, app.Options{Trust: c.Trust, RunOptions: app.RunOptions{
		Model: c.Model, System: c.System, AppendSystem: c.AppendSystem, Tools: c.Tools, NoTools: c.NoTools, Thinking: c.Thinking,
		SessionDir: c.SessionDir, NoSession: c.NoSession, Continue: c.Continue, SessionRef: c.Session, Fork: c.Fork, Name: c.Name},
		Load: c.loadOptions()})
	if err != nil {
		fmt.Fprintln(st.Err, "koine:", err)
		return 1
	}
	defer a.Close()
	if len(c.UnknownLong) > 0 { // options only extensions can define: now that they are loaded, read the command line again with their types
		types := map[string]string{}
		for _, f := range a.ExtFlags() {
			types[f.Name] = f.Type
		}
		if c, err = parseArgsWith(args, types); err != nil {
			fmt.Fprintln(st.Err, "koine:", err)
			if !a.Trusted && len(c.Extensions) > 0 {
				fmt.Fprintln(st.Err, "koine: extensions from -e do not load in an untrusted folder; start with --trust to use their options")
			}
			return 2
		}
		a.SetExtFlags(c.ExtFlags)
	}

	if c.Resume {
		if !st.InTTY { // piped: just list
			fmt.Fprintln(st.Out, app.ListSessions(a.SessionDir()))
			return 0
		}
		if ok, err := a.PickResume(st.In, st.Out); err != nil {
			fmt.Fprintln(st.Err, "koine:", err)
			return 1
		} else if !ok {
			return 0
		}
	}
	if c.Serve != "" {
		warnAll(a, st)
		fmt.Fprintf(st.Err, "koine serving on http://%s\n", c.Serve)
		s := &server.Server{App: a}
		srv := &http.Server{Addr: c.Serve, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second} // no WriteTimeout: SSE streams
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintln(st.Err, "koine:", err)
			return 1
		}
		return 0
	}
	prompt, images, notes, err := buildPrompt(c, a, st)
	if err != nil {
		fmt.Fprintln(st.Err, "koine:", err)
		return 2
	}
	if prompt == "" && len(images) == 0 && !c.Print && c.Mode != "json" {
		if err := tui.Run(ctx, a); err != nil {
			fmt.Fprintln(st.Err, "koine:", err)
			return 1
		}
		return 0
	}
	if prompt == "" && len(images) == 0 {
		fmt.Fprintln(st.Err, "koine: nothing to do: give a message, pipe text on stdin, or name an @file")
		return 2
	}
	warnAll(a, st)
	for _, n := range notes {
		fmt.Fprintln(st.Err, "koine: note:", n)
	}
	return runPrint(ctx, a, prompt, images, c.Mode, st)
}

// warnAll prints the startup warnings (untrusted code that was not loaded, unusable -e paths and the like) on stderr for the modes that
// have no screen of their own.
func warnAll(a *app.App, st streams) {
	for _, w := range a.Warnings {
		fmt.Fprintln(st.Err, "koine: warning:", w)
	}
}
