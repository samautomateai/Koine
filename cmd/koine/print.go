package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"koine/core/agent"
	"koine/core/app"
)

// streams are the process's standard streams, injectable so the command line can be tested end to end.
type streams struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	InTTY  bool
	OutTTY bool
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func stdStreams() streams {
	return streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, InTTY: isTTY(os.Stdin), OutTTY: isTTY(os.Stdout)}
}

// buildPrompt joins piped stdin, @files and the message, in that order, separated by blank lines.
func buildPrompt(c cliArgs, dir string, st streams) (string, error) {
	var parts []string
	if !st.InTTY && st.In != nil {
		b, err := io.ReadAll(st.In)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			parts = append(parts, s)
		}
	}
	files, err := expandFiles(dir, c.Files)
	if err != nil {
		return "", err
	}
	if files != "" {
		parts = append(parts, strings.TrimRight(files, "\n"))
	}
	if m := strings.TrimSpace(strings.Join(c.Message, " ")); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, "\n\n"), nil
}

// runPrint runs one prompt and returns the exit code. In text mode only the final answer (the text after the last tool call) goes to
// stdout; tool activity, notices and errors go to stderr. In json mode stdout carries only Pi's JSON event stream (jsonmode.go).
func runPrint(ctx context.Context, a *app.App, prompt, mode string, st streams) int {
	failed := false
	var final strings.Builder
	var js *jsonStream
	if mode == "json" {
		js = newJSONStream(st.Out, a)
		a.OnMessage = js.Message
		defer func() { a.OnMessage = nil }()
		js.Start()
	}
	emit := func(e agent.Event) {
		if e.Type == "error" {
			failed = true
		}
		if js != nil {
			js.Event(e)
			switch e.Type {
			case "notice":
				fmt.Fprintln(st.Err, e.Text)
			case "error":
				fmt.Fprintln(st.Err, "koine: error:", e.Text)
			}
			return
		}
		switch e.Type {
		case "text":
			final.WriteString(e.Text)
		case "thinking":
			fmt.Fprintln(st.Err, strings.TrimSpace(e.Text))
		case "tool_start":
			if final.Len() > 0 {
				fmt.Fprintln(st.Err, strings.TrimSpace(final.String()))
				final.Reset()
			}
			fmt.Fprintf(st.Err, "⏺ %s\n", e.Tool)
		case "tool_end":
			if e.IsError {
				fmt.Fprintf(st.Err, "  ⎿ error: %s\n", firstLine(e.Text))
			}
		case "notice":
			fmt.Fprintln(st.Err, e.Text)
		case "error":
			fmt.Fprintln(st.Err, "koine: error:", e.Text)
		}
	}
	_, err := a.HandleInput(ctx, prompt, emit)
	if js != nil {
		js.Finish(ctx.Err() != nil)
	}
	if err != nil {
		fmt.Fprintln(st.Err, "koine:", err)
		return 1
	}
	if js == nil && final.Len() > 0 {
		fmt.Fprintln(st.Out, strings.TrimRight(final.String(), "\n"))
	}
	if failed {
		return 1
	}
	return 0
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}
