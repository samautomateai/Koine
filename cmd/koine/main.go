package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

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

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run is main without the process globals, so it can be tested end to end.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("koine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	prompt := fs.String("p", "", "run one prompt non-interactively and exit")
	serve := fs.String("serve", "", "serve the HTTP/SSE API on this address (e.g. 127.0.0.1:7777)")
	dir := fs.String("C", "", "project directory (default: current)")
	trustFlag := fs.Bool("trust", false, "trust this folder for this run (load its extensions, hooks and providers)")
	showCompat := fs.Bool("compat", false, "print Pi compatibility status (what is verified, inert, unsupported) and exit")
	ver := fs.Bool("version", false, "print version and the Pi release it tracks, then exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ver {
		fmt.Fprintf(os.Stdout, "koine %s+pi%s\n", version, piBaseline)
		return 0
	}
	if *showCompat {
		m, err := compat.Load()
		if err != nil {
			fmt.Fprintln(stderr, "koine:", err)
			return 1
		}
		fmt.Fprint(os.Stdout, m.Report())
		return 0
	}
	cwd := *dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	ctx := context.Background()
	a, err := app.NewWithOptions(ctx, cwd, app.Options{Trust: *trustFlag})
	if err != nil {
		fmt.Fprintln(stderr, "koine:", err)
		return 1
	}
	defer a.Close()

	switch {
	case *serve != "":
		fmt.Fprintf(stderr, "koine serving on http://%s\n", *serve)
		s := &server.Server{App: a}
		if err := http.ListenAndServe(*serve, s.Handler()); err != nil {
			fmt.Fprintln(stderr, "koine:", err)
			return 1
		}
	case *prompt != "":
		if _, err := a.HandleInput(ctx, *prompt, tui.Render); err != nil {
			return 1
		}
	default:
		if err := tui.Run(ctx, a); err != nil {
			fmt.Fprintln(stderr, "koine:", err)
			return 1
		}
	}
	return 0
}
