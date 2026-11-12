package main

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"koine/core/rpcclient"
)

// backend is the part of rpcclient.Client the model uses, so tests can stand in for the server.
type backend interface {
	Prompt(ctx context.Context, message, streamingBehavior string) (string, error)
	Abort(ctx context.Context) error
	RespondUI(id string, fields map[string]any) error
}

// evMsg carries one event from the server; exitMsg says its output ended; sentMsg is the answer to a prompt or abort command.
type (
	evMsg   rpcclient.Event
	exitMsg struct{ err error }
	sentMsg struct{ err error }
)

type model struct {
	b    backend
	tr   *rpcclient.Transcript
	vp   viewport.Model
	in   textarea.Model
	info string // header line: model and working directory
	seen int    // entries already rendered into log
	log  []string
}

func newModel(b backend, info string) model {
	in := textarea.New()
	in.Placeholder = "Message koine — enter to send, alt+enter for newline, esc to interrupt, ctrl+c to quit"
	in.ShowLineNumbers = false
	in.SetHeight(3)
	in.Focus()
	m := model{b: b, tr: rpcclient.NewTranscript(), in: in, vp: viewport.New(80, 20), info: info}
	m.log = []string{title.Render("koine") + dim.Render("  "+info)}
	return m
}

func (m model) Init() tea.Cmd { return textarea.Blink }

// refresh re-renders the entries that changed since the last call. Streaming appends to the last entry, so that one is always redone.
func (m *model) refresh() {
	if m.seen > 0 && m.seen <= len(m.tr.Entries) {
		m.seen--
	}
	m.log = m.log[:1+m.seen]
	for _, e := range m.tr.Entries[m.seen:] {
		m.log = append(m.log, renderEntry(e))
	}
	m.seen = len(m.tr.Entries)
	m.vp.SetContent(strings.Join(m.log, "\n"))
	m.vp.GotoBottom()
}

func (m model) send(line string) tea.Cmd {
	behavior := ""
	if m.tr.Busy {
		behavior = "steer"
	}
	return func() tea.Msg {
		_, err := m.b.Prompt(context.Background(), line, behavior)
		return sentMsg{err}
	}
}

func (m model) abort() tea.Cmd {
	return func() tea.Msg { return sentMsg{m.b.Abort(context.Background())} }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp.Width, m.vp.Height = msg.Width, max(msg.Height-8, 3)
		m.in.SetWidth(msg.Width)
		m.refresh()
	case tea.KeyMsg:
		if next, cmd, handled := m.key(msg); handled {
			return next, cmd
		}
	case evMsg:
		m.tr.Apply(rpcclient.Event(msg))
		if t := m.tr.TakeEditorText(); t != "" {
			m.in.SetValue(t)
		}
		m.refresh()
		return m, nil
	case sentMsg:
		if msg.err != nil {
			m.tr.Add(rpcclient.Entry{Kind: rpcclient.KindError, Text: msg.err.Error()})
			m.refresh()
		}
		return m, nil
	case exitMsg:
		if msg.err != nil {
			m.tr.Add(rpcclient.Entry{Kind: rpcclient.KindError, Text: "koine stopped: " + msg.err.Error()})
			m.refresh()
		}
		return m, tea.Quit
	}
	var cmds []tea.Cmd
	var c tea.Cmd
	m.in, c = m.in.Update(msg)
	cmds = append(cmds, c)
	m.vp, c = m.vp.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

// key handles the keys the model owns; everything else goes to the editor and the viewport.
func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit, true
	case "esc":
		if m.tr.Dialog != nil {
			_ = m.b.RespondUI(m.tr.Dialog.ID, m.tr.Dialog.Cancel())
			m.tr.Dialog = nil
			return m, nil, true
		}
		if m.tr.Busy {
			return m, m.abort(), true
		}
		return m, nil, true
	case "alt+enter":
		m.in.InsertString("\n")
		return m, nil, true
	case "enter":
		line := strings.TrimSpace(m.in.Value())
		if line == "" {
			return m, nil, true
		}
		if d := m.tr.Dialog; d != nil {
			fields, ok := dialogAnswer(d, line)
			if !ok {
				return m, nil, true
			}
			_ = m.b.RespondUI(d.ID, fields)
			m.tr.Dialog = nil
			m.in.Reset()
			return m, nil, true
		}
		m.in.Reset()
		return m, m.send(line), true
	}
	return m, nil, false
}

func (m model) View() string {
	status := ""
	switch {
	case m.tr.Dialog != nil:
		status = renderDialog(m.tr.Dialog)
	case m.tr.Busy:
		status = dim.Render("working… (esc to interrupt, enter steers)")
	}
	if line := m.tr.StatusLine(); line != "" {
		status = strings.TrimSpace(status + "\n" + dim.Render(line))
	}
	return m.vp.View() + "\n" + status + "\n" + m.in.View()
}
