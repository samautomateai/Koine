package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Resolver turns configuration values into strings: ${VAR} and ${VAR:-default} are replaced from the environment, and a value that starts
// with "!" is a command whose output (without trailing newlines) is the value, as in Pi: the command must be the entire value.
type Resolver struct {
	Getenv func(string) string // defaults to os.Getenv
	// Run executes a "!command". It defaults to the platform shell with a ten second limit; tests replace it.
	Run func(ctx context.Context, command, dir string) (string, error)
	Dir string // working directory of commands
}

var varRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// Resolve returns the value of one setting. An unset variable without a default is an error, so a missing token never silently becomes "".
func (r Resolver) Resolve(ctx context.Context, v string) (string, error) {
	if cmd, ok := strings.CutPrefix(v, "!"); ok {
		run := r.Run
		if run == nil {
			run = runShell
		}
		out, err := run(ctx, cmd, r.Dir)
		if err != nil {
			return "", fmt.Errorf("command %q: %w", cmd, err)
		}
		return strings.TrimRight(out, "\r\n"), nil
	}
	get := r.Getenv
	if get == nil {
		get = os.Getenv
	}
	var missing []string
	out := varRE.ReplaceAllStringFunc(v, func(m string) string {
		sub := varRE.FindStringSubmatch(m)
		if val := get(sub[1]); val != "" {
			return val
		}
		if strings.Contains(m, ":-") {
			return sub[2]
		}
		missing = append(missing, sub[1])
		return ""
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable %s is not set", strings.Join(missing, ", "))
	}
	return out, nil
}

// ResolveMap resolves every value of a map.
func (r Resolver) ResolveMap(ctx context.Context, m map[string]string) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		val, err := r.Resolve(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

func runShell(ctx context.Context, command, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}
