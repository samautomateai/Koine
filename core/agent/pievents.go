package agent

import (
	"context"
	"fmt"
	"time"

	"koine/core/llm"
)

// The event types below carry Pi's names and, in Event.Data, Pi's field names (https://pi.dev/docs/latest/json), so `--mode json` can write
// them as they are and the TUI, the server and the desktop app can render them without a second translation.
const (
	EvQueueUpdate        = "queue_update"           // steering, followUp: the complete queues
	EvEntryAppended      = "entry_appended"         // entry: a line just written to the session file
	EvSessionInfoChanged = "session_info_changed"   // name (absent when cleared)
	EvThinkingChanged    = "thinking_level_changed" // level
	EvCompactionStart    = "compaction_start"       // reason: manual | threshold | overflow
	EvCompactionEnd      = "compaction_end"         // reason, result, aborted, willRetry, errorMessage
	EvRetryStart         = "auto_retry_start"       // attempt, maxAttempts, delayMs, errorMessage
	EvRetryEnd           = "auto_retry_end"         // success, attempt, finalError
	EvSumRetryScheduled  = "summarization_retry_scheduled"
	EvSumRetryStart      = "summarization_retry_attempt_start" // source: compaction | branchSummary, reason
	EvSumRetryFinished   = "summarization_retry_finished"
	EvToolUpdate         = "tool_execution_update" // Text is the partial output of the tool named in Tool
)

// Compaction reasons, as Pi names them.
const (
	ReasonManual    = "manual"
	ReasonThreshold = "threshold"
	ReasonOverflow  = "overflow"
)

// Data builds the Data of an event from key, value pairs.
func Data(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func millis(d time.Duration) int64 { return d.Milliseconds() }

// complete asks the provider, streaming when it can, and reports the response as it arrives (text_delta, thinking_delta, tool_delta) and the
// retries it makes as Pi's auto_retry_start and auto_retry_end events (and as a notice).
func (a *Agent) complete(ctx context.Context, req llm.Request, emit func(Event)) (*llm.Response, error) {
	if a.Warm != nil {
		a.Warm.Busy() // a real request is going out: a pending cache refresh is pointless
	}
	retries := 0
	ctx = llm.WithRetryNotify(ctx, func(n llm.RetryNotice) {
		retries = n.Attempt
		emit(Event{Type: "notice", Text: fmt.Sprintf("retrying in %s (%d of %d): %v", n.Delay.Round(100*time.Millisecond), n.Attempt, n.Of, n.Err)})
		emit(Event{Type: EvRetryStart, Data: Data("attempt", n.Attempt, "maxAttempts", n.Of, "delayMs", millis(n.Delay), "errorMessage", n.Err.Error())})
	})
	resp, err := llm.Stream(ctx, a.Provider, req, func(e llm.StreamEvent) {
		switch e.Type {
		case llm.EvTextDelta:
			emit(Event{Type: "text_delta", Text: e.Delta})
		case llm.EvThinkingDelta:
			if !a.HideThinking {
				emit(Event{Type: "thinking_delta", Text: e.Delta})
			}
		case llm.EvToolStart, llm.EvToolDelta: // the first tool_delta of a call has no text: it announces the call
			emit(Event{Type: "tool_delta", Tool: e.Name, CallID: e.ID, Text: e.Delta})
		}
	})
	if retries > 0 {
		d := Data("success", err == nil, "attempt", retries)
		if err != nil {
			d["finalError"] = err.Error()
		}
		emit(Event{Type: EvRetryEnd, Data: d})
	}
	return resp, err
}

// Summarizing runs one summarisation (fn makes the provider calls with the context it is given) and reports the retries its calls make as
// summarization_retry_* events. source is "compaction" or "branchSummary"; reason is the compaction reason ("" for a branch summary).
func Summarizing(ctx context.Context, emit func(Event), source, reason string, fn func(ctx context.Context) error) error {
	if emit == nil {
		emit = func(Event) {}
	}
	retried := false
	ctx = llm.WithRetryNotify(ctx, func(n llm.RetryNotice) {
		retried = true
		emit(Event{Type: EvSumRetryScheduled, Data: Data("attempt", n.Attempt, "maxAttempts", n.Of, "delayMs", millis(n.Delay), "errorMessage", n.Err.Error())})
	})
	ctx = llm.WithRetryResume(ctx, func(int) {
		d := Data("source", source)
		if reason != "" {
			d["reason"] = reason
		}
		emit(Event{Type: EvSumRetryStart, Data: d})
	})
	err := fn(ctx)
	if retried {
		emit(Event{Type: EvSumRetryFinished, Data: Data()})
	}
	return err
}
