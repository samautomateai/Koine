package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"koine/core/agent"
	"koine/core/compact"
	"koine/core/session"
)

// treeCommand handles /tree, /label, /clone and /export (and /fork with an entry). It reports whether name was one of them.
func (a *App) treeCommand(ctx context.Context, name, args string, emit func(agent.Event)) bool {
	switch name {
	case "tree", "label", "clone", "export":
	case "fork":
		if args == "" {
			return false
		}
	default:
		return false
	}
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	if a.Session == nil {
		notice("sessions are off (--no-session)")
		return true
	}
	var err error
	switch name {
	case "tree":
		err = a.tree(ctx, args, notice)
	case "label":
		err = a.label(args, notice)
	case "clone":
		err = a.cloneAt(a.Session.Leaf(), "", notice)
	case "fork":
		err = a.forkFrom(args, notice)
	case "export":
		err = a.export(args, notice)
	}
	if err != nil {
		emit(agent.Event{Type: "error", Text: err.Error()})
	}
	return true
}

// TreeFilterMode is the filter /tree starts with: the treeFilterMode setting, or "default" when it is missing or not a known mode.
func (a *App) TreeFilterMode() string {
	if session.ValidFilter(a.Cfg.TreeFilterMode) {
		return a.Cfg.TreeFilterMode
	}
	return session.FilterDefault
}

// DoubleEscapeAction is what a frontend with an Escape key should do on a double press with an empty editor: "tree", "fork" or "none"
// (the doubleEscapeAction setting; "tree" when it is missing or invalid). Line mode has no Escape key, so only richer frontends call it.
func (a *App) DoubleEscapeAction() string {
	switch a.Cfg.DoubleEscapeAction {
	case "tree", "fork", "none":
		return a.Cfg.DoubleEscapeAction
	}
	return "tree"
}

func (a *App) skipSummaryPrompt() bool {
	return a.Cfg.BranchSummary.SkipPrompt != nil && *a.Cfg.BranchSummary.SkipPrompt
}

// RenderTree lists the entry tree: one line per entry with its id, marks for the active branch and the current position, and labels.
func RenderTree(s *session.Session, mode string) string {
	rows := s.Rows(mode)
	if len(rows) == 0 {
		return "no entries yet"
	}
	var b strings.Builder
	for _, r := range rows {
		mark := " "
		if r.OnPath {
			mark = "*"
		}
		lab := ""
		if r.Label != "" {
			lab = "  [" + r.Label + "]"
		}
		cur := ""
		if r.IsLeaf {
			cur = "  <- current"
		}
		fmt.Fprintf(&b, "%s%s %s  %s%s%s\n", strings.Repeat("  ", r.Depth), mark, r.Entry.ID, r.Text, lab, cur)
	}
	return strings.TrimRight(b.String(), "\n")
}

// tree lists the tree, or moves to an entry: /tree [filter] | /tree <id|label> [--summary|--no-summary] [focus for the summary].
func (a *App) tree(ctx context.Context, args string, notice func(string)) error {
	s := a.Session
	f := strings.Fields(args)
	if len(f) == 0 || (len(f) == 1 && session.ValidFilter(f[0])) {
		mode := a.TreeFilterMode()
		if len(f) == 1 {
			mode = f[0]
		}
		notice(RenderTree(s, mode) + "\n(* active branch; /tree <id> moves there; /tree <filter>: default no-tools user-only labeled-only all)")
		return nil
	}
	want, rest := "", f[1:]
	for len(rest) > 0 {
		switch rest[0] {
		case "--summary", "-s", "--no-summary":
			want = rest[0]
			rest = rest[1:]
			continue
		}
		break
	}
	focus := strings.Join(rest, " ")
	e, err := s.Find(f[0])
	if err != nil {
		return err
	}
	leaf, text := session.Destination(e)
	old := s.Leaf()
	if leaf == old {
		notice("already there")
		return nil
	}
	left, _ := s.Abandoned(leaf)
	summarise := want == "--summary" || want == "-s"
	if want == "" && len(left) > 0 && !a.skipSummaryPrompt() {
		if a.Choose != nil {
			summarise = a.Choose("Summarise the branch you are leaving?", []string{"No summary", "Summarise"}) == 1
		} else {
			notice("the branch you are leaving was not summarised; add --summary to do that, or set branchSummary.skipPrompt to stop this hint")
		}
	}
	summary := ""
	if summarise && len(left) > 0 {
		if summary, err = a.summariseBranch(ctx, left, focus); err != nil {
			return fmt.Errorf("branch not summarised, still at the old position: %w", err)
		}
	}
	s.SetLeaf(leaf)
	if summary != "" {
		if err := s.AddBranchSummary(old, summary); err != nil {
			return err
		}
	}
	a.Agent.SetHistory(s.Messages())
	msg := fmt.Sprintf("moved to %s; %d messages in context", e.ID, len(a.Agent.History))
	if summary != "" {
		msg += "; summary of the old branch added"
	}
	notice(msg)
	if text != "" {
		notice("that message is not part of the context now; send it again (edited if you like) to start a new branch:\n" + text)
	}
	return nil
}

