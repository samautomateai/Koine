package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DefaultBashTimeout applies when the model gives none. Pi has no default; a hung command would stall a run forever, so Koine keeps one.
const DefaultBashTimeout = 120 * time.Second

func (o Options) shell() Shell {
	return Shell{Path: o.ShellPath, Prefix: o.ShellCommandPrefix, Cwd: o.Cwd, Home: o.Home, Env: o.ShellEnv}
}

func (o Options) bash(ctx context.Context, a json.RawMessage) (string, error) {
	var p struct {
		Command string
		Timeout float64
	}
	if err := json.Unmarshal(a, &p); err != nil {
		return "", err
	}
	timeout := DefaultBashTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout * float64(time.Second))
	}
	res, err := o.shell().Run(ctx, p.Command, timeout, Updates(ctx))
	if err != nil {
		return "", err
	}
	return FormatShellResult(res, timeout)
}

// FormatShellResult turns a result into the tool's text, and into an error when the command failed, timed out or was aborted.
func FormatShellResult(res ShellResult, timeout time.Duration) (string, error) {
	out := res.Output
	if out == "" {
		out = "(no output)"
	}
	switch {
	case res.TimedOut:
		msg := fmt.Sprintf("Command timed out after %g seconds", timeout.Seconds())
		return out + "\n\n" + msg, errors.New(msg)
	case res.Cancelled:
		return out + "\n\nCommand aborted", errors.New("command aborted")
	case res.ExitCode != 0:
		msg := fmt.Sprintf("Command exited with code %d", res.ExitCode)
		return out + "\n\n" + msg, errors.New(msg)
	}
	return out, nil
}
