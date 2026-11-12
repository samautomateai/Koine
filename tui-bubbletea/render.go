package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"koine/core/rpcclient"
)

var (
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tool  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	bad   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	title = lipgloss.NewStyle().Bold(true)
	think = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
)

// renderEntry is how one transcript entry looks. It is plain text plus styles, so it is the same on every terminal that supports colour and
// readable without.
func renderEntry(e rpcclient.Entry) string {
	switch e.Kind {
	case rpcclient.KindUser:
		return "\n" + title.Render("› ") + e.Text
	case rpcclient.KindThinking:
		return think.Render(e.Text)
	case rpcclient.KindTool:
		head := tool.Render(fmt.Sprintf("⏺ %s %s", e.Tool, e.Args))
		switch {
		case !e.Done && e.Text == "":
			return head
		case e.IsError:
			return head + "\n" + bad.Render("  ⎿ "+e.Text)
		default:
			return head + "\n" + dim.Render("  ⎿ "+e.Text)
		}
	case rpcclient.KindNotice:
		return dim.Render(e.Text)
	case rpcclient.KindError:
		return bad.Render("error: " + e.Text)
	}
	return e.Text
}

// renderDialog describes the question an extension is asking and how to answer it in the one-line editor.
func renderDialog(d *rpcclient.Dialog) string {
	var b strings.Builder
	b.WriteString(title.Render(d.Title))
	if d.Message != "" {
		b.WriteString("\n" + d.Message)
	}
	switch d.Method {
	case "select":
		for i, o := range d.Options {
			b.WriteString("\n  " + strconv.Itoa(i+1) + ". " + o)
		}
		b.WriteString("\n" + dim.Render("type a number and press enter, esc to cancel"))
	case "confirm":
		b.WriteString("\n" + dim.Render("y or n, then enter; esc to cancel"))
	default:
		b.WriteString("\n" + dim.Render("type an answer and press enter; esc to cancel"))
	}
	return b.String()
}

// dialogAnswer turns what was typed into the reply fields for the dialog. ok is false when the text does not answer it (a select needs a
// number in range), so the dialog stays open.
func dialogAnswer(d *rpcclient.Dialog, typed string) (fields map[string]any, ok bool) {
	typed = strings.TrimSpace(typed)
	switch d.Method {
	case "select":
		n, err := strconv.Atoi(typed)
		if err != nil || n < 1 || n > len(d.Options) {
			return nil, false
		}
		return d.Reply(d.Options[n-1], false), true
	case "confirm":
		switch strings.ToLower(typed) {
		case "y", "yes":
			return d.Reply("", true), true
		case "n", "no":
			return d.Reply("", false), true
		}
		return nil, false
	}
	return d.Reply(typed, false), true
}
