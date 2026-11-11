package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"koine/core/compact"
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
	Usage      *piUsage        `json:"usage,omitempty"`
	// ThinkingLevel is the level the response was requested with (Pi's optional thinkingLevel).
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

// piUsage is the usage object Pi stores on assistant messages. cost is left out when the model's price was not known.
type piUsage struct {
	Input       int     `json:"input"`
	Output      int     `json:"output"`
	CacheRead   int     `json:"cacheRead"`
	CacheWrite  int     `json:"cacheWrite"`
	TotalTokens int     `json:"totalTokens"`
	Cost        *piCost `json:"cost,omitempty"`
}

type piCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

func usageToPi(m llm.Message) *piUsage {
	if m.Usage == nil {
		return nil
	}
	u := &piUsage{Input: m.Usage.Input, Output: m.Usage.Output, CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite,
		TotalTokens: m.Usage.Total()}
	if c := m.Cost; c != nil {
		u.Cost = &piCost{Input: c.Input, Output: c.Output, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite, Total: c.Total}
	}
	return u
}

func usageFromPi(pm piMessage, m *llm.Message) {
	u := pm.Usage
	if u == nil {
		return
	}
	m.Usage = &llm.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
	if c := u.Cost; c != nil {
		m.Cost = &llm.Cost{Input: c.Input, Output: c.Output, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite, Total: c.Total}
	}
}

type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Thinking blocks, in Pi's shape: the text (a pointer, because a redacted block has an empty one that must still be written),
	// the provider's opaque replay data, and whether the block is redacted.
	Thinking          *string `json:"thinking,omitempty"`
	ThinkingSignature string  `json:"thinkingSignature,omitempty"`
	Redacted          bool    `json:"redacted,omitempty"`
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
			case "thinking":
				t := b.Thinking
				blocks = append(blocks, piBlock{Type: "thinking", Thinking: &t, ThinkingSignature: b.Signature, Redacted: b.Redacted})
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
		return put(piMessage{Role: "assistant", Content: c, Timestamp: now, Provider: provider, Model: model, StopReason: stop,
			Usage: usageToPi(m), ThinkingLevel: m.ThinkingLevel})
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
// firstKeptEntryId with its summary. Roles and blocks Koine cannot represent (images, custom) are skipped.
func (s *Session) Messages() []llm.Message {
	msgs, _ := s.messagesWithIDs()
	return msgs
}

// messagesWithIDs is Messages plus, for each message, the id of the entry it starts at ("" for a compaction summary).
func (s *Session) messagesWithIDs() ([]llm.Message, []string) {
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
	var ids []string
	if summary != "" {
		out, ids = append(out, compact.SummaryMessage(summary)), append(ids, "")
	}
	for i := start; i < len(path); i++ {
		e := path[i]
		if e.Type != "message" {
			if m, ok := entryMessage(e); ok { // branch summaries and custom messages carry context (tree.go)
				out, ids = append(out, m), append(ids, e.ID)
			}
			continue
		}
		var pm piMessage
		if json.Unmarshal(e.Message, &pm) != nil {
			continue
		}
		switch pm.Role {
		case "user":
			if t := textOf(pm.Content); t != "" {
				out, ids = append(out, llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: t}}}), append(ids, e.ID)
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
				case "thinking":
					if b.Thinking != nil && (*b.Thinking != "" || b.ThinkingSignature != "") {
						cb = append(cb, llm.Block{Type: "thinking", Thinking: *b.Thinking, Signature: b.ThinkingSignature, Redacted: b.Redacted})
					}
				case "toolCall":
					cb = append(cb, llm.Block{Type: "tool_use", ID: b.ID, Name: b.Name, Input: b.Arguments})
				}
			}
			if len(cb) > 0 {
				am := llm.Message{Role: "assistant", Content: cb, ThinkingLevel: pm.ThinkingLevel}
				usageFromPi(pm, &am)
				out, ids = append(out, am), append(ids, e.ID)
			}
		case "toolResult":
			r := llm.Block{Type: "tool_result", ToolUseID: pm.ToolCallID, Content: textOf(pm.Content), IsError: pm.IsError}
			if n := len(out); n > 0 && out[n-1].Role == "user" && len(out[n-1].Content) > 0 && out[n-1].Content[0].Type == "tool_result" {
				out[n-1].Content = append(out[n-1].Content, r)
			} else {
				out, ids = append(out, llm.Message{Role: "user", Content: []llm.Block{r}}), append(ids, e.ID)
			}
		}
	}
	return out, ids
}

// Compact records a compaction: the messages before index firstKept (an index into Messages) are replaced by summary.
func (s *Session) Compact(firstKept int, summary string, tokensBefore int) error {
	_, ids := s.messagesWithIDs()
	if firstKept < 1 || firstKept >= len(ids) || ids[firstKept] == "" {
		return fmt.Errorf("cannot compact at message %d of %d", firstKept, len(ids))
	}
	return s.appendEntry("compaction", func(e *Entry) { e.Summary, e.FirstKeptEntryID, e.TokensBefore = summary, ids[firstKept], tokensBefore })
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
