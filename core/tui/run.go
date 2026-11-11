package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"

	"koine/core/agent"
	"koine/core/agentdir"
	"koine/core/app"
)

// Terminal is the thin layer between the UI and a real terminal. Everything above it is pure and tested; implementations are the raw-mode
// shim (term_unix.go) and a scripted fake in the tests.
type Terminal interface {
	Size() (w, h int)
	Input() io.Reader
	Output() io.Writer
	ResizeCh() <-chan struct{}
	Raw() (restore func(), err error) // keys arrive unbuffered and unechoed until restore is called
	Suspend() error                   // stop the process until the shell continues it
	Exec(cmd *exec.Cmd) error         // run a program on the terminal in normal (cooked) mode
}

// Options configure Run.
type Options struct {
	Env Env
	Now func() time.Time
}

// Run starts the interactive UI on the process's terminal. When stdin or stdout is not a terminal it falls back to the line-mode loop.
func Run(ctx context.Context, a *app.App) error { return RunWith(ctx, a, Options{}) }

// RunWith is Run with options.
func RunWith(ctx context.Context, a *app.App, o Options) error {
	if t := openTTY(); t != nil && os.Getenv("KOINE_LINE_MODE") == "" {
		return Loop(ctx, a, t, o)
	}
	return runLines(ctx, a)
}

type msg any

type (
	inputMsg   []byte
	eventMsg   agent.Event
	escMsg     struct{}
	tickMsg    struct{}
	resizeMsg  struct{}
	inputEnd   struct{}
	prefillMsg string
	doneMsg    struct {
		quit        bool
		err         error
		interrupted bool
	}
	askMsg struct {
		title string
		opts  []string
		reply chan int
	}
)

// Looks resolves the theme and key bindings the way startup does.
func Looks(a *app.App, set Settings) (*Theme, *Bindings, []string, []string) {
	paths, use, noThemes := a.ThemeArgs()
	dirs := ThemeDirsFor(a.Home, a.Cwd, a.Trusted, noThemes, paths)
	name := set.Theme
	if use != "" {
		name = use
	}
	th, warns := LoadTheme(name, dirs, set.TrueColor)
	keys, kw := LoadBindings(agentdir.Of(a.Home))
	return th, keys, dirs, append(warns, kw...)
}

// Loop runs the interactive UI on term until the user exits or ctx ends.
func Loop(ctx context.Context, a *app.App, term Terminal, o Options) error {
	env := o.Env
	if env == nil {
		env = os.Getenv
	}
	set := ResolveSettings(a, env)
	th, keys, dirs, warns := Looks(a, set)
	w, h := term.Size()
	ui := NewUI(a, set, th, keys, w, h, o.Now)
	ui.themeDirs = dirs
	for _, m := range warns {
		ui.notice("warning: " + m)
	}
	restore, err := term.Raw()
	if err != nil {
		return err
	}
	out := term.Output()
	_, _ = io.WriteString(out, EnableKeys)
	defer func() {
		_, _ = io.WriteString(out, ui.scr.Clear()+RestoreKeys)
		restore()
	}()

	lctx, stop := context.WithCancel(ctx)
	defer stop()
	msgs := make(chan msg, 256)
	send := func(m msg) {
		select {
		case msgs <- m:
		case <-lctx.Done():
		}
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Input().Read(buf)
			if n > 0 {
				send(inputMsg(append([]byte(nil), buf[:n]...)))
			}
			if err != nil {
				send(inputEnd{})
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-term.ResizeCh():
				send(resizeMsg{})
			case <-lctx.Done():
				return
			}
		}
	}()
	a.Choose = func(title string, options []string) int {
		reply := make(chan int, 1)
		send(askMsg{title, options, reply})
		select {
		case i := <-reply:
			return i
		case <-lctx.Done():
			return -1
		}
	}
	a.PrefillEditor = func(t string) { send(prefillMsg(t)) }
	defer func() { a.Choose, a.PrefillEditor = nil, nil }()

	l := &loop{ctx: lctx, a: a, ui: ui, term: term, out: out, env: env, send: send, set: set}
	ui.StartupPrompts()
	l.draw()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !ui.Quit() {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if !ui.Tick() {
				continue
			}
		case m := <-msgs:
			l.handle(m)
		}
		l.actions()
		l.draw()
	}
	return nil
}

