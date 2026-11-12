package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"koine/core/agent"
	"koine/core/app"
	"koine/core/session"
)

// Action is something the UI wants done outside itself: running a line through the app, interrupting the run, and the things that need the
// terminal (an external editor, suspending, copying).
type Action struct {
	Kind string // run, interrupt, editor, suspend, copy, quit
	Text string
}

// UI is the interactive screen model. It never touches a terminal: Key, Event, Tick and the like change its state, Frame returns the bytes
// to draw, and Take returns what the loop must do. Everything is driven from one goroutine.
type UI struct {
	a    *app.App
	set  Settings
	th   *Theme
	keys *Bindings
	ed   *Editor
	src  Sources

	comp     *Completion
	compOff  bool
	lastText string

	blocks  []Block
	printed int
	pending []string
	full    bool
	scr     Screen

	prompt  *promptEdit
	sel     *Selector
	selPick func(idx int)
	selKey  func(k Key) bool // extra keys of the open selector; true when handled
	tree    *TreeNav
	treeFor string // "tree" or "fork"
	label   *labelEdit

	foot         FooterState
	busy         bool
	busyAt       time.Time
	frame        int
	expanded     bool
	hideThinking bool
	flash        string
	flashUntil   time.Time
	armedC       time.Time
	armedEsc     time.Time
	lastRun      string

	width, height int
	quit          bool
	actions       []Action
	now           func() time.Time
	themeDirs     []string
	themeWarns    []string
	scoped        []string

	fs      *Fullscreen // fullscreen mode (REQ-TUI-002); nil in regular mode
	fsLines []string    // rendered finished blocks, kept between frames
	fsN     int
	wheel   wheel
	env     Env
}

type labelEdit struct {
	id string
	ed *Editor
}

// NewUI builds the screen model for a. now may be nil.
func NewUI(a *app.App, set Settings, th *Theme, keys *Bindings, width, height int, now func() time.Time) *UI {
	if now == nil {
		now = time.Now
	}
	ui := &UI{a: a, set: set, th: th, keys: keys, ed: NewEditor(), width: max(20, width), height: max(5, height), now: now,
		hideThinking: set.HideThinking, scoped: a.Scope}
	a.Agent.HideThinking = false // the UI hides thinking itself, so Ctrl+T can bring it back
	if set.FullscreenOn {
		ui.fs = &Fullscreen{}
	}
	ui.env = os.Getenv
	ui.src = Sources{Cwd: a.Cwd, Files: &FileIndex{}, Commands: ui.commandList(), Args: ui.commandArgs}
	ui.foot = FooterState{Cwd: a.Cwd, Branch: GitBranch(a.Cwd)}
	ui.foot.Input, ui.foot.Output = a.Agent.Totals.Input, a.Agent.Totals.Output
	ui.foot.CacheRead, ui.foot.CacheWrite = a.Agent.Totals.CacheRead, a.Agent.Totals.CacheWrite
	ui.foot.Cost, ui.foot.Unpriced = a.Agent.CostTotal, a.Agent.Unpriced
	ui.refreshFooter()
	ui.ed.SetHistory(UserPrompts(a.Agent.History))
	ui.blocks = append(ui.blocks, ui.banner()...)
	ui.blocks = append(ui.blocks, BlocksFromHistory(a.Agent.History)...)
	return ui
}

func (ui *UI) commandList() []Command {
	cmds := append([]Command(nil), BuiltinCommands...)
	seen := map[string]bool{}
	for _, c := range cmds {
		seen[c.Name] = true
	}
	info := ui.a.Describe()
	for _, n := range info.Commands {
		if !seen[n] {
			cmds = append(cmds, Command{n, "extension command or prompt template"})
		}
	}
	for _, s := range info.Skills {
		cmds = append(cmds, Command{"skill:" + s, "skill"})
	}
	return cmds
}

