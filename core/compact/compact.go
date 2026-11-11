// Package compact shrinks a conversation that is outgrowing the model's context window: it picks a cut point that never separates a tool
// result from its call, asks the model for a structured summary of everything before it, and tells the caller what to keep.
package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"koine/core/llm"
)

// SummaryPrefix starts the user message that stands in for the summarised part of the conversation.
const SummaryPrefix = "Summary of the earlier conversation:\n"

// SummaryMessage wraps a summary as the message that replaces the compacted history.
func SummaryMessage(summary string) llm.Message {
	return llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: SummaryPrefix + summary}}}
}

// SummaryText returns the summary held by m, if m is a summary message.
func SummaryText(m llm.Message) (string, bool) {
	if m.Role == "user" && len(m.Content) == 1 && strings.HasPrefix(m.Content[0].Text, SummaryPrefix) {
		return strings.TrimPrefix(m.Content[0].Text, SummaryPrefix), true
	}
	return "", false
}

// EstimateText is a cheap token estimate: about four characters per token.
func EstimateText(s string) int { return (len(s) + 3) / 4 }

func estimateMessage(m llm.Message) int {
	n := 4
	for _, b := range m.Content {
		n += EstimateText(b.Text) + EstimateText(b.Content) + EstimateText(b.Name) + EstimateText(string(b.Input))
	}
	return n
}

// EstimateTokens estimates the tokens msgs take up.
func EstimateTokens(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += estimateMessage(m)
	}
	return n
}

func isToolResult(m llm.Message) bool {
	return m.Role == "user" && len(m.Content) > 0 && m.Content[0].Type == "tool_result"
}

// CutPoint returns the index of the first message to keep: the latest valid cut that still keeps at least keepRecent tokens. A cut is
// valid at any message except a tool-result message, so a result never loses its call. It returns 0 when there is nothing to summarise.
func CutPoint(msgs []llm.Message, keepRecent int) int {
	kept := 0
	for i := len(msgs) - 1; i >= 1; i-- {
		kept += estimateMessage(msgs[i])
		if kept >= keepRecent && !isToolResult(msgs[i]) {
			return i
		}
	}
	return 0
}

// Transcript renders msgs as plain text for the summariser. Long tool output is shortened.
func Transcript(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Content {
			switch blk.Type {
			case "text":
				who := "User"
				if m.Role == "assistant" {
					who = "Assistant"
				}
				fmt.Fprintf(&b, "[%s]: %s\n", who, blk.Text)
			case "tool_use":
				fmt.Fprintf(&b, "[Tool call]: %s(%s)\n", blk.Name, clip(string(blk.Input), 500))
			case "tool_result":
				tag := "Tool result"
				if blk.IsError {
					tag = "Tool error"
				}
				fmt.Fprintf(&b, "[%s]: %s\n", tag, clip(blk.Content, 1500))
			}
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… [truncated]"
}

// FileOps lists the paths the model read and the paths it changed, from the tool calls in msgs.
func FileOps(msgs []llm.Message) (read, modified []string) {
	r, w := map[string]bool{}, map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type != "tool_use" {
				continue
			}
			var in struct {
				Path     string `json:"path"`
				FilePath string `json:"file_path"`
			}
			_ = json.Unmarshal(b.Input, &in)
			p := in.Path
			if p == "" {
				p = in.FilePath
			}
			switch b.Name {
			case "read":
				r[p] = p != ""
			case "write", "edit":
				w[p] = p != ""
			}
		}
	}
	for p, ok := range r {
		if ok && !w[p] {
			read = append(read, p)
		}
	}
	for p, ok := range w {
		if ok {
			modified = append(modified, p)
		}
	}
	sort.Strings(read)
	sort.Strings(modified)
	return read, modified
}

const system = `You summarise a coding-agent conversation so the agent can continue the work from the summary alone.
Do not continue the conversation and do not answer questions in it; output only the summary.`

const format = `Write the summary in exactly this structure, keeping each section short and concrete:

## Goal
## Constraints and preferences
## Progress
### Done
### In progress
### Blocked
## Key decisions
## Next steps
## Critical context
(exact file paths, function names, error messages and commands that the next steps depend on)`

// Summarize asks the model for a summary of msgs. prev is the summary of an earlier compaction, to be updated rather than repeated.
func Summarize(ctx context.Context, p llm.Provider, model, prev string, msgs []llm.Message, instructions string) (string, error) {
	var b strings.Builder
	if prev != "" {
		b.WriteString("<previous-summary>\n" + prev + "\n</previous-summary>\n\nUpdate the previous summary with what happened next.\n\n")
	}
	b.WriteString("<conversation>\n" + Transcript(msgs) + "</conversation>\n\n" + format)
	if instructions != "" {
		b.WriteString("\n\nExtra focus requested by the user: " + instructions)
	}
	resp, err := p.Complete(ctx, llm.Request{Model: model, System: system, MaxTokens: 4096,
		Messages: []llm.Message{{Role: "user", Content: []llm.Block{{Type: "text", Text: b.String()}}}}})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, blk := range resp.Content {
		if blk.Type == "text" {
			out.WriteString(blk.Text)
		}
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return "", fmt.Errorf("the model returned an empty summary")
	}
	read, modified := FileOps(msgs)
	if len(read) > 0 {
		s += "\n\n<read-files>\n" + strings.Join(read, "\n") + "\n</read-files>"
	}
	if len(modified) > 0 {
		s += "\n\n<modified-files>\n" + strings.Join(modified, "\n") + "\n</modified-files>"
	}
	return s, nil
}

// IsOverflow reports whether err is a provider's "the conversation does not fit" failure (see llm.IsOverflow for the per-provider wording).
func IsOverflow(err error) bool { return llm.IsOverflow(err) }