// loop is the state Loop needs between messages.
type loop struct {
	ctx     context.Context
	a       *app.App
	ui      *UI
	term    Terminal
	out     io.Writer
	env     Env
	send    func(msg)
	set     Settings
	buf     []byte
	cancel  context.CancelFunc
	wasBusy bool
}

func (l *loop) handle(m msg) {
	ui := l.ui
	switch m := m.(type) {
	case inputMsg:
		l.buf = append(l.buf, m...)
		l.decode(false)
	case escMsg:
		l.decode(true)
	case inputEnd:
		ui.quit = true
	case eventMsg:
		ui.Event(agent.Event(m))
	case doneMsg:
		if l.cancel != nil {
			l.cancel()
			l.cancel = nil
		}
		ui.RunDone(m.quit, m.err, m.interrupted)
	case askMsg:
		ui.Ask(m.title, m.opts, func(i int) { m.reply <- i })
	case prefillMsg:
		ui.Prefill(string(m))
	case resizeMsg:
		w, h := l.term.Size()
		ui.Resize(w, h)
	}
}

// decode feeds buffered input to the UI. A lone Escape waits briefly in case it starts a longer sequence.
func (l *loop) decode(final bool) {
	keys, used := Decode(l.buf, final)
	l.buf = l.buf[used:]
	for _, k := range keys {
		l.ui.Key(k)
	}
	if len(l.buf) > 0 && !final {
		time.AfterFunc(40*time.Millisecond, func() { l.send(escMsg{}) })
	}
}

// actions carries out what the UI asked for.
func (l *loop) actions() {
	ui := l.ui
	for _, act := range ui.Take() {
		switch act.Kind {
		case "run":
			rctx, cancel := context.WithCancel(l.ctx)
			l.cancel = cancel
			go func(line string) {
				quit, err := l.a.HandleInput(rctx, line, func(e agent.Event) { l.send(eventMsg(e)) })
				l.send(doneMsg{quit, err, rctx.Err() != nil})
			}(act.Text)
		case "interrupt":
			if l.cancel != nil {
				l.cancel()
			}
		case "copy":
			if act.Text == "" {
				ui.say("nothing to copy yet")
				break
			}
			_, _ = io.WriteString(l.out, Copy(act.Text))
			ui.say("copied the last answer")
		case "editor":
			l.external(act.Text)
		case "suspend":
			_, _ = io.WriteString(l.out, ui.scr.Clear()+RestoreKeys)
			_ = l.term.Suspend()
			_, _ = io.WriteString(l.out, EnableKeys)
			ui.Redraw()
		}
	}
	if l.set.TerminalProgress && ui.Busy() != l.wasBusy {
		_, _ = io.WriteString(l.out, ProgressSeq(ui.Busy()))
	}
	l.wasBusy = ui.Busy()
}

func (l *loop) external(text string) {
	ui := l.ui
	cmd := EditorCommand(ui.a.Cfg.TUI().ExternalEditor, l.env)
	_, _ = io.WriteString(l.out, ui.scr.Clear()+RestoreKeys)
	got, err := EditExternally(text, cmd, func(c []string) error {
		return l.term.Exec(exec.Command(c[0], c[1:]...)) //nolint:gosec // the user's own editor command
	})
	_, _ = io.WriteString(l.out, EnableKeys)
	ui.Redraw()
	if err != nil {
		ui.say("editor: " + err.Error())
		return
	}
	ui.ed.SetText(got)
	ui.afterEdit()
}

func (l *loop) draw() { _, _ = io.WriteString(l.out, l.ui.Frame()) }
