package main

import (
	"context"
	"io"
	"testing"
	"time"

	"koine/core/rpcclient"
)

// TestRPCClientDrivesTheServer connects core/rpcclient, the library the Bubble Tea and Wails frontends use, to the real `--mode rpc`
// server and checks that a prompt produces the transcript those frontends would show (REQ-FE-003).
func TestRPCClientDrivesTheServer(t *testing.T) {
	api := newGateAPI(t)
	isolate(t, api.URL)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"-C", t.TempDir(), "--no-session", "--trust", "--mode", "rpc"}, streams{In: inR, Out: outW, Err: io.Discard})
		_ = outW.Close()
	}()
	c := rpcclient.NewClient(outR, inW)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tr := rpcclient.NewTranscript()
	if d, err := c.Prompt(ctx, "hello there", ""); err != nil || d != "started" {
		t.Fatalf("prompt: %q %v", d, err)
	}
	for e := range c.Events() {
		tr.Apply(e)
		if e.Type == "agent_settled" {
			break
		}
	}
	if tr.Busy || len(tr.Entries) != 2 || tr.Entries[0].Kind != rpcclient.KindUser || tr.Entries[0].Text != "hello there" ||
		tr.Entries[1].Kind != rpcclient.KindAssistant || tr.Entries[1].Text != "ok" {
		t.Errorf("busy=%v entries=%+v", tr.Busy, tr.Entries)
	}
	st, err := c.GetState(ctx)
	if err != nil || st.MessageCount != 2 || st.Model == nil || st.Model.ID == "" {
		t.Errorf("state: %+v %v", st, err)
	}
	if _, err := c.GetCommands(ctx); err != nil {
		t.Errorf("commands: %v", err)
	}
	if err := c.Abort(ctx); err != nil {
		t.Errorf("abort: %v", err)
	}

	_ = c.Close()
	go func() {
		for range c.Events() { // drain until the server's output ends
		}
	}()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("server did not stop at the end of its input")
	}
}
