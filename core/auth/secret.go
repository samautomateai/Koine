package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefaultCommandTimeout bounds a "!command" secret. Pi does not document its limit.
const DefaultCommandTimeout = 10 * time.Second

// Resolver turns a configured secret into its value, as Pi's models.json and auth.json do:
//
//	!command   run the command with the shell and use its trimmed output
//	$NAME      the environment variable NAME ("${NAME}" too, anywhere in the text)
//	$$ and $!  a literal "$" and a literal "!"
//	anything else is used as written
//
// A Resolver must only be given values from user-level files (auth.json, the agent directory's models.json) or from a trusted project:
// a command runs with the user's rights.
type Resolver struct {
	Getenv  func(string) string                                   // default os.Getenv
	Run     func(ctx context.Context, cmd string) (string, error) // default: the shell; replaced in tests
	Timeout time.Duration                                         // default DefaultCommandTimeout
	// Burst is how long an uncached command's output is reused. Looking up which models have credentials resolves the same command many
	// times in a moment; a token command still runs afresh for each request. Zero = never reuse.
	Burst time.Duration

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	val string
	err error
	at  time.Time
}

func (r *Resolver) getenv(k string) string {
	if r.Getenv != nil {
		return r.Getenv(k)
	}
	return os.Getenv(k)
}

// Resolve returns the value of v. With cache, a command is run once for the life of the Resolver and its result, failure included, is
// kept (Pi caches auth.json commands until it restarts); without it the command runs on every call (Pi does not cache models.json ones).
func (r *Resolver) Resolve(v string, cache bool) (string, error) {
	cmd, isCmd := strings.CutPrefix(v, "!")
	if !isCmd {
		return r.expand(v)
	}
	cmd = strings.TrimSpace(cmd)
	key := fmt.Sprint(cache) + cmd // the same command may be wanted both forever and fresh
	r.mu.Lock()
	if c, ok := r.cache[key]; ok && (cache || time.Since(c.at) < r.Burst) {
		r.mu.Unlock()
		return c.val, c.err
	}
	r.mu.Unlock()
	val, err := r.command(cmd)
	if cache || r.Burst > 0 {
		r.mu.Lock()
		if r.cache == nil {
			r.cache = map[string]cached{}
		}
		r.cache[key] = cached{val, err, time.Now()}
		r.mu.Unlock()
	}
	return val, err
}

func (r *Resolver) command(cmd string) (string, error) {
	if cmd == "" {
		return "", errors.New("empty secret command")
	}
	d := r.Timeout
	if d <= 0 {
		d = DefaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	run := r.Run
	if run == nil {
		run = shell
	}
	out, err := run(ctx, cmd)
	if ctx.Err() != nil {
		return "", fmt.Errorf("secret command timed out after %s", d)
	}
	if err != nil {
		return "", fmt.Errorf("secret command failed: %w", err)
	}
	if out = strings.TrimSpace(out); out == "" {
		return "", errors.New("secret command printed nothing")
	}
	return out, nil
}

func shell(ctx context.Context, cmd string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", cmd)
	}
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, firstLine(msg))
		}
		return "", err
	}
	return out.String(), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}

// expand substitutes $NAME and ${NAME}; an unset variable is an error, since an empty key would only fail later and more obscurely.
func (r *Resolver) expand(v string) (string, error) {
	if !strings.Contains(v, "$") {
		return v, nil
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '$' || i+1 >= len(v) {
			b.WriteByte(c)
			continue
		}
		switch n := v[i+1]; {
		case n == '$' || n == '!':
			b.WriteByte(n)
			i++
		case n == '{':
			end := strings.IndexByte(v[i:], '}')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			val, err := r.variable(v[i+2 : i+end])
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			i += end
		case isNameStart(n):
			j := i + 1
			for j < len(v) && isNameChar(v[j]) {
				j++
			}
			val, err := r.variable(v[i+1 : j])
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}

func (r *Resolver) variable(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty variable name in ${}")
	}
	val := r.getenv(name)
	if val == "" {
		return "", fmt.Errorf("environment variable %s is not set", name)
	}
	return val, nil
}

func isNameStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isNameChar(c byte) bool  { return isNameStart(c) || (c >= '0' && c <= '9') }
