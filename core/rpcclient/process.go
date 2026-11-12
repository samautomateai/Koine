package rpcclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Options say how to start the server.
type Options struct {
	Bin  string   // path of the koine binary; empty means FindBinary()
	Args []string // options before --mode rpc, such as -C DIR or --session-dir DIR
	Dir  string   // working directory of the child
	Env  []string // extra KEY=VALUE pairs added to the environment
}

// Process is a Client attached to a running `koine --mode rpc` child.
type Process struct {
	*Client
	cmd  *exec.Cmd
	exit chan error
}

// FindBinary looks for the koine executable: $KOINE_BIN, then next to the running program, then $PATH.
func FindBinary() (string, error) {
	if p := os.Getenv("KOINE_BIN"); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		name := "koine"
		if os.PathSeparator == '\\' {
			name += ".exe"
		}
		if p := filepath.Join(filepath.Dir(exe), name); fileExists(p) {
			return p, nil
		}
	}
	p, err := exec.LookPath("koine")
	if err != nil {
		return "", errors.New("koine binary not found: set KOINE_BIN, or put koine next to this program or on PATH")
	}
	return p, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Start runs the server as a child process. Lines it writes to standard error (notices, warnings, extension output) arrive on Events as
// {"type":"stderr","text":...} records, so a frontend can show them. ctx ends the child.
func Start(ctx context.Context, o Options) (*Process, error) {
	bin := o.Bin
	if bin == "" {
		var err error
		if bin, err = FindBinary(); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, bin, append(append([]string{}, o.Args...), "--mode", "rpc")...)
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(), o.Env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	p := &Process{cmd: cmd, exit: make(chan error, 1)}
	p.Client = newClient(out, in, 1)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		defer p.extra.Done()
		sc := bufio.NewScanner(errPipe)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			b, _ := json.Marshal(map[string]string{"type": "stderr", "text": sc.Text()})
			p.emit(Event{Type: "stderr", Raw: b})
		}
	}()
	go func() {
		<-p.Done()
		<-stderrDone // Wait closes the pipes, so it must come after the reads
		p.exit <- cmd.Wait()
	}()
	return p, nil
}

// Close closes the server's input, waits a few seconds for it to finish the commands in flight, then kills it. It returns the child's exit
// error, if any.
func (p *Process) Close() error {
	_ = p.Client.Close()
	select {
	case err := <-p.exit:
		p.exit <- err
		return exitErr(err)
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		return exitErr(<-p.exit)
	}
}

func exitErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && !ee.Exited() {
		return nil // ended by a signal: we killed it after the grace period
	}
	return err
}
