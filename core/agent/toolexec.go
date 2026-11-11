package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"koine/core/events"
	"koine/core/llm"
	"koine/core/tools"
)

// Tool execution modes.
const (
	ExecParallel   = "parallel"
	ExecSequential = "sequential"
)

// ExecutionModer is implemented by tools that want to say how they run. A tool that answers "sequential" makes the whole batch of calls
// it is part of run one at a time, as Pi's per-tool executionMode does.
type ExecutionModer interface{ ExecutionMode() string }

// sequential reports whether a batch of calls must run one at a time.
func (a *Agent) sequential(uses []llm.Block) bool {
	if a.ToolExecution == ExecSequential {
		return true
	}
	for _, u := range uses {
		t, ok := a.Tools.Get(u.Name)
		if !ok {
			continue
		}
		if m, ok := t.(ExecutionModer); ok && m.ExecutionMode() == ExecSequential {
			return true // write and edit need no such flag: core/tools holds a per-file mutation queue (TOOL-11)
		}
	}
	return false
}

var errAborted = errors.New("aborted")

// call is one tool call on its way through execTools.
type call struct {
	use  llm.Block
	args json.RawMessage
	res  llm.Block
	run  bool // passed the checks and still has to be executed
	stop bool // its result asks the agent to stop after the batch
}

// execTools answers every tool call of one assistant message and returns the results in the order of the calls. Calls are first checked
// one after another, in order (the tool_call hook may block or rewrite them, and tool_start is announced); the ones that passed then run
// at the same time, unless the batch must be sequential, and tool_end is announced as each finishes. The second result is true when every
// result asked to terminate, in which case the agent stops after this batch.
func (a *Agent) execTools(ctx context.Context, uses []llm.Block, emit func(Event)) ([]llm.Block, bool) {
	calls := make([]*call, len(uses))
	for i, u := range uses {
		calls[i] = a.preflight(ctx, u, emit)
	}
	if a.sequential(uses) {
		for _, c := range calls {
			if c.run {
				a.runCall(ctx, c, emit)
			}
		}
	} else {
		var wg sync.WaitGroup
		for _, c := range calls {
			if c.run {
				wg.Add(1)
				go func() {
					defer wg.Done()
					a.runCall(ctx, c, emit)
				}()
			}
		}
		wg.Wait()
	}
	results := make([]llm.Block, len(calls))
	stop := len(calls) > 0
	for i, c := range calls {
		results[i] = c.res
		stop = stop && c.stop
	}
	return results, stop
}

// finish records the result of a call that never runs and announces it.
func finish(c *call, emit func(Event), text string, stop bool) {
	c.res.Content, c.res.IsError, c.stop = text, true, stop
	emit(Event{Type: "tool_start", Tool: c.use.Name, CallID: c.use.ID, Args: c.args})
	emit(Event{Type: "tool_end", Tool: c.use.Name, CallID: c.use.ID, Text: text, IsError: true})
}

func (a *Agent) preflight(ctx context.Context, u llm.Block, emit func(Event)) *call {
	c := &call{use: u, args: u.Input, res: llm.Block{Type: "tool_result", ToolUseID: u.ID}}
	if len(c.args) == 0 {
		c.args = json.RawMessage("{}")
	}
	if ctx.Err() != nil {
		finish(c, emit, errAborted.Error(), false)
		return c
	}
	d := a.dispatch(ctx, events.ToolCall, map[string]any{"toolName": u.Name, "callId": u.ID, "input": c.args})
	if d.Block {
		finish(c, emit, "Blocked: "+orDefault(d.Reason, "denied by extension"), d.Terminate)
		return c
	}
	if len(d.Args) > 0 {
		c.args = d.Args
	}
	emit(Event{Type: "tool_start", Tool: u.Name, CallID: u.ID, Args: c.args})
	c.run = true
	return c
}

// runCall executes a call that passed preflight, lets the tool_result hook adjust the result and announces it.
func (a *Agent) runCall(ctx context.Context, c *call, emit func(Event)) {
	u := c.use
	var out string
	var err error
	switch t, ok := a.Tools.Get(u.Name); {
	case ctx.Err() != nil:
		err = errAborted
	case !ok:
		err = fmt.Errorf("unknown tool %q", u.Name)
	default:
		tctx := tools.WithUpdates(ctx, func(partial string) {
			emit(Event{Type: EvToolUpdate, Tool: u.Name, CallID: u.ID, Args: c.args, Text: partial})
		})
		out, err = runTool(tools.WithImages(tctx, a.imageSink(&c.res.Images)), t.Run, u.Name, c.args)
	}
	isErr := err != nil
	if isErr && out == "" {
		out = err.Error()
	}
	d := a.dispatch(ctx, events.ToolResult, map[string]any{"toolName": u.Name, "callId": u.ID, "input": c.args, "output": out, "isError": isErr})
	if d.Output != nil {
		out = *d.Output
	}
	if d.IsError != nil {
		isErr = *d.IsError
	}
	c.res.Content, c.res.IsError, c.stop = out, isErr, d.Terminate
	emit(Event{Type: "tool_end", Tool: u.Name, CallID: u.ID, Text: out, IsError: isErr})
}

// runTool runs a tool, turning a panic into an error: a call running on its own goroutine must not take the process down.
func runTool(ctx context.Context, run func(context.Context, json.RawMessage) (string, error), name string, args json.RawMessage) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("tool %s panicked: %v", name, r)
		}
	}()
	return run(ctx, args)
}
