// Bubble Tea frontend for koine. Same core as cmd/koine; richer UI (scrollback
// viewport, multiline input, live tool status).
//
//	cd tui-bubbletea && go mod tidy && go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"koine/core/agent"
	"koine/core/app"
)

var (
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tool  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	bad   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	title = lipgloss.NewStyle().Bold(true)
)

type evMsg agent.Event
type doneMsg struct {
	quit bool
	err  error
}

type model struct {
	app    *app.App
	vp     viewport.Model
	in     textarea.Model
	log    []string
	busy   bool
	cancel context.CancelFunc
	events chan agent.Event
	w, h   int
}

func newModel(a *app.App) model {
	in := textarea.New()
	in.Placeholder = "Message koine — enter to send, alt+enter for newline, esc to interrupt, ctrl+c to quit"
	in.ShowLineNumbers = false
	in.SetHeight(3)
	in.Focus()
	info := a.Describe()
	m := model{app: a, in: in, vp: viewport.New(80, 20), events: make(chan agent.Event, 64)}
	m.log = append(m.log, title.Render("koine")+dim.Render(fmt.Sprintf("  %s · %s", info.Model, info.Cwd)),
		dim.Render(fmt.Sprintf("%d tools · %d skills · %d agents · %d commands", len(info.Tools), len(info.Skills), len(info.Agents), len(info.Commands))))
	for _, w := range a.Warnings {
		m.log = append(m.log, bad.Render("warning: "+w))
	}
	return m
}

func (m model) Init() tea.Cmd { return textarea.Blink }

func waitEvent(ch chan agent.Event) tea.Cmd { return func() tea.Msg { return evMsg(<-ch) } }

func (m model) run(line string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return tea.Batch(
		func() tea.Msg {
			quit, err := m.app.HandleInput(ctx, line, func(e agent.Event) { m.events <- e })
			m.events <- agent.Event{Type: "__end"}
			return doneMsg{quit, err}
		},
		waitEvent(m.events),
	)
}

func (m *model) render() {
	m.vp.SetContent(strings.Join(m.log, "\n"))
	m.vp.GotoBottom()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.vp.Width, m.vp.Height = msg.Width, msg.Height-6
		m.in.SetWidth(msg.Width)
		m.render()
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "esc":
			if m.busy && m.cancel != nil {
				m.cancel()
			}
		case "enter":
			line := strings.TrimSpace(m.in.Value())
			if line == "" || m.busy {
				return m, nil
			}
			m.in.Reset()
			m.busy = true
			m.log = append(m.log, "", title.Render("› ")+line)
			m.render()
			return m, m.run(line)
		}
	case evMsg:
		e := agent.Event(msg)
		if e.Type == "__end" {
			return m, nil
		}
		switch e.Type {
		case "text":
			m.log = append(m.log, e.Text)
		case "tool_start":
			var args map[string]any
			_ = json.Unmarshal(e.Args, &args)
			m.log = append(m.log, tool.Render(fmt.Sprintf("⏺ %s %v", e.Tool, args)))
		case "tool_end":
			st := dim
			if e.IsError {
				st = bad
			}
			t := strings.Join(strings.Fields(e.Text), " ")
			if len(t) > 200 {
				t = t[:200] + "…"
			}
			m.log = append(m.log, st.Render("  ⎿ "+t))
		case "notice":
			m.log = append(m.log, dim.Render(e.Text))
		case "error":
			m.log = append(m.log, bad.Render("error: "+e.Text))
		}
		m.render()
		return m, waitEvent(m.events)
	case doneMsg:
		m.busy = false
		if msg.quit {
			return m, tea.Quit
		}
		if msg.err != nil && msg.err != context.Canceled {
			m.log = append(m.log, bad.Render("error: "+msg.err.Error()))
		}
		m.render()
	}
	var c tea.Cmd
	m.in, c = m.in.Update(msg)
	cmds = append(cmds, c)
	m.vp, c = m.vp.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	status := ""
	if m.busy {
		status = dim.Render("working… (esc to interrupt)")
	}
	return m.vp.View() + "\n" + status + "\n" + m.in.View()
}

func main() {
	cwd, _ := os.Getwd()
	a, err := app.New(context.Background(), cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer a.Close()
	if _, err := tea.NewProgram(newModel(a), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
