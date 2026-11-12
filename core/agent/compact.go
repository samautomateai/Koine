package agent

import (
	"context"
	"errors"
	"fmt"

	"koine/core/compact"
	"koine/core/events"
	"koine/core/llm"
)

// Default compaction settings, as in Pi: leave 16384 tokens for the answer and keep about 20000 tokens of recent conversation.
const (
	DefaultContextWindow = 200000
	DefaultReserve       = 16384
	DefaultKeepRecent    = 20000
)

// CompactResult says what a compaction did.
type CompactResult struct {
	Summary      string
	TokensBefore int
	TokensAfter  int
	Dropped      int // messages replaced by the summary
	// FirstKeptEntryID is the session entry the compaction kept first ("" without a session).
	FirstKeptEntryID string
}

// ErrNothingToCompact is returned when the conversation is too short to have a part worth summarising.
var ErrNothingToCompact = errors.New("nothing to compact yet")

// ErrCompactCancelled is returned when an extension cancelled the compaction (session_before_compact).
var ErrCompactCancelled = errors.New("compaction cancelled by an extension")

// Compact replaces the older part of the history with a model-written summary, keeping about KeepRecent tokens of recent messages.
// instructions (optional) steers what the summary focuses on. OnCompact, if set, is told the old index of the first kept message.
func (a *Agent) Compact(ctx context.Context, instructions string) (CompactResult, error) {
	return a.CompactFor(ctx, instructions, ReasonManual, nil)
}

// CompactFor is Compact that reports itself to emit as Pi's compaction_start and compaction_end events (and summarization_retry_* ones when
// the model call is retried). reason is manual, threshold or overflow; after an overflow the agent asks the model again, which compaction_end
// says in willRetry. Nothing is emitted when there is nothing to compact.
func (a *Agent) CompactFor(ctx context.Context, instructions, reason string, emit func(Event)) (CompactResult, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	keep := a.KeepRecent
	if keep <= 0 {
		keep = DefaultKeepRecent
	}
	cut := compact.CutPoint(a.History, keep)
	if cut == 0 {
		return CompactResult{}, ErrNothingToCompact
	}
	gate := map[string]any{"reason": reason, "customInstructions": instructions, "willRetry": reason == ReasonOverflow}
	if a.dispatch(ctx, events.SessionBeforeCompact, gate).Cancel {
		return CompactResult{}, ErrCompactCancelled
	}
	prev, start := "", 0
	if t, ok := compact.SummaryText(a.History[0]); ok {
		prev, start = t, 1
	}
	emit(Event{Type: EvCompactionStart, Data: Data("reason", reason)})
	var summary string
	err := Summarizing(ctx, emit, "compaction", reason, func(c context.Context) (e error) {
		summary, e = compact.Summarize(c, a.Provider, a.Model, prev, a.History[start:cut], instructions)
		return e
	})
	if err != nil {
		emit(Event{Type: EvCompactionEnd, Data: Data("reason", reason, "aborted", ctx.Err() != nil, "willRetry", false, "errorMessage", err.Error())})
		return CompactResult{}, fmt.Errorf("compaction failed: %w", err)
	}
	res := CompactResult{Summary: summary, TokensBefore: compact.EstimateTokens(a.History), Dropped: cut}
	if a.OnCompact != nil {
		a.OnCompact(cut, summary, res.TokensBefore)
	}
	if a.KeptEntryID != nil {
		res.FirstKeptEntryID = a.KeptEntryID()
	}
	a.History = append([]llm.Message{compact.SummaryMessage(summary)}, a.History[cut:]...)
	a.lastCtx, a.ctxAt = 0, 0 // the provider's count no longer matches the history
	res.TokensAfter = compact.EstimateTokens(a.History)
	a.dispatch(ctx, events.SessionCompact, map[string]any{"reason": reason, "willRetry": reason == ReasonOverflow, "fromExtension": false,
		"compactionEntry": map[string]any{"summary": summary, "firstKeptEntryId": res.FirstKeptEntryID, "tokensBefore": res.TokensBefore}})
	emit(Event{Type: EvCompactionEnd, Data: Data("reason", reason, "aborted", false, "willRetry", reason == ReasonOverflow,
		"result", Data("summary", summary, "firstKeptEntryId", res.FirstKeptEntryID, "tokensBefore", res.TokensBefore,
			"estimatedTokensAfter", res.TokensAfter))})
	return res, nil
}

// needsCompaction reports whether history plus the system prompt no longer leaves Reserve tokens of room in the window.
func (a *Agent) needsCompaction(system string) bool {
	if !a.AutoCompact {
		return false
	}
	reserve := a.Reserve
	if reserve <= 0 {
		reserve = DefaultReserve
	}
	return a.contextTokens(system) > a.window()-reserve
}

func compactNotice(r CompactResult) string {
	return fmt.Sprintf("compacted: %d messages summarised, about %dk → %dk tokens", r.Dropped, r.TokensBefore/1000, r.TokensAfter/1000)
}
