package app

import (
	"context"
	"fmt"
	"strings"

	"koine/core/agent"
	"koine/core/tools"
)

const userBashUsage = "usage: !<command> runs it and shows the output to the model; !!<command> runs it without telling the model"

// userBash runs the user's own shell command: "!cmd" adds its output to the conversation, "!!cmd" does not. Unlike the model's bash tool it
// has no timeout (the user can interrupt it) and it does not go through tool hooks. It reports whether line was a bash command.
func (a *App) userBash(ctx context.Context, line string, emit func(agent.Event)) bool {
	rest, ok := strings.CutPrefix(line, "!")
	if !ok {
		return false
	}
	hidden := strings.HasPrefix(rest, "!")
	cmd := strings.TrimSpace(strings.TrimPrefix(rest, "!"))
	if cmd == "" {
		emit(agent.Event{Type: "notice", Text: userBashUsage})
		return true
	}
	sh := tools.Shell{Path: a.Cfg.ShellPath, Prefix: a.Cfg.ShellCommandPrefix, Cwd: a.Cwd, Home: a.Home, Env: a.shellEnv}
	res, err := sh.Run(ctx, cmd, 0, nil)
	if err != nil {
		emit(agent.Event{Type: "error", Text: err.Error()})
		return true
	}
	out, status := res.Output, ""
	switch {
	case res.Cancelled:
		status = "cancelled"
	case res.ExitCode != 0:
		status = fmt.Sprintf("exit code %d", res.ExitCode)
	}
	shown := "$ " + cmd + "\n" + out
	if out == "" {
		shown = "$ " + cmd
	}
	if status != "" {
		shown += "\n[" + status + "]"
	}
	emit(agent.Event{Type: "notice", Text: shown})
	if !hidden {
		a.Agent.AddUserContext(userBashContext(cmd, out, status))
		emit(agent.Event{Type: "notice", Text: "(output added to the conversation; use !!<command> to keep it out)"})
	}
	return true
}

// userBashContext is how the model sees a command the user ran.
func userBashContext(cmd, out, status string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ran `%s`\n", cmd)
	if out == "" {
		b.WriteString("(no output)")
	} else {
		b.WriteString("```\n" + out + "\n```")
	}
	if status != "" {
		b.WriteString("\n[" + status + "]")
	}
	return b.String()
}

// toolOptions are the settings the built-in tools read.
func (a *App) toolOptions() tools.Options {
	return tools.Options{Cwd: a.Cwd, Home: a.Home, ShellPath: a.Cfg.ShellPath, ShellCommandPrefix: a.Cfg.ShellCommandPrefix, ShellEnv: a.shellEnv}
}
