// Package tui is a dependency-free terminal UI (ANSI line mode). A richer
// Bubble Tea frontend lives in ../../tui-bubbletea and uses the same core.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"koine/core/agent"
	"koine/core/app"
)

const (
	dim   = "\x1b[2m"
	red   = "\x1b[31m"
	cyan  = "\x1b[36m"
	bold  = "\x1b[1m"
	reset = "\x1b[0m"
)

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func Render(e agent.Event) {
	switch e.Type {
	case "text":
		fmt.Println(e.Text)
	case "thinking":
		fmt.Printf("%s%s%s\n", dim, e.Text, reset)
	case "tool_start":
		var args map[string]any
		_ = json.Unmarshal(e.Args, &args)
		var parts []string
		for k, v := range args {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		fmt.Printf("%s⏺ %s(%s)%s\n", cyan, e.Tool, short(strings.Join(parts, " "), 100), reset)
	case "tool_end":
		c := dim
		if e.IsError {
			c = red
		}
		fmt.Printf("%s  ⎿ %s%s\n", c, short(e.Text, 160), reset)
	case "notice":
		fmt.Printf("%s%s%s\n", dim, e.Text, reset)
	case "error":
		fmt.Printf("%serror: %s%s\n", red, e.Text, reset)
	}
}

// choose prints options as a numbered list and reads a number from in. It returns the zero-based index, or -1 when the answer is empty,
// not a valid number or the input ended.
func choose(in *bufio.Reader, out io.Writer, title string, options []string) int {
	_, _ = fmt.Fprintln(out, title)
	for i, o := range options {
		_, _ = fmt.Fprintf(out, "%2d  %s\n", i+1, o)
	}
	_, _ = fmt.Fprint(out, "number (empty to cancel): ")
	line, _ := in.ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(options) {
		return -1
	}
	return n - 1
}

// Run starts the interactive loop. Ctrl-C cancels a running turn; at the prompt it exits.
func Run(ctx context.Context, a *app.App) error {
	info := a.Describe()
	fmt.Printf("%skoine%s  %s · %s\n", bold, reset, info.Model, info.Cwd)
	fmt.Printf("%s%d tools · %d skills · %d agents · %d commands — /help%s\n",
		dim, len(info.Tools), len(info.Skills), len(info.Agents), len(info.Commands), reset)
	for _, w := range a.Warnings {
		fmt.Printf("%swarning: %s%s\n", red, w, reset)
	}
	in := bufio.NewReader(os.Stdin)
	a.Choose = func(title string, options []string) int { return choose(in, os.Stdout, title, options) }
	for {
		fmt.Printf("%s› %s", bold, reset)
		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			return nil
		}
		runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		quit, err := a.HandleInput(runCtx, line, Render)
		interrupted := runCtx.Err() != nil
		stop()
		if interrupted {
			fmt.Printf("%s(interrupted)%s\n", dim, reset)
		} else if err != nil {
			Render(agent.Event{Type: "error", Text: err.Error()})
		}
		if quit {
			return nil
		}
	}
}