// summariseBranch asks the model for a summary of the entries being left, newest ones first within the token budget (the context window
// minus branchSummary.reserveTokens, 16384 by default).
func (a *App) summariseBranch(ctx context.Context, left []session.Entry, focus string) (string, error) {
	msgs := session.MessagesOf(left)
	window := a.Agent.ContextWindow
	if window <= 0 {
		window = agent.DefaultContextWindow
	}
	reserve := a.Cfg.BranchSummary.ReserveTokens
	if reserve <= 0 {
		reserve = agent.DefaultReserve
	}
	for len(msgs) > 1 && compact.EstimateTokens(msgs) > window-reserve {
		msgs = msgs[1:]
	}
	if len(msgs) == 0 {
		return "", fmt.Errorf("nothing to summarise")
	}
	instr := "This is a branch of the conversation that is being left for another. Write the summary for someone who continues from the " +
		"earlier point: the goal, constraints, what was done, key decisions, and what remained."
	if focus != "" {
		instr += " Focus: " + focus
	}
	var summary string
	err := agent.Summarizing(ctx, a.event, "branchSummary", "", func(c context.Context) (e error) {
		summary, e = compact.Summarize(c, a.Agent.Provider, a.Agent.Model, "", msgs, instr)
		return e
	})
	return summary, err
}

// labelTarget is the entry /label marks by default: the current position, or the nearest entry above it that is not bookkeeping.
func (a *App) labelTarget() (string, error) {
	id := a.Session.Leaf()
	for id != "" {
		e, ok := a.Session.Entry(id)
		if !ok {
			break
		}
		switch e.Type {
		case "label", "session_info", "model_change", "thinking_level_change":
			if e.ParentID == nil {
				id = ""
			} else {
				id = *e.ParentID
			}
			continue
		}
		return id, nil
	}
	return "", fmt.Errorf("nothing to label yet")
}

// label handles /label (list), /label [@ref] <text> (set) and /label [@ref] - (clear).
func (a *App) label(args string, notice func(string)) error {
	s := a.Session
	if args == "" {
		var b strings.Builder
		for _, r := range s.Rows(session.FilterLabeledOnly) {
			fmt.Fprintf(&b, "%s  [%s]  %s\n", r.Entry.ID, r.Label, r.Text)
		}
		if b.Len() == 0 {
			notice("no labels yet (/label <text> marks the current position)")
		} else {
			notice(strings.TrimRight(b.String(), "\n"))
		}
		return nil
	}
	var target string
	if strings.HasPrefix(args, "@") {
		ref, rest, _ := strings.Cut(args[1:], " ")
		e, err := s.Find(ref)
		if err != nil {
			return err
		}
		target, args = e.ID, strings.TrimSpace(rest)
	} else {
		var err error
		if target, err = a.labelTarget(); err != nil {
			return err
		}
	}
	if args == "" {
		return fmt.Errorf("usage: /label [@id] <text>, or /label [@id] - to clear")
	}
	if args == "-" {
		args = ""
	}
	if err := s.SetLabel(target, args); err != nil {
		return err
	}
	if args == "" {
		notice("label cleared on " + target)
	} else {
		notice(fmt.Sprintf("labelled %s: %s", target, args))
	}
	return nil
}

