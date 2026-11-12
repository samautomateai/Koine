package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
func (a *Agent) finish(ctx context.Context, c *call, emit func(Event), text string, stop bool) {
	c.res.Content, c.res.IsError, c.stop = text, true, stop
	emit(Event{Type: "tool_start", Tool: c.use.Name, CallID: c.use.ID, Args: c.args})
	emit(Event{Type: "tool_end", Tool: c.use.Name, CallID: c.use.ID, Text: text, IsError: true})
	a.executionEnd(ctx, c.use, text, true, nil)
}

func (a *Agent) preflight(ctx context.Context, u llm.Block, emit func(Event)) *call {
	c := &call{use: u, args: u.Input, res: llm.Block{Type: "tool_result", ToolUseID: u.ID}}
	if len(c.args) == 0 {
		c.args = json.RawMessage("{}")
	}
	if ctx.Err() != nil {
		a.finish(ctx, c, emit, errAborted.Error(), false)
		return c
	}
	a.dispatch(ctx, events.ToolExecutionStart, map[string]any{"toolCallId": u.ID, "toolName": u.Name, "args": c.args})
	if t, ok := a.Tools.Get(u.Name); ok { // Pi skips tool_call for an unknown tool and for arguments that do not fit the schema
		args, err := tools.Validate(t.Schema(), c.args)
		if err != nil {
			a.finish(ctx, c, emit, fmt.Sprintf("Invalid arguments for tool %q:\n%v", u.Name, err), false)
			return c
		}
		c.args = args
	}
	d := a.dispatch(ctx, events.ToolCall, map[string]any{"toolName": u.Name, "callId": u.ID, "input": c.args})
	if d.Block {
		a.finish(ctx, c, emit, "Blocked: "+orDefault(d.Reason, "denied by extension"), d.Terminate)
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
			a.dispatch(ctx, events.ToolExecutionUpdate, map[string]any{"toolCallId": u.ID, "toolName": u.Name, "args": c.args,
				"partialResult": resultJSON(partial, nil)})
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
	if len(d.Content) > 0 { // the extension replaced the whole result: its text and its images
		if text, images, ok := contentFromPi(d.Content); ok {
			out, c.res.Images = text, images
		}
	}
	c.res.Content, c.res.IsError, c.stop = out, isErr, d.Terminate
	end := Event{Type: "tool_end", Tool: u.Name, CallID: u.ID, Text: out, IsError: isErr}
	if len(d.Details) > 0 {
		end.Data = Data("details", d.Details)
	}
	emit(end)
	a.executionEnd(ctx, u, out, isErr, d.Details)
}

// executionEnd tells extensions that a tool call is over (Pi's tool_execution_end).
func (a *Agent) executionEnd(ctx context.Context, u llm.Block, text string, isErr bool, details json.RawMessage) {
	a.dispatch(ctx, events.ToolExecutionEnd, map[string]any{"toolCallId": u.ID, "toolName": u.Name, "result": resultJSON(text, details), "isError": isErr})
}

// resultJSON is a tool result as Pi events carry it: {content: [{type: "text", text}], details}.
func resultJSON(text string, details json.RawMessage) map[string]any {
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "details": details}
}

// contentFromPi reads Pi content blocks an extension answered with: the text blocks joined by newlines, and the image blocks.
func contentFromPi(raw json.RawMessage) (string, []llm.Block, bool) {
	var blocks []struct{ Type, Text, Data, MimeType string }
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil, false
	}
	var texts []string
	var images []llm.Block
	for _, b := range blocks {
		switch {
		case b.Type == "text":
			texts = append(texts, b.Text)
		case b.Type == "image" && b.Data != "" && b.MimeType != "":
			images = append(images, llm.ImageBlock(b.MimeType, b.Data))
		}
	}
	return strings.Join(texts, "\n"), images, true
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
