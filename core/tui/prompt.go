package tui

import "strings"

// promptEdit is a one-line question (App.Prompt, used by /login for API keys). A secret is drawn as bullets, never as the text.
type promptEdit struct {
	question string
	secret   bool
	text     []rune
	done     func(text string, ok bool)
}

type promptReply struct {
	text string
	ok   bool
}

// Prompt asks for a line of text in place of the editor; done gets the text, or ok=false when cancelled.
func (ui *UI) Prompt(question string, secret bool, done func(string, bool)) {
	ui.prompt = &promptEdit{question: question, secret: secret, done: done}
}

func (ui *UI) promptKey(k Key) {
	p, b := ui.prompt, ui.keys
	switch {
	case b.Matches("tui.select.cancel", k):
		ui.prompt = nil
		p.done("", false)
	case b.Matches("tui.input.submit", k):
		ui.prompt = nil
		p.done(strings.TrimSpace(string(p.text)), true)
	case k.Name == "backspace":
		if len(p.text) > 0 {
			p.text = p.text[:len(p.text)-1]
		}
	case k.Text != "":
		p.text = append(p.text, []rune(strings.ReplaceAll(strings.ReplaceAll(k.Text, "\r", ""), "\n", ""))...)
	}
}

// promptView draws the question and the typed text (as bullets for a secret).
func (ui *UI) promptView() []string {
	p := ui.prompt
	shown := string(p.text)
	if p.secret {
		shown = strings.Repeat("•", len(p.text))
	}
	return []string{ui.th.Fg("borderAccent", strings.Repeat("─", max(1, ui.width))),
		" " + ui.th.Fg("accent", ui.th.Bold(Truncate(p.question, max(1, ui.width-2), "…"))),
		" " + Truncate(shown, max(1, ui.width-3), "…") + ui.th.Inverse(" "),
		ui.th.Fg("dim", " Enter to confirm, Esc to cancel")}
}
