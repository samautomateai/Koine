package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"koine/core/events"
	"koine/core/llm"
	"koine/core/tools"
)

// gate makes tools wait for each other: it counts how many run at once and records the highest.
type gate struct {
	now, peak atomic.Int32
}

func (g *gate) tool(name string, wait time.Duration) *tools.Func {
	return &tools.Func{N: name, D: "d", S: json.RawMessage(`{}`), F: func(context.Context, json.RawMessage) (string, error) {
		n := g.now.Add(1)
		for {
			p := g.peak.Load()
			if n <= p || g.peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(wait)
		g.now.Add(-1)
		return name + " done", nil
	}}
}

func threeCalls(names ...string) *script {
	var uses []llm.Block
	for i, n := range names {
		uses = append(uses, use(string(rune('a'+i)), n, `{}`))
	}
	return &script{turns: [][]llm.Block{uses}}
}

func TestToolsRunConcurrentlyAndResultsKeepSourceOrder(t *testing.T) {
	var g gate
	s := threeCalls("slow", "fast", "mid")
	a := newAgent(s)
	a.Tools.Register(g.tool("slow", 120*time.Millisecond))
	a.Tools.Register(g.tool("fast", 10*time.Millisecond))
	a.Tools.Register(g.tool("mid", 60*time.Millisecond))
	var order []string
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == "tool_start" || e.Type == "tool_end" {
			order = append(order, e.Type+":"+e.Tool)
		}
	}
	// starts in source order, then ends in completion order
	want := "tool_start:slow,tool_start:fast,tool_start:mid,tool_end:fast,tool_end:mid,tool_end:slow"
	if strings.Join(order, ",") != want {
		t.Errorf("events:\n got %s\nwant %s", strings.Join(order, ","), want)
	}
	if g.peak.Load() != 3 {
		t.Errorf("peak concurrency %d, want 3", g.peak.Load())
	}
	res := lastResults(s)
	if len(res) != 3 || res[0].Content != "slow done" || res[1].Content != "fast done" || res[2].Content != "mid done" ||
		res[0].ToolUseID != "a" || res[2].ToolUseID != "c" {
		t.Errorf("results must follow the calls: %+v", res)
	}
}

func TestSequentialModesRunOneAtATime(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a *Agent, g *gate)
		peak  int32
	}{
		{"setting", func(a *Agent, g *gate) { a.ToolExecution = ExecSequential }, 1},
		{"a write call in the batch (the file queue serializes)", func(a *Agent, g *gate) { a.Tools.Register(g.tool("write", 30*time.Millisecond)) }, 3},
		{"a tool that asks to be sequential", func(a *Agent, g *gate) {
			a.Tools.Register(&modalTool{g.tool("write", time.Millisecond), "sequential"})
		}, 1},
		{"a tool that says parallel", func(a *Agent, g *gate) {
			a.Tools.Register(&modalTool{g.tool("write", 30*time.Millisecond), "parallel"})
		}, 3},
	}
	for _, tc := range cases {
		var g gate
		s := threeCalls("one", "two", "write")
		a := newAgent(s)
		a.Tools.Register(g.tool("one", 30*time.Millisecond))
		a.Tools.Register(g.tool("two", 30*time.Millisecond))
		a.Tools.Register(g.tool("write", time.Millisecond))
		tc.setup(a, &g)
		if _, err := collect(a, "go"); err != nil {
			t.Fatal(err)
		}
		if g.peak.Load() != tc.peak {
			t.Errorf("%s: peak concurrency %d, want %d", tc.name, g.peak.Load(), tc.peak)
		}
		if res := lastResults(s); len(res) != 3 || res[2].Content != "write done" {
			t.Errorf("%s: %+v", tc.name, res)
		}
	}
}

type modalTool struct {
	*tools.Func
	mode string
}

func (m *modalTool) ExecutionMode() string { return m.mode }

