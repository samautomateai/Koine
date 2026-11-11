package agent

import "koine/core/llm"

// maxThinkingOutput is the output limit Koine asks for when thinking is on and no limit is set: thinking tokens count against it, so the
// provider's small default would cut answers short.
const maxThinkingOutput = 32000

// thinkingFor decides what to ask the model for on this call and how many output tokens to allow.
func (a *Agent) thinkingFor() (t *llm.Thinking, maxTokens int) {
	t = a.Thinking.Resolve(a.Catalog, a.Model)
	maxTokens = a.MaxTokens
	if t != nil && t.Level != "off" && maxTokens == 0 {
		maxTokens = maxThinkingOutput
		if m, ok := a.Catalog.Lookup(a.Model); ok && m.MaxOutput > 0 {
			maxTokens = min(maxTokens, m.MaxOutput)
		}
	}
	return t, maxTokens
}

// ThinkingLevel is the level the next request will use for the current model: "" when the model gets no thinking parameters.
func (a *Agent) ThinkingLevel() string {
	if t, _ := a.thinkingFor(); t != nil {
		return t.Level
	}
	return ""
}

// showThinking emits the readable thinking of a response, unless thinking blocks are hidden. The blocks stay in the history either way.
func (a *Agent) showThinking(b llm.Block, emit func(Event)) {
	if !a.HideThinking && !b.Redacted && b.Thinking != "" {
		emit(Event{Type: "thinking", Text: b.Thinking})
	}
}
