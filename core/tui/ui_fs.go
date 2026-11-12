package tui

import (
	"runtime"
	"strings"
)

// Fullscreen mode in the UI model (REQ-TUI-002). The transcript is rendered whole each frame (finished blocks are cached) and shown through
// a Fullscreen viewport; nothing is printed to scrollback while the alternate screen is on.

// fullscreenAllowed reports whether fullscreen mode can be used: the setting asks for it and the terminal is not "dumb".
func fullscreenAllowed(want bool, env Env) bool { return want && env("TERM") != "dumb" }

// wheelIsLocalMac says whether the mouse wheel is read in a terminal on this Mac rather than over SSH, where Pi's "auto" moves one line.
func wheelIsLocalMac(goos string, env Env) bool {
	return goos == "darwin" && env("SSH_CONNECTION") == "" && env("SSH_TTY") == ""
}

func (ui *UI) fsFrame() string {
	ctx := ui.ctx()
	bar := ui.set.Scrollbar
	if bar != "hidden" {
		ctx.Width-- // the last column is the scrollbar's
	}
	if ui.full {
		ui.fsLines, ui.fsN = nil, 0
	}
	n := len(ui.finalBlocks())
	if n > ui.fsN {
		ui.fsLines = append(ui.fsLines, RenderBlocks(ui.blocks[ui.fsN:n], ctx)...)
		ui.fsN = n
	}
	content := ui.fsLines
	if n < len(ui.blocks) {
		content = append(append([]string(nil), content...), RenderBlocks(ui.blocks[n:], ctx)...)
	}
	live, row, col := ui.live()
	rows := ui.fs.Layout(content, live, FSOptions{Width: ui.width, Height: ui.height, Scrollbar: bar, Thumb: ui.th.Fg("accent", "┃"),
		Track: ui.th.Fg("borderMuted", "│")})
	show := ui.set.HardwareCursor && ui.sel == nil && ui.tree == nil && ui.prompt == nil
	s := ui.fs.Paint(rows, ui.fs.CursorRow(row), col, show, ui.full)
	ui.full = false
	return s
}

// scrollKey applies the tui.altScreen.* bindings and the mouse wheel to the transcript. It reports whether k was one of them.
func (ui *UI) scrollKey(k Key) bool {
	b, fs := ui.keys, ui.fs
	half := max(1, fs.PageSize()/2)
	switch {
	case strings.HasSuffix(k.Name, "wheelUp") || strings.HasSuffix(k.Name, "wheelDown"):
		n := ui.wheel.step(ui.set.WheelLines, wheelIsLocalMac(runtime.GOOS, ui.env), ui.now().UnixNano())
		if strings.HasPrefix(k.Name, "alt+") {
			n *= 5
		}
		if strings.HasSuffix(k.Name, "Down") {
			n = -n
		}
		fs.Scroll(n)
	case b.Matches("tui.altScreen.pageUp", k):
		fs.Scroll(fs.PageSize())
	case b.Matches("tui.altScreen.pageDown", k):
		fs.Scroll(-fs.PageSize())
	case b.Matches("tui.altScreen.halfPageUp", k):
		fs.Scroll(half)
	case b.Matches("tui.altScreen.halfPageDown", k):
		fs.Scroll(-half)
	case b.Matches("tui.altScreen.lineUp", k):
		fs.Scroll(1)
	case b.Matches("tui.altScreen.lineDown", k):
		fs.Scroll(-1)
	case b.Matches("tui.altScreen.top", k):
		fs.Top()
	case b.Matches("tui.altScreen.bottom", k):
		fs.Bottom()
	default:
		return false
	}
	return true
}

// ExitOutput is what fullscreenExitOutput asks to print on the normal screen after fullscreen mode ends: the transcript, or a hint for
// resuming the session. It is empty in regular mode and when there is nothing to say.
func (ui *UI) ExitOutput() string {
	if ui.fs == nil {
		return ""
	}
	if ui.set.ExitOutput == "resume-hint" {
		if s := ui.session(); s != nil && s.ID != "" {
			return "Resume this session with: koine --session " + s.ID + "\r\n"
		}
		return ""
	}
	lines := RenderBlocks(ui.finalBlocks(), ui.ctx())
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\x1b[0m\r\n") + "\x1b[0m\r\n"
}

// park returns what hands the terminal back to the shell or another program: key modes undone, and either the live area erased (regular
// mode) or the alternate screen left (fullscreen).
func (ui *UI) park() string {
	if ui.fs != nil {
		return RestoreKeys + FullscreenLeave
	}
	return ui.scr.Clear() + RestoreKeys
}

// unpark undoes park: key modes on, and the alternate screen entered in fullscreen mode (the next frame is then drawn in full).
func (ui *UI) unpark() string {
	if ui.fs != nil {
		ui.full = true
		return EnableKeys + FullscreenEnter
	}
	return EnableKeys
}

func (ui *UI) finalBlocks() []Block {
	n := 0
	for n < len(ui.blocks) && ui.blocks[n].Final {
		n++
	}
	return ui.blocks[:n]
}