func TestPreflightIsSequentialAndBlockedCallsDoNotRun(t *testing.T) {
	var g gate
	s := threeCalls("one", "two", "one")
	a := newAgent(s)
	a.Tools.Register(g.tool("one", time.Millisecond))
	a.Tools.Register(g.tool("two", time.Millisecond))
	var hooks []string
	var mu sync.Mutex // result hooks run on the tools' own goroutines
	a.Dispatch = disp(func(n string, p any) events.Decision {
		m, _ := p.(map[string]any)
		mu.Lock()
		defer mu.Unlock()
		switch n {
		case events.ToolCall:
			hooks = append(hooks, "call:"+m["callId"].(string)) // never concurrent: preflight is in order
			if m["callId"] == "b" {
				return events.Decision{Block: true, Reason: "no two"}
			}
		case events.ToolResult:
			hooks = append(hooks, "result:"+m["callId"].(string))
		}
		return events.Decision{}
	})
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(hooks[:3], ","); got != "call:a,call:b,call:c" {
		t.Errorf("hooks %v", hooks)
	}
	res := lastResults(s)
	if res[1].Content != "Blocked: no two" || !res[1].IsError || res[0].IsError || res[2].IsError {
		t.Errorf("results: %+v", res)
	}
	if len(hooks) != 5 { // the blocked call has no result hook
		t.Errorf("hooks %v", hooks)
	}
}

func TestTerminateStopsTheLoopOnlyWhenEveryResultAsksForIt(t *testing.T) {
	run := func(terminate func(id string) events.Decision, onResult bool) (int, []llm.Block) {
		s := &script{turns: [][]llm.Block{{use("a", "echo", `{}`), use("b", "echo", `{}`)}, {txt("after")}}}
		a := newAgent(s)
		a.Dispatch = disp(func(n string, p any) events.Decision {
			m, _ := p.(map[string]any)
			switch {
			case n == events.ToolCall && !onResult:
				d := terminate(m["callId"].(string))
				d.Block = true
				return d
			case n == events.ToolResult && onResult:
				return terminate(m["callId"].(string))
			}
			return events.Decision{}
		})
		evs, err := collect(a, "go")
		if err != nil || evs[len(evs)-1].Type != "done" {
			t.Fatal(err, types(evs))
		}
		return len(s.reqs), a.History[len(a.History)-1].Content
	}
	all := func(string) events.Decision { return events.Decision{Terminate: true} }
	one := func(id string) events.Decision { return events.Decision{Terminate: id == "a"} }
	for _, onResult := range []bool{false, true} {
		if n, res := run(all, onResult); n != 1 || len(res) != 2 {
			t.Errorf("all terminate (onResult=%v): %d requests, %d results", onResult, n, len(res))
		}
		if n, _ := run(one, onResult); n != 2 {
			t.Errorf("mixed batch must go on (onResult=%v): %d requests", onResult, n)
		}
	}
}

func TestToolResultHookCanFlipTheErrorFlagAndPanicsAreErrors(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("a", "boom", `{}`), use("b", "echo", `{}`), use("c", "bad", `{}`)}}}
	a := newAgent(s)
	a.Tools.Register(&tools.Func{N: "bad", D: "d", S: json.RawMessage(`{}`), F: func(context.Context, json.RawMessage) (string, error) {
		panic("oops")
	}})
	yes, no := true, false
	a.Dispatch = disp(func(n string, p any) events.Decision {
		m, _ := p.(map[string]any)
		if n == events.ToolResult {
			switch m["callId"] {
			case "a":
				return events.Decision{IsError: &no}
			case "b":
				return events.Decision{IsError: &yes}
			}
		}
		return events.Decision{}
	})
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	res := lastResults(s)
	if res[0].IsError || res[0].Content != "kaboom" || !res[1].IsError || !res[2].IsError || !strings.Contains(res[2].Content, "panicked: oops") {
		t.Errorf("results: %+v", res)
	}
}

func TestDecisionMergeCarriesTheNewFields(t *testing.T) {
	yes := true
	var d events.Decision
	d.Merge(events.Decision{IsError: &yes, Terminate: true, Messages: json.RawMessage(`[]`)})
	d.Merge(events.Decision{})
	if d.IsError == nil || !*d.IsError || !d.Terminate || string(d.Messages) != `[]` {
		t.Errorf("%+v", d)
	}
}

func TestEventsAreSerializedWhileToolsRunInParallel(t *testing.T) {
	var g gate
	names := []string{"t1", "t2", "t3", "t4", "t5", "t6"}
	s := threeCalls(names...)
	a := newAgent(s)
	for _, n := range names {
		a.Tools.Register(g.tool(n, time.Millisecond))
	}
	var mu sync.Mutex
	inside, overlap := 0, false
	err := a.Run(context.Background(), "go", func(Event) {
		mu.Lock()
		inside++
		overlap = overlap || inside > 1
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		inside--
		mu.Unlock()
	})
	if err != nil || overlap {
		t.Errorf("err %v overlap %v", err, overlap)
	}
}