// cloneAt copies the branch ending at leaf into a new session file and switches to it.
func (a *App) cloneAt(leaf, text string, notice func(string)) error {
	f, err := a.Session.ForkAt(a.SessionDir(), a.Cwd, leaf)
	if err != nil {
		return err
	}
	a.setSession(f)
	a.Agent.SetHistory(f.Messages())
	notice("cloned into " + shortID(f.ID))
	if text != "" {
		notice("send this message again (edited if you like) to continue from there:\n" + text)
	}
	return nil
}

// forkFrom starts a new session from an earlier user message: /fork <id>. The message itself is left out so it can be sent again.
func (a *App) forkFrom(ref string, notice func(string)) error {
	e, err := a.Session.Find(ref)
	if err != nil {
		return err
	}
	leaf, text := session.Destination(e)
	if text == "" {
		return fmt.Errorf("%s is not a user message; /fork needs one (use /clone for the current position)", e.ID)
	}
	return a.cloneAt(leaf, text, notice)
}

// export handles /export [file]: .jsonl writes the session file as stored, anything else (default <id>.html) a self-contained HTML page.
func (a *App) export(args string, notice func(string)) error {
	out := args
	if out == "" {
		out = "koine-session-" + shortID(a.Session.ID) + ".html"
	}
	out = a.resolvePath(out)
	if err := ExportSession(a.Session, out); err != nil {
		return err
	}
	notice("exported to " + out + " (it can contain prompts, tool output and file contents; review it before sharing)")
	return nil
}

// ExportSession writes s to path as JSONL when the name ends in .jsonl, else as HTML. An existing file is not overwritten.
func ExportSession(s *session.Session, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		err = s.ExportJSONL(f)
	} else {
		err = s.ExportHTML(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// ExportFile converts the session file in to HTML or JSONL at out (for `--export`).
func ExportFile(in, out string) error {
	s, err := session.Open(in)
	if err != nil {
		return err
	}
	return ExportSession(s, out)
}

// sessionLines is the numbered list the resume picker shows, newest first.
func sessionLines(l []session.Info) []string {
	out := make([]string, len(l))
	for i, x := range l {
		name := x.Name
		if name == "" {
			name = x.Preview
		}
		out[i] = fmt.Sprintf("%s  %s  %3d msgs  %s", shortID(x.ID), x.Modified.Format("2006-01-02 15:04"), x.Messages, name)
	}
	return out
}

// pickByNumber returns the session a small number refers to in the newest-first list (/resume 2), or false if ref is not such a number.
func pickByNumber(dir, ref string) (string, bool) {
	n, err := strconv.Atoi(ref)
	if err != nil || len(ref) > 3 || n < 1 {
		return "", false
	}
	if l := session.List(dir); n <= len(l) {
		return l[n-1].Path, true
	}
	return "", false
}

// PickResume shows the sessions as a numbered list on out and reads the choice from in ("q" or an empty line cancels). It switches this
// App to the chosen session and reports whether one was chosen. It is the plain-terminal picker behind -r.
func (a *App) PickResume(in io.Reader, out io.Writer) (bool, error) {
	l := session.List(a.SessionDir())
	if len(l) == 0 {
		_, _ = fmt.Fprintln(out, "no sessions yet")
		return false, nil
	}
	for i, line := range sessionLines(l) {
		_, _ = fmt.Fprintf(out, "%2d  %s\n", i+1, line)
	}
	_, _ = fmt.Fprint(out, "resume which? (number, q to cancel) ")
	text, _ := bufio.NewReader(in).ReadString('\n')
	text = strings.TrimSpace(text)
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 || n > len(l) {
		_, _ = fmt.Fprintln(out)
		return false, nil
	}
	s, err := session.Open(l[n-1].Path)
	if err != nil {
		return false, err
	}
	a.useSession(s)
	return true, nil
}
