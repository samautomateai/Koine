package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/app"
	"koine/core/llm"
)

func capture(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan string)
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, r); done <- b.String() }()
	f()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestRender(t *testing.T) {
	out := capture(t, func() {
		Render(agent.Event{Type: "text", Text: "hello"})
		Render(agent.Event{Type: "tool_start", Tool: "bash", Args: json.RawMessage(`{"command":"ls"}`)})
		Render(agent.Event{Type: "tool_end", Text: "ok\n\nmore"})
		Render(agent.Event{Type: "tool_end", Text: "bad", IsError: true})
		Render(agent.Event{Type: "notice", Text: "note"})
		Render(agent.Event{Type: "error", Text: "oops"})
		Render(agent.Event{Type: "done"}) // renders nothing
	})
	for _, want := range []string{"hello", "bash(command=ls)", "ok more", "\x1b[31m", "note", "error: oops"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if short(strings.Repeat("a", 300), 10) != "aaaaaaaaaa…" || short("a \n b", 10) != "a b" {
		t.Error("short")
	}
}

type say string

func (s say) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: []llm.Block{{Type: "text", Text: string(s)}}}, nil
}

func TestRunLoop(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CACHE_HOME", h)
	a, err := app.New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Agent.Provider = say("model says hi")
	a.Warnings = append(a.Warnings, "a startup warning")

	oldIn := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	defer func() { os.Stdin = oldIn }()
	_, _ = w.WriteString("hello\n/skills\n/exit\n")
	_ = w.Close()

	out := capture(t, func() {
		if err := Run(context.Background(), a); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"koine", "a startup warning", "model says hi", "(none)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}

	// EOF without /exit ends cleanly
	r2, w2, _ := os.Pipe()
	os.Stdin = r2
	_ = w2.Close()
	capture(t, func() {
		if err := Run(context.Background(), a); err != nil {
			t.Error(err)
		}
	})
}

func TestChoose(t *testing.T) {
	opts := []string{"a", "b", "c"}
	for _, c := range []struct {
		in   string
		want int
	}{{"2\n", 1}, {"\n", -1}, {"x\n", -1}, {"9\n", -1}, {"0\n", -1}, {"", -1}} {
		var out bytes.Buffer
		if got := choose(bufio.NewReader(strings.NewReader(c.in)), &out, "pick", opts); got != c.want {
			t.Errorf("%q: got %d want %d", c.in, got, c.want)
		}
		if !strings.Contains(out.String(), "pick") || !strings.Contains(out.String(), " 3  c") {
			t.Errorf("menu: %q", out.String())
		}
	}
}
