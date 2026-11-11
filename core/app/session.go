package app

import (
	"context"
	"fmt"
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"strings"

	"koine/core/agent"
	"koine/core/llm"
	"koine/core/session"
)

// SessionDir is where this project's sessions live: --session-dir, $KOINE_SESSION_DIR or the sessionDir setting (in that order, used as
// given, relative paths from the project), else ~/.koine/sessions split per project as Pi does.
func (a *App) SessionDir() string {
	for _, d := range []string{a.opts.SessionDir, os.Getenv("KOINE_SESSION_DIR"), a.Cfg.SessionDir} {
		if d != "" {
			return a.resolvePath(d)
		}
	}
	return session.DirFor(filepath.Join(agentdir.Of(a.Home), "sessions"), a.Cwd)
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
	a.setSession(s)
	a.Agent.SetHistory(s.Messages())
	if n := len(a.Agent.History); n > 0 {
		a.Info = append(a.Info, fmt.Sprintf("session %s: %d messages loaded", shortID(s.ID), n))
	}
}

func (a *App) persist(m llm.Message) {
	if a.OnMessage != nil {
		a.OnMessage(m)
	}
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
	a.setSession(nil)
	if !a.opts.NoSession {
		a.setSession(session.Create(a.SessionDir(), a.Cwd))
	}
}

// sessionCommand handles /new, /session, /name, /resume and /fork. It reports whether line's command was one of them.
func (a *App) sessionCommand(ctx context.Context, name, args string, emit func(agent.Event)) bool {
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
		n, leaves, labels := a.Session.Stats()
		notice(fmt.Sprintf("%d entries · %d branches · %d labels · position %s", n, leaves, labels, a.Session.Leaf()))
		notice(a.Agent.UsageSummary())
	case "name":
		if a.Session == nil {
			notice("sessions are off (--no-session)")
		} else if args == "" {
			notice(fmt.Sprintf("name: %q", a.Session.Name()))
		} else if args == "--clear" {
			if err := a.Session.SetName(""); err != nil {
				fail(err)
			} else {
				a.nameChanged("")
				notice("name cleared")
			}
		} else if err := a.Session.SetName(args); err != nil {
			fail(err)
		} else {
			a.nameChanged(args)
			notice("named: " + args)
		}
	case "resume":
		if args == "" {
			args = a.chooseSession(notice)
			if args == "" {
				break
			}
		}
		if p, ok := pickByNumber(a.SessionDir(), args); ok {
			args = p
		}
		s, err := session.Resolve(a.SessionDir(), args)
		if err != nil {
			fail(err)
			break
		}
		a.useSession(s)
		notice(fmt.Sprintf("resumed %s (%d messages)", shortID(s.ID), len(a.Agent.History)))
	case "fork":
		if args != "" { // /fork <entry>: from an earlier user message (tree.go)
			return a.treeCommand(ctx, name, args, emit)
		}
		if a.Session == nil {
			notice("sessions are off (--no-session)")
			break
		}
		f, err := a.Session.Fork(a.SessionDir(), a.Cwd)
		if err != nil {
			fail(err)
			break
		}
		a.setSession(f)
		notice("forked into " + shortID(f.ID))
	default:
		return a.treeCommand(ctx, name, args, emit)
	}
	return true
}

// chooseSession lets the user pick a session for /resume: through the frontend's Choose when it has one, otherwise by printing the numbered
// list. It returns the path to resume, or "" when nothing was chosen.
func (a *App) chooseSession(notice func(string)) string {
	l := session.List(a.SessionDir())
	if a.Choose == nil || len(l) == 0 {
		notice(ListSessions(a.SessionDir()) + "\n(/resume <number|id|name>)")
		return ""
	}
	if i := a.Choose("Resume which session?", sessionLines(l)); i >= 0 && i < len(l) {
		return l[i].Path
	}
	return ""
}

// ListSessions renders the sessions in dir, newest first, for /resume and --resume.
func ListSessions(dir string) string {
	l := session.List(dir)
	if len(l) == 0 {
		return "no sessions yet"
	}
	var b strings.Builder
	for i, line := range sessionLines(l) {
		fmt.Fprintf(&b, "%2d  %s\n", i+1, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

// configureCompaction applies the compaction settings to the agent and wires compaction into the session file.
func (a *App) configureCompaction(c CompactionConfig) {
	w := c.ContextWindow // 0 lets the agent take the window from the model catalog
	a.Agent.ContextWindow, a.Agent.Reserve, a.Agent.KeepRecent = w, c.ReserveTokens, c.KeepRecentTokens
	a.Agent.AutoCompact = c.Enabled == nil || *c.Enabled
	a.Agent.OnCompact = a.persistCompaction
	a.Agent.KeptEntryID = a.keptEntryID
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

// resolvePath expands a leading ~ and makes relative paths relative to the project.
func (a *App) resolvePath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(a.Home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.Cwd, p)
	}
	return p
}
