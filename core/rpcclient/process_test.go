package rpcclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHelperServer is not a test: Start runs this test binary as a stand-in for `koine --mode rpc` when KOINE_RPCCLIENT_HELPER is set.
func TestHelperServer(t *testing.T) {
	mode := os.Getenv("KOINE_RPCCLIENT_HELPER")
	if mode == "" {
		t.Skip("helper process")
	}
	fmt.Fprintln(os.Stderr, "koine: error: helper says hello")
	if mode == "exit3" {
		os.Exit(3)
	}
	if mode == "stubborn" { // ignores the end of input
		select {}
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c map[string]any
		_ = json.Unmarshal(sc.Bytes(), &c)
		fmt.Printf("{\"type\":\"response\",\"id\":%q,\"command\":%q,\"success\":true,\"data\":{\"disposition\":\"started\"}}\n", c["id"], c["type"])
		fmt.Println(`{"type":"agent_start"}`)
	}
	os.Exit(0)
}

func helper(mode string) Options {
	return Options{Bin: os.Args[0], Args: []string{"-test.run=^TestHelperServer$", "--"}, Env: []string{"KOINE_RPCCLIENT_HELPER=" + mode}}
}

func TestStartRunsAChildAndForwardsStderr(t *testing.T) {
	p, err := Start(context.Background(), helper("echo"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.Prompt(context.Background(), "hi", "")
	if err != nil || d != "started" {
		t.Fatalf("prompt: %q %v", d, err)
	}
	seen := map[string]bool{}
	timeout := time.After(10 * time.Second)
	for !seen["stderr"] || !seen["agent_start"] {
		select {
		case e := <-p.Events():
			seen[e.Type] = true
			if e.Type == "stderr" {
				var r struct{ Text string }
				_ = e.Decode(&r)
				if r.Text != "koine: error: helper says hello" {
					t.Errorf("stderr text %q", r.Text)
				}
			}
		case <-timeout:
			t.Fatalf("saw only %v", seen)
		}
	}
	if err := p.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestChildExitIsReported(t *testing.T) {
	p, err := Start(context.Background(), helper("exit3"))
	if err != nil {
		t.Fatal(err)
	}
	for range p.Events() {
	}
	if err := p.Close(); err == nil {
		t.Error("exit status 3 should be reported")
	}
}

func TestCloseKillsAServerThatIgnoresTheEndOfInput(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the grace period")
	}
	p, err := Start(context.Background(), helper("stubborn"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("a killed child is not an error: %v", err)
	}
}

func TestStartFailures(t *testing.T) {
	if _, err := Start(context.Background(), Options{Bin: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Error("missing binary should fail")
	}
}

func TestFindBinary(t *testing.T) {
	t.Setenv("KOINE_BIN", "/x/koine")
	if p, err := FindBinary(); err != nil || p != "/x/koine" {
		t.Errorf("KOINE_BIN: %q %v", p, err)
	}
	t.Setenv("KOINE_BIN", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := FindBinary(); err == nil {
		t.Error("nothing to find")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "koine")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if p, err := FindBinary(); err != nil || p != bin {
		t.Errorf("PATH: %q %v", p, err)
	}
}
