package agent

import (
	"context"
	"errors"
	"fmt"

	"koine/core/compact"
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
}

// ErrNothingToCompact is returned when the conversation is too short to have a part worth summarising.
var ErrNothingToCompact = errors.New("nothing to compact yet")

// Compact replaces the older part of the history with a model-written summary, keeping about KeepRecent tokens of recent messages.
// instructions (optional) steers what the summary focuses on. OnCompact, if set, is told the old index of the first kept message.
func (a *Agent) Compact(ctx context.Context, instructions string) (CompactResult, error) {
	keep := a.KeepRecent
	if keep <= 0 {
		keep = DefaultKeepRecent
	}
	cut := compact.CutPoint(a.History, keep)
	if cut == 0 {
		return CompactResult{}, ErrNothingToCompact
	}
	prev, start := "", 0
	if t, ok := compact.SummaryText(a.History[0]); ok {
		prev, start = t, 1
	}
	summary, err := compact.Summarize(ctx, a.Provider, a.Model, prev, a.History[start:cut], instructions)
	if err != nil {
		return CompactResult{}, fmt.Errorf("compaction failed: %w", err)
	}
	res := CompactResult{Summary: summary, TokensBefore: compact.EstimateTokens(a.History), Dropped: cut}
	if a.OnCompact != nil {
		a.OnCompact(cut, summary, res.TokensBefore)
	}
	a.History = append([]llm.Message{compact.SummaryMessage(summary)}, a.History[cut:]...)
	res.TokensAfter = compact.EstimateTokens(a.History)
	return res, nil
}

// needsCompaction reports whether history plus the system prompt no longer leaves Reserve tokens of room in the window.
func (a *Agent) needsCompaction(system string) bool {
	if !a.AutoCompact || a.ContextWindow <= 0 {
		return false
	}
	reserve := a.Reserve
	if reserve <= 0 {
		reserve = DefaultReserve
	}
	return compact.EstimateText(system)+compact.EstimateTokens(a.History) > a.ContextWindow-reserve
}

func compactNotice(r CompactResult) string {
	return fmt.Sprintf("compacted: %d messages summarised, about %dk → %dk tokens", r.Dropped, r.TokensBefore/1000, r.TokensAfter/1000)
}