// banner is the startup header: the name and model, then (unless quietStartup says otherwise) the resource listing, and always warnings.
func (ui *UI) banner() []Block {
	info := ui.a.Describe()
	th := ui.th
	var b []Block
	if ui.set.Quiet != "true" {
		b = append(b, Block{Kind: "banner", Final: true, Text: th.Fg("accent", th.Bold("koine")) + "  " + info.Model + " · " + info.Cwd})
	}
	if ui.set.Quiet == "" {
		b = append(b, Block{Kind: "banner", Final: true, Text: th.Fg("dim", fmt.Sprintf("%d tools · %d skills · %d agents · %d commands · /help · /hotkeys",
			len(info.Tools), len(info.Skills), len(info.Agents), len(info.Commands)))})
	}
	for _, w := range ui.a.Warnings {
		b = append(b, Block{Kind: "banner", Final: true, Text: th.Fg("warning", "warning: "+w)})
	}
	for _, w := range ui.themeWarns {
		b = append(b, Block{Kind: "banner", Final: true, Text: th.Fg("warning", "theme: "+w)})
	}
	return b
}

func (ui *UI) refreshFooter() {
	f := &ui.foot
	f.Model = ui.a.Agent.Model
	f.Thinking = ui.a.Agent.ThinkingLevel()
	f.Session = ""
	if ui.a.Session != nil {
		f.Session = ui.a.Session.Name()
	}
}

// Take returns the pending actions and clears them.
func (ui *UI) Take() []Action {
	a := ui.actions
	ui.actions = nil
	return a
}

// Quit reports whether the UI wants to exit.
func (ui *UI) Quit() bool { return ui.quit }

// Busy reports whether a turn is running.
func (ui *UI) Busy() bool { return ui.busy }

// Resize records a new terminal size; the next frame redraws everything.
func (ui *UI) Resize(w, h int) {
	ui.width, ui.height = max(20, w), max(5, h)
	ui.full = true
}

// Redraw asks for a full redraw of the transcript (after a theme change, expand toggle and the like).
func (ui *UI) Redraw() { ui.full = true }

func (ui *UI) ctx() RenderCtx {
	return RenderCtx{Width: ui.width, Theme: ui.th, Expanded: ui.expanded, HideThinking: ui.hideThinking, OutputPad: ui.set.OutputPad,
		CodeIndent: ui.set.CodeIndent, Hyperlinks: ui.set.Links, ImageProto: ui.set.ImageProto, ImageCells: ui.set.ImageWidth,
		ShowImages: ui.set.ShowImages}
}

// flush moves finished blocks (those before the first unfinished one) into the lines to print.
func (ui *UI) flush() {
	n := 0
	for n < len(ui.blocks) && ui.blocks[n].Final {
		n++
	}
	if ui.full {
		ui.pending = RenderBlocks(ui.blocks[:n], ui.ctx())
		ui.printed = n
		return
	}
	if n > ui.printed {
		ui.pending = append(ui.pending, RenderBlocks(ui.blocks[ui.printed:n], ui.ctx())...)
		ui.printed = n
	}
}

