package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Target describes the provider a request is about to go to, for TransformMessages.
type Target struct {
	API      string // the wire protocol (APIAnthropic, ...)
	Provider string // the provider id, for rules that belong to one provider (Mistral's tool call ids)
	// SameOrigin says whether the model named by an assistant message's Origin is the model the request goes to. Nil means every
	// message is taken to come from it.
	SameOrigin func(origin string) bool
}

// idRule is what a provider accepts as a tool call id.
type idRule struct {
	max   int  // longest id
	exact bool // the id must be exactly max long and alphanumeric (Mistral)
}

// toolIDRule returns the rule for a target, and false when ids are not sent at all (Gemini matches calls to results by name).
func toolIDRule(t Target) (idRule, bool) {
	switch t.API {
	case APIAnthropic, APIBedrock, APIResponses, APIAzure:
		return idRule{max: 64}, true
	case APICompletions:
		if t.Provider == "mistral" {
			return idRule{max: 9, exact: true}, true
		}
		return idRule{max: 40}, true
	}
	return idRule{}, false
}

func idChar(c byte, alnumOnly bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return !alnumOnly && (c == '_' || c == '-')
}

func (r idRule) valid(id string) bool {
	if id == "" || len(id) > r.max || (r.exact && len(id) != r.max) {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !idChar(id[i], r.exact) {
			return false
		}
	}
	return true
}

// fix returns an id the rule accepts. An id that is already fine is kept; otherwise the result is derived from the old id alone, so the
// same call gets the same id every time the history is converted. salt makes a second attempt differ after a collision.
func (r idRule) fix(old string, salt int) string {
	if r.valid(old) && salt == 0 {
		return old
	}
	sum := sha256.Sum256([]byte(old + "#" + strconv.Itoa(salt)))
	h := hex.EncodeToString(sum[:])
	if r.exact {
		return h[:r.max]
	}
	var b strings.Builder
	for i := 0; i < len(old); i++ {
		if idChar(old[i], false) {
			b.WriteByte(old[i])
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > r.max-9 {
		s = s[:r.max-9]
	}
	return s + "_" + h[:8] // the hash keeps two ids that sanitise alike apart
}

// TransformMessages makes a conversation that one model produced acceptable to another provider, so that the model or provider can be
// changed in the middle of a session. The input is not modified: the session on disk keeps what the original model said.
//
//   - Thinking blocks of a different model cannot be replayed (the signature belongs to the model that wrote it). Readable ones become
//     plain text; redacted ones are dropped. Gemini thought signatures of another model are dropped too.
//   - Tool call ids are rewritten to what the target accepts, consistently in the calls and their results.
//   - A tool result with no matching call is dropped; a call with no result gets an error result, because providers reject both.
//   - Messages left empty by these steps are removed.
//
// Images are handled by StripImages, which needs the model's capabilities.
func TransformMessages(in []Message, t Target) []Message {
	out := make([]Message, 0, len(in))
	for _, m := range in {
		m.Content = append([]Block(nil), m.Content...)
		if m.Role == "assistant" && m.Origin != "" && t.SameOrigin != nil && !t.SameOrigin(m.Origin) {
			m.Content = foreignThinking(m.Content)
		}
		if len(m.Content) > 0 {
			out = append(out, m)
		}
	}
	if rule, ok := toolIDRule(t); ok {
		out = renameToolIDs(out, rule)
	}
	return repairToolPairs(out)
}

// foreignThinking turns readable thinking into text and drops what cannot be shown.
func foreignThinking(bs []Block) []Block {
	out := bs[:0]
	for _, b := range bs {
		b.ThoughtSignature = "" // Gemini's token belongs to the model that wrote it, like a thinking signature
		if b.Type == "thinking" {
			if b.Redacted || strings.TrimSpace(b.Thinking) == "" {
				continue
			}
			b = Block{Type: "text", Text: b.Thinking}
		}
		out = append(out, b)
	}
	return out
}

// renameToolIDs rewrites tool call ids that rule rejects, in the calls and in the results that answer them.
func renameToolIDs(msgs []Message, rule idRule) []Message {
	newID := map[string]string{} // old -> new, for ids that changed
	taken := map[string]string{} // new -> old
	pick := func(old string) string {
		if n, ok := newID[old]; ok {
			return n
		}
		n := rule.fix(old, 0)
		for salt := 1; taken[n] != "" && taken[n] != old; salt++ {
			n = rule.fix(old, salt)
		}
		taken[n], newID[old] = old, n
		return n
	}
	for i, m := range msgs {
		changed := false
		for _, b := range m.Content {
			if (b.Type == "tool_use" && pick(b.ID) != b.ID) || (b.Type == "tool_result" && pick(b.ToolUseID) != b.ToolUseID) {
				changed = true
			}
		}
		if !changed {
			continue
		}
		bs := append([]Block(nil), m.Content...)
		for j := range bs {
			switch bs[j].Type {
			case "tool_use":
				bs[j].ID = pick(bs[j].ID)
			case "tool_result":
				bs[j].ToolUseID = pick(bs[j].ToolUseID)
			}
		}
		msgs[i].Content = bs
	}
	return msgs
}

// repairToolPairs makes every tool call be answered by the user message right after it, and every result answer a call there.
func repairToolPairs(in []Message) []Message {
	out := make([]Message, 0, len(in))
	for i := 0; i < len(in); i++ {
		m := in[i]
		switch m.Role {
		case "assistant":
			out = append(out, m)
			calls := map[string]bool{}
			var order []string
			for _, b := range m.Content {
				if b.Type == "tool_use" {
					calls[b.ID] = true
					order = append(order, b.ID)
				}
			}
			if len(calls) == 0 {
				continue
			}
			var next Message
			if i+1 < len(in) && in[i+1].Role == "user" {
				next = in[i+1]
				i++
			} else {
				next = Message{Role: "user"}
			}
			answered := map[string]bool{}
			var results, rest []Block // providers want the results first in the message
			for _, b := range next.Content {
				switch {
				case b.Type != "tool_result":
					rest = append(rest, b)
				case calls[b.ToolUseID] && !answered[b.ToolUseID]:
					answered[b.ToolUseID] = true
					results = append(results, b)
				}
			}
			for _, id := range order {
				if !answered[id] {
					results = append(results, Block{Type: "tool_result", ToolUseID: id, Content: "Aborted: no result was recorded", IsError: true})
					answered[id] = true
				}
			}
			next.Content = append(results, rest...)
			out = append(out, next)
		default:
			var kept []Block // results that follow no call are dropped
			for _, b := range m.Content {
				if b.Type != "tool_result" {
					kept = append(kept, b)
				}
			}
			if len(kept) > 0 {
				m.Content = kept
				out = append(out, m)
			}
		}
	}
	return out
}
