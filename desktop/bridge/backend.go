// Package bridge is the Go side of the desktop page: it drives the agent through core/rpcclient and keeps the transcript. It imports no GUI
// code, so its tests run without WebKit or a display.
package bridge

import (
	"context"
	"sync"

	"koine/core/rpcclient"
)

// rpc is the part of rpcclient.Client the backend uses; tests replace it.
type rpc interface {
	Prompt(ctx context.Context, message, streamingBehavior string) (string, error)
	Abort(ctx context.Context) error
	RespondUI(id string, fields map[string]any) error
	GetState(ctx context.Context) (rpcclient.State, error)
	GetCommands(ctx context.Context) ([]rpcclient.Command, error)
}

// Change is one transcript entry that was added or changed.
type Change struct {
	Index int             `json:"index"`
	Entry rpcclient.Entry `json:"entry"`
}

// Update is what the page receives on "koine:update" after each server event that changed something.
type Update struct {
	Changes []Change            `json:"changes"`
	Busy    bool                `json:"busy"`
	Dialog  *rpcclient.Dialog   `json:"dialog"`
	Status  string              `json:"status"`
	Title   string              `json:"title"`
	Editor  string              `json:"editor,omitempty"` // text an extension put in the editor
	Widgets map[string][]string `json:"widgets"`
}

// Info is the sidebar: the model, the working directory and the slash commands.
type Info struct {
	Model    string              `json:"model"`
	Cwd      string              `json:"cwd"`
	Commands []rpcclient.Command `json:"commands"`
}

// Backend is bound to the page (window.go.bridge.Backend.*). It drives `koine --mode rpc` through core/rpcclient and keeps the transcript;
// the page only draws it. emit is Wails' EventsEmit in the app and a recorder in tests.
type Backend struct {
	emit func(name string, data ...any)
	Cwd  string

	mu sync.Mutex
	c  rpc
	tr *rpcclient.Transcript
}

// NewBackend returns a backend that has no server yet; Attach gives it one.
func NewBackend(emit func(string, ...any), cwd string) *Backend {
	return &Backend{emit: emit, Cwd: cwd, tr: rpcclient.NewTranscript()}
}

// Attach sets the client and feeds the backend with the events until they end.
func (b *Backend) Attach(c rpc, events <-chan rpcclient.Event) {
	b.mu.Lock()
	b.c = c
	b.mu.Unlock()
	for e := range events {
		b.Handle(e)
	}
	b.Handle(rpcclient.Event{Type: "stderr", Raw: []byte(`{"type":"stderr","text":"koine: error: the agent process stopped"}`)})
}

// Handle folds one event into the transcript and tells the page what changed.
func (b *Backend) Handle(e rpcclient.Event) {
	b.mu.Lock()
	idx := b.tr.Apply(e)
	u := Update{Busy: b.tr.Busy, Dialog: b.tr.Dialog, Status: b.tr.StatusLine(), Title: b.tr.Title, Editor: b.tr.TakeEditorText(),
		Widgets: b.tr.Widgets, Changes: []Change{}}
	for _, i := range idx {
		u.Changes = append(u.Changes, Change{Index: i, Entry: b.tr.Entries[i]})
	}
	emit := b.emit
	b.mu.Unlock()
	emit("koine:update", u)
}

func (b *Backend) client() rpc {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.c
}

// Info returns the model, directory and commands for the sidebar.
func (b *Backend) Info() Info {
	i := Info{Cwd: b.Cwd, Commands: []rpcclient.Command{}}
	c := b.client()
	if c == nil {
		return i
	}
	if st, err := c.GetState(context.Background()); err == nil && st.Model != nil {
		i.Model = st.Model.ID
	}
	if cmds, err := c.GetCommands(context.Background()); err == nil {
		i.Commands = cmds
	}
	return i
}

// Send sends a prompt, or steers the run in progress. A refusal is shown in the transcript.
func (b *Backend) Send(text string) {
	c := b.client()
	if c == nil {
		return
	}
	b.mu.Lock()
	behavior := ""
	if b.tr.Busy {
		behavior = "steer"
	}
	b.mu.Unlock()
	if _, err := c.Prompt(context.Background(), text, behavior); err != nil {
		b.Fail(err)
	}
}

// Cancel interrupts the run in progress.
func (b *Backend) Cancel() {
	if c := b.client(); c != nil {
		if err := c.Abort(context.Background()); err != nil {
			b.Fail(err)
		}
	}
}

// Answer replies to the dialog an extension opened: a chosen or typed value, or yes/no for a confirmation.
func (b *Backend) Answer(value string, yes bool) {
	b.mu.Lock()
	d := b.tr.Dialog
	b.tr.Dialog = nil
	b.mu.Unlock()
	if c := b.client(); c != nil && d != nil {
		_ = c.RespondUI(d.ID, d.Reply(value, yes))
	}
	b.Handle(rpcclient.Event{Type: "none", Raw: []byte(`{"type":"none"}`)}) // redraw without the dialog
}

// Dismiss declines the dialog.
func (b *Backend) Dismiss() {
	b.mu.Lock()
	d := b.tr.Dialog
	b.tr.Dialog = nil
	b.mu.Unlock()
	if c := b.client(); c != nil && d != nil {
		_ = c.RespondUI(d.ID, d.Cancel())
	}
	b.Handle(rpcclient.Event{Type: "none", Raw: []byte(`{"type":"none"}`)})
}

// SetEmit sets how events reach the page (Wails' EventsEmit once the window exists).
func (b *Backend) SetEmit(f func(string, ...any)) {
	b.mu.Lock()
	b.emit = f
	b.mu.Unlock()
}

// Fail shows an error in the transcript.
func (b *Backend) Fail(err error) {
	b.mu.Lock()
	i := b.tr.Add(rpcclient.Entry{Kind: rpcclient.KindError, Text: err.Error()})
	u := Update{Busy: b.tr.Busy, Dialog: b.tr.Dialog, Status: b.tr.StatusLine(), Widgets: b.tr.Widgets,
		Changes: []Change{{Index: i, Entry: b.tr.Entries[i]}}}
	emit := b.emit
	b.mu.Unlock()
	emit("koine:update", u)
}
