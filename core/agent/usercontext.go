package agent

import "koine/core/llm"

// AddUserContext puts text into the conversation as a user message without calling the model (the output of the user's !command).
func (a *Agent) AddUserContext(text string) {
	a.add(llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: text}}})
}
