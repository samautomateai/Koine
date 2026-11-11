package session

import (
	"encoding/json"
	"strings"
	"time"

	"koine/core/llm"
)

// piMessage is the part of Pi's AgentMessage that Koine reads and writes.
type piMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Timestamp  int64           `json:"timestamp,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	Model      string          `json:"model,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
}

type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// AppendMessage stores one conversation message. A user message that carries tool results becomes one toolResult entry per result,
// as Pi writes them. provider and model describe the assistant that produced an assistant message.
func (s *Session) AppendMessage(m llm.Message, provider, model string) error {
	now := time.Now().UnixMilli()
	put := func(pm piMessage) error {
		b, err := json.Marshal(pm)
		if err != nil {
			return err
		}
		return s.appendEntry("message", func(e *Entry) { e.Message = b })
	}
	switch m.Role {
	case "assistant":
		blocks, stop := []piBlock{}, "stop"
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				blocks = append(blocks, piBlock{Type: "text", Text: b.Text})
			case "tool_use":
				stop = "toolUse"
				args := b.Input
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				blocks = append(blocks, piBlock{Type: "toolCall", ID: b.ID, Name: b.Name, Arguments: args})
			}
		}
		c, _ := json.Marshal(blocks)
		return put(piMessage{Role: "assistant", Content: c, Timestamp: now, Provider: provider, Model: model, StopReason: stop})
	default:
		var text []piBlock
		for _, b := range m.Content {
			switch b.Type {
			case "tool_result":
				c, _ := json.Marshal([]piBlock{{Type: "text", Text: b.Content}})
				if err := put(piMessage{Role: "toolResult", Content: c, Timestamp: now, ToolCallID: b.ToolUseID, ToolName: s.toolName(b.ToolUseID),
					IsError: b.IsError}); err != nil {
					return err
				}
			case "text":
				text = append(text, piBlock{Type: "text", Text: b.Text})
			}
		}
		if len(text) == 0 {
			return nil
		}
		c, _ := json.Marshal(text)
		return put(piMessage{Role: "user", Content: c, Timestamp: now})
	}
}

// toolName finds the name of the tool call with the given id among stored assistant messages.
func (s *Session) toolName(id string) string {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].Type != "message" {
			continue
		}
		var pm piMessage
		var blocks []piBlock
		if json.Unmarshal(s.entries[i].Message, &pm) != nil || pm.Role != "assistant" || json.Unmarshal(pm.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "toolCall" && b.ID == id {
				return b.Name
			}
		}
	}
	return ""
}

// Messages rebuilds the conversation on the active branch. The latest compaction on the branch replaces everything before its
// firstKeptEntryId with its summary. Roles and blocks Koine cannot represent (thinking, images, custom) are skipped.
func (s *Session) Messages() []llm.Message {
	path := s.path()
	start, summary := 0, ""
	for i, e := range path {
		if e.Type == "compaction" {
			summary = e.Summary
			start = i + 1
			for j, k := range path {
				if k.ID == e.FirstKeptEntryID {
					start = j
					break
				}
			}
		}
	}
	var out []llm.Message
	if summary != "" {
		out = append(out, llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "Summary of the earlier conversation:\n" + summary}}})
	}
	for i := start; i < len(path); i++ {
		e := path[i]
		if e.Type != "message" {
			continue
		}
		var pm piMessage
		if json.Unmarshal(e.Message, &pm) != nil {
			continue
		}
		switch pm.Role {
		case "user":
			if t := textOf(pm.Content); t != "" {
				out = append(out, llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: t}}})
			}
		case "assistant":
			var blocks []piBlock
			_ = json.Unmarshal(pm.Content, &blocks)
			var cb []llm.Block
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						cb = append(cb, llm.Block{Type: "text", Text: b.Text})
					}
				case "toolCall":
					cb = append(cb, llm.Block{Type: "tool_use", ID: b.ID, Name: b.Name, Input: b.Arguments})
				}
			}
			if len(cb) > 0 {
				out = append(out, llm.Message{Role: "assistant", Content: cb})
			}
		case "toolResult":
			r := llm.Block{Type: "tool_result", ToolUseID: pm.ToolCallID, Content: textOf(pm.Content), IsError: pm.IsError}
			if n := len(out); n > 0 && out[n-1].Role == "user" && len(out[n-1].Content) > 0 && out[n-1].Content[0].Type == "tool_result" {
				out[n-1].Content = append(out[n-1].Content, r)
			} else {
				out = append(out, llm.Message{Role: "user", Content: []llm.Block{r}})
			}
		}
	}
	return out
}

// textOf joins the text of a content value that is either a string or an array of blocks.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []piBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
