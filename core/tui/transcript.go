package tui

import (
	"strings"

	"koine/core/llm"
)

// Block is one item of the transcript.
type Block struct {
	Kind  string // user, assistant, thinking, tool, notice, error, banner
	Text  string
	Tool  *ToolCall
	Final bool // false while it is still changing (streamed text, a running tool); such blocks stay in the live area
}

// RenderCtx carries what drawing a block depends on.
type RenderCtx struct {
	Width        int
	Theme        *Theme
	Expanded     bool // tool output expanded
	HideThinking bool
	OutputPad    int // 0 or 1 cell of margin on each side
	CodeIndent   string
	Hyperlinks   bool
}

// RenderBlock draws one block as lines no wider than c.Width, without the blank line that separates blocks.
func RenderBlock(b Block, c RenderCtx) []string {
	th := c.Theme
	pad := strings.Repeat(" ", c.OutputPad)
	w := max(10, c.Width-2*c.OutputPad)
	var lines []string
	switch b.Kind {
	case "user":
		for _, l := range Wrap(b.Text, w-2) {
			lines = append(lines, th.Bg("userMessageBg", " "+Pad(th.Fg("userMessageText", l), w-2)+" "))
		}
	case "assistant":
		lines = Markdown(b.Text, MDOptions{Width: w, Theme: th, CodeIndent: c.CodeIndent, Hyperlinks: c.Hyperlinks})
	case "thinking":
		if c.HideThinking {
			lines = []string{th.Fg("thinkingText", th.Italic("Thinking…"))}
			break
		}
		for _, l := range Wrap(strings.TrimSpace(b.Text), w) {
			lines = append(lines, th.Fg("thinkingText", th.Italic(l)))
		}
	case "tool":
		if b.Tool != nil {
			lines = b.Tool.Render(ToolOptions{Width: w, Theme: th, Expanded: c.Expanded})
		}
	case "notice":
		for _, l := range Wrap(b.Text, w) {
			lines = append(lines, th.Fg("muted", l))
		}
	case "error":
		for _, l := range Wrap("error: "+b.Text, w) {
			lines = append(lines, th.Fg("error", l))
		}
	default: // banner and anything else: lines as given
		for _, l := range strings.Split(b.Text, "\n") {
			lines = append(lines, Truncate(l, w, "…"))
		}
	}
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

// RenderBlocks draws blocks one after another, each preceded by a blank line.
func RenderBlocks(bs []Block, c RenderCtx) []string {
	var out []string
	for _, b := range bs {
		out = append(out, "")
		out = append(out, RenderBlock(b, c)...)
	}
	return out
}

// BlocksFromHistory rebuilds the transcript of a resumed or switched session from its messages.
func BlocksFromHistory(h []llm.Message) []Block {
	var out []Block
	byID := map[string]*ToolCall{}
	for _, m := range h {
		for _, b := range m.Content {
			switch {
			case m.Role == "user" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
				out = append(out, Block{Kind: "user", Text: b.Text, Final: true})
			case m.Role == "assistant" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
				out = append(out, Block{Kind: "assistant", Text: b.Text, Final: true})
			case m.Role == "assistant" && b.Type == "thinking" && b.Thinking != "":
				out = append(out, Block{Kind: "thinking", Text: b.Thinking, Final: true})
			case b.Type == "tool_use":
				tc := &ToolCall{ID: b.ID, Name: b.Name, Args: b.Input}
				byID[b.ID] = tc
				out = append(out, Block{Kind: "tool", Tool: tc, Final: true})
			case b.Type == "tool_result":
				if tc := byID[b.ToolUseID]; tc != nil {
					tc.Result, tc.IsError, tc.Done = b.Content, b.IsError, true
				}
			}
		}
	}
	return out
}

// UserPrompts lists the user's typed messages in order, for the prompt history of a resumed session.
func UserPrompts(h []llm.Message) []string {
	var out []string
	for _, m := range h {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				out = append(out, b.Text)
			}
		}
	}
	return out
}