// Frame returns the bytes that bring the terminal up to date.
func (ui *UI) Frame() string {
	if ui.fs != nil {
		return ui.fsFrame()
	}
	ui.flush()
	live, row, col := ui.live()
	show := ui.set.HardwareCursor && ui.sel == nil && ui.tree == nil && ui.prompt == nil
	s := ui.scr.Draw(ui.pending, live, row, col, show, ui.full, ui.height)
	ui.pending, ui.full = nil, false
	return s
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (ui *UI) status() string {
	th := ui.th
	switch {
	case ui.flash != "":
		return th.Fg("warning", ui.flash)
	case ui.busy:
		el := ui.now().Sub(ui.busyAt).Round(time.Second)
		msg := "Working…"
		if w := ui.a.Surfaces().Working; w != "" { // ctx.ui.setWorkingMessage
			msg = w
		}
		return th.Fg("accent", spinner[ui.frame%len(spinner)]) + th.Fg("muted", fmt.Sprintf(" %s %s  (%s to interrupt)", msg, el,
			firstKey(ui.keys, "app.interrupt")))
	}
	return ""
}

func firstKey(b *Bindings, id string) string {
	if k := b.Keys(id); len(k) > 0 {
		return strings.Replace(k[0], "escape", "esc", 1)
	}
	return "unbound"
}

// live draws the area below the transcript and says where the cursor is in it.
func (ui *UI) live() (lines []string, row, col int) {
	ctx := ui.ctx()
	if ui.fs == nil && ui.printed < len(ui.blocks) {
		lines = append(lines, RenderBlocks(ui.blocks[ui.printed:], ctx)...)
	}
	lines = append(lines, ui.status())
	surf := ui.a.Surfaces() // what extensions set through ctx.ui: widgets around the editor and status entries under it
	switch {
	case ui.prompt != nil:
		lines = append(lines, ui.promptView()...)
		row = len(lines) - 1
	case ui.sel != nil:
		lines = append(lines, ui.sel.View(ui.width, ui.th)...)
		row = len(lines) - 1
	case ui.tree != nil:
		lines = append(lines, ui.tree.View(ui.width, ui.th, ui.treeHint())...)
		if ui.label != nil {
			lines = append(lines, " "+ui.th.Fg("accent", "label for "+ui.label.id+": ")+ui.label.ed.Text()+ui.th.Inverse(" "))
		}
		row = len(lines) - 1
	default:
		view, r, c := ui.ed.View(ViewOptions{Width: ui.width, PadX: ui.set.EditorPaddingX, Theme: ui.th, HardwareCursor: ui.set.HardwareCursor,
			BorderToken: ui.borderToken(), Placeholder: ""})
		lines = append(lines, surf.Above...)
		row, col = len(lines)+r, c
		lines = append(lines, view...)
		if ui.comp != nil {
			lines = append(lines, ui.comp.View(ui.width, ui.set.AutocompleteMax, ui.th)...)
		}
	}
	if ui.prompt == nil && ui.sel == nil && ui.tree == nil {
		lines = append(lines, surf.Below...)
	}
	lines = append(lines, Footer(ui.foot, ui.a.Home, ui.width, ui.th)...)
	if len(surf.Statuses) > 0 {
		lines = append(lines, ui.th.Fg("muted", " "+strings.Join(surf.Statuses, " · ")))
	}
	for i := range lines {
		lines[i] = Truncate(lines[i], ui.width, "")
	}
	return lines, row, col
}

// borderToken colours the editor frame by thinking level, as Pi does.
func (ui *UI) borderToken() string {
	switch l := ui.foot.Thinking; l {
	case "minimal", "low", "medium", "high", "xhigh", "max":
		return "thinking" + strings.ToUpper(l[:1]) + l[1:]
	case "off":
		return "thinkingOff"
	}
	return "borderMuted"
}

func (ui *UI) treeHint() string {
	k := func(id string) string { return firstKey(ui.keys, id) }
	return fmt.Sprintf("enter select · esc close · %s/%s fold · %s label · %s filter · type to search", k("app.tree.foldOrUp"),
		k("app.tree.unfoldOrDown"), k("app.tree.editLabel"), k("app.tree.filter.cycleForward"))
}

// Tick advances the spinner and expires the flash message. It reports whether anything on screen changed.
func (ui *UI) Tick() bool {
	changed := false
	if ui.busy {
		ui.frame++
		changed = true
	}
	if ui.flash != "" && !ui.now().Before(ui.flashUntil) {
		ui.flash, changed = "", true
	}
	return changed
}

func (ui *UI) say(msg string) {
	ui.flash, ui.flashUntil = msg, ui.now().Add(2*time.Second)
}

func (ui *UI) add(b Block) { ui.blocks = append(ui.blocks, b) }

func (ui *UI) notice(text string) { ui.add(Block{Kind: "notice", Text: text, Final: true}) }

// Event applies one agent event to the transcript and the footer.
func (ui *UI) Event(e agent.Event) {
	last := func(kind string) *Block {
		if n := len(ui.blocks); n > 0 && ui.blocks[n-1].Kind == kind && !ui.blocks[n-1].Final {
			return &ui.blocks[n-1]
		}
		return nil
	}
	switch e.Type {
	case "text_delta", "thinking_delta":
		kind := strings.TrimSuffix(e.Type, "_delta")
		if kind == "thinking" || kind == "text" {
			if kind == "text" {
				kind = "assistant"
			}
			if b := last(kind); b != nil {
				b.Text += e.Text
			} else {
				ui.add(Block{Kind: kind, Text: e.Text})
			}
		}
	case "text":
		if b := last("assistant"); b != nil {
			b.Text, b.Final = e.Text, true
		} else {
			ui.add(Block{Kind: "assistant", Text: e.Text, Final: true})
		}
	case "thinking":
		if b := last("thinking"); b != nil {
			b.Text, b.Final = e.Text, true
		} else {
			ui.add(Block{Kind: "thinking", Text: e.Text, Final: true})
		}
	case "user_message": // a queued steering or follow-up message the agent has now taken up
		ui.add(Block{Kind: "user", Text: e.Text, Final: true})
	case "aborted": // keep what streamed so far; RunDone adds the "(interrupted)" line
		for i := range ui.blocks {
			if k := ui.blocks[i].Kind; k == "assistant" || k == "thinking" {
				ui.blocks[i].Final = true
			}
		}
	case "tool_start":
		ui.add(Block{Kind: "tool", Tool: &ToolCall{ID: e.CallID, Name: e.Tool, Args: e.Args}})
	case "tool_end":
		ui.endTool(e)
	case "notice":
		ui.notice(e.Text)
	case "extension_error":
		ui.notice("warning: " + e.Text)
	case "custom_message": // a message an extension added; display says whether it is shown
		if show, _ := e.Data["display"].(bool); show {
			ui.notice(fmt.Sprintf("[%v] %s", e.Data["customType"], e.Text))
		}
	case "error":
		ui.add(Block{Kind: "error", Text: e.Text, Final: true})
	case "usage":
		ui.foot.AddUsage(e.Usage)
	}
}

func (ui *UI) endTool(e agent.Event) {
	for i := len(ui.blocks) - 1; i >= 0; i-- {
		b := &ui.blocks[i]
		if b.Kind != "tool" || b.Final {
			continue
		}
		if (e.CallID != "" && b.Tool.ID == e.CallID) || (e.CallID == "" && (e.Tool == "" || b.Tool.Name == e.Tool)) {
			b.Tool.Result, b.Tool.IsError, b.Tool.Done, b.Final = e.Text, e.IsError, true, true
			b.Tool.Images = ImagesOf(e.Images)
			return
		}
	}
	tc := &ToolCall{ID: e.CallID, Name: e.Tool, Result: e.Text, IsError: e.IsError, Done: true, Images: ImagesOf(e.Images)}
	ui.add(Block{Kind: "tool", Tool: tc, Final: true})
}

// RunDone is called when the app finished a line. interrupted is true when the user cancelled it.
func (ui *UI) RunDone(quit bool, err error, interrupted bool) {
	ui.busy = false
	for i := range ui.blocks { // anything still open ends here
		ui.blocks[i].Final = true
	}
	switch {
	case interrupted:
		ui.notice("(interrupted)")
	case err != nil:
		ui.add(Block{Kind: "error", Text: err.Error(), Final: true})
	}
	if quit {
		ui.quit = true
	}
	ui.refreshFooter()
	if sessionCommand(ui.lastRun) {
		ui.rebuildTranscript()
	}
}

func sessionCommand(line string) bool {
	name, _, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	if !strings.HasPrefix(line, "/") {
		return false
	}
	switch name {
	case "new", "clear", "resume", "fork", "clone", "tree":
		return true
	}
	return false
}

// rebuildTranscript redraws the screen from the conversation, after the session changed.
func (ui *UI) rebuildTranscript() {
	ui.blocks = append(ui.banner(), BlocksFromHistory(ui.a.Agent.History)...)
	ui.ed.SetHistory(UserPrompts(ui.a.Agent.History))
	ui.full = true
}

// Prefill puts text in the editor (a user message the session tree went back to).
func (ui *UI) Prefill(text string) {
	ui.ed.SetText(text)
	ui.afterEdit()
}

// session returns the open session, or nil.
func (ui *UI) session() *session.Session { return ui.a.Session }
