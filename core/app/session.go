package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"koine/core/agent"
	"koine/core/llm"
	"koine/core/session"
)

// SessionDir is where this project's sessions live: --session-dir (used as given), $KOINE_SESSION_DIR or ~/.koine/sessions, the last two
// split per project as Pi does.
func (a *App) SessionDir() string {
	if a.opts.SessionDir != "" {
		return a.opts.SessionDir
	}
	root := os.Getenv("KOINE_SESSION_DIR")
	if root == "" {
		root = filepath.Join(a.Home, ".koine", "sessions")
	}
	return session.DirFor(root, a.Cwd)
}

// initSession opens, continues, forks or creates the session for this run and loads its history into the agent.
func (a *App) initSession() error {
	o := a.opts
	a.Agent.Persist = a.persist
	if o.NoSession {
		return nil
	}
	dir := a.SessionDir()
	var s *session.Session
	var err error
	switch {
	case o.SessionRef != "":
		s, err = session.Resolve(dir, o.SessionRef)
	case o.Fork != "":
		var src *session.Session
		if src, err = session.Resolve(dir, o.Fork); err == nil {
			s, err = src.Fork(dir, a.Cwd)
		}
	case o.Continue:
		if s, err = session.Latest(dir); err != nil {
			s, err = session.Create(dir, a.Cwd), nil
		}
	default:
		s = session.Create(dir, a.Cwd)
	}
	if err != nil {
		return err
	}
	a.useSession(s)
	if o.Name != "" {
		return s.SetName(o.Name)
	}
	return nil
}

func (a *App) useSession(s *session.Session) {
	a.Session = s
	a.Agent.History = s.Messages()
	if n := len(a.Agent.History); n > 0 {
		a.Info = append(a.Info, fmt.Sprintf("session %s: %d messages loaded", shortID(s.ID), n))
	}
}

func (a *App) persist(m llm.Message) {
	if a.Session == nil {
		return
	}
	if err := a.Session.AppendMessage(m, "", a.Agent.Model); err != nil {
		a.Warnings = append(a.Warnings, "session not saved: "+err.Error())
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// NewSession clears the conversation and starts a new session file (or none, with --no-session).
func (a *App) NewSession() {
	a.Agent.Reset()
	a.Session = nil
	if !a.opts.NoSession {
		a.Session = session.Create(a.SessionDir(), a.Cwd)
	}
}

// sessionCommand handles /new, /session, /name, /resume and /fork. It reports whether line's command was one of them.
func (a *App) sessionCommand(name, args string, emit func(agent.Event)) bool {
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	fail := func(err error) { emit(agent.Event{Type: "error", Text: err.Error()}) }
	switch name {
	case "new", "clear":
		a.NewSession()
		notice("started a new session")
	case "session":
		if a.Session == nil {
			notice("sessions are off (--no-session)")
			break
		}
		notice(fmt.Sprintf("id %s · %d messages · name %q · file %s", a.Session.ID, len(a.Agent.History), a.Session.Name(), a.Session.Path))
	case "name":
		if a.Session == nil {
			notice("sessions are off (--no-session)")
		} else if args == "" {
			notice(fmt.Sprintf("name: %q", a.Session.Name()))
		} else if err := a.Session.SetName(args); err != nil {
			fail(err)
		} else {
			notice("named: " + args)
		}
	case "resume":
		if args == "" {
			notice(ListSessions(a.SessionDir()))
			break
		}
		s, err := session.Resolve(a.SessionDir(), args)
		if err != nil {
			fail(err)
			break
		}
		a.useSession(s)
		notice(fmt.Sprintf("resumed %s (%d messages)", shortID(s.ID), len(a.Agent.History)))
	case "fork":
		if a.Session == nil {
			notice("sessions are off (--no-session)")
			break
		}
		f, err := a.Session.Fork(a.SessionDir(), a.Cwd)
		if err != nil {
			fail(err)
			break
		}
		a.Session = f
		notice("forked into " + shortID(f.ID))
	default:
		return false
	}
	return true
}

// ListSessions renders the sessions in dir, newest first, for /resume and --resume.
func ListSessions(dir string) string {
	l := session.List(dir)
	if len(l) == 0 {
		return "no sessions yet"
	}
	var b strings.Builder
	for _, i := range l {
		name := i.Name
		if name == "" {
			name = i.Preview
		}
		fmt.Fprintf(&b, "%s  %s  %3d msgs  %s\n", shortID(i.ID), i.Modified.Format("2006-01-02 15:04"), i.Messages, name)
	}
	return strings.TrimRight(b.String(), "\n")
}

// configureCompaction applies the compaction settings to the agent and wires compaction into the session file.
func (a *App) configureCompaction(c CompactionConfig) {
	w := c.ContextWindow
	if w <= 0 {
		w = agent.DefaultContextWindow
	}
	a.Agent.ContextWindow, a.Agent.Reserve, a.Agent.KeepRecent = w, c.ReserveTokens, c.KeepRecentTokens
	a.Agent.AutoCompact = c.Enabled == nil || *c.Enabled
	a.Agent.OnCompact = a.persistCompaction
}

// persistCompaction records a compaction in the session file, so a resumed session starts from the summary.
func (a *App) persistCompaction(firstKept int, summary string, tokensBefore int) {
	if a.Session == nil {
		return
	}
	if len(a.Session.Messages()) != len(a.Agent.History) { // OnCompact runs before the history is replaced
		a.Warnings = append(a.Warnings, "compaction not saved to the session: the file and the conversation disagree")
		return
	}
	if err := a.Session.Compact(firstKept, summary, tokensBefore); err != nil {
		a.Warnings = append(a.Warnings, "compaction not saved to the session: "+err.Error())
	}
}
