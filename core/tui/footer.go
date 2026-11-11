package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"koine/core/agent"
)

// FooterState is what the footer shows. It is fed from the agent's "usage" events.
type FooterState struct {
	Cwd, Branch, Session  string
	Model, Thinking       string
	Input, Output         int
	CacheRead, CacheWrite int
	Cost                  float64
	Unpriced              bool // some responses had no price, so Cost is a lower bound
	CtxTokens, CtxWindow  int
	CtxPct                float64
	CtxKnown              bool
}

// AddUsage accounts for one response.
func (f *FooterState) AddUsage(u *agent.UsageInfo) {
	if u == nil {
		return
	}
	f.Input += u.Input
	f.Output += u.Output
	f.CacheRead += u.CacheRead
	f.CacheWrite += u.CacheWrite
	if u.Cost != nil {
		f.Cost += *u.Cost
	} else {
		f.Unpriced = true
	}
	f.CtxTokens, f.CtxWindow, f.CtxPct, f.CtxKnown = u.ContextTokens, u.ContextWindow, u.ContextPct, true
}

// FormatTokens shortens a token count: 842, 1.2k, 45k, 1.3M.
func FormatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}

// ShortPath replaces the home directory prefix with ~.
func ShortPath(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// Footer draws two lines: the working directory with git branch and session name, then the usage statistics on the left and the model with
// its thinking level on the right. The context percentage turns yellow above 70% and red above 90%.
func Footer(f FooterState, home string, width int, th *Theme) []string {
	loc := ShortPath(f.Cwd, home)
	if f.Branch != "" {
		loc += " (" + f.Branch + ")"
	}
	if f.Session != "" {
		loc += " • " + f.Session
	}
	var stats []string
	if f.Input+f.Output+f.CacheRead+f.CacheWrite > 0 {
		stats = append(stats, "↑"+FormatTokens(f.Input), "↓"+FormatTokens(f.Output))
		if f.CacheRead > 0 {
			stats = append(stats, "R"+FormatTokens(f.CacheRead))
		}
		if f.CacheWrite > 0 {
			stats = append(stats, "W"+FormatTokens(f.CacheWrite))
		}
		switch {
		case f.Cost > 0 && f.Unpriced:
			stats = append(stats, fmt.Sprintf("≥$%.3f", f.Cost))
		case f.Cost > 0:
			stats = append(stats, fmt.Sprintf("$%.3f", f.Cost))
		}
	}
	left := th.Fg("dim", strings.Join(stats, " "))
	if f.CtxKnown && f.CtxWindow > 0 {
		pct := fmt.Sprintf("%.1f%%/%s", f.CtxPct, FormatTokens(f.CtxWindow))
		switch {
		case f.CtxPct > 90:
			pct = th.Fg("error", pct)
		case f.CtxPct > 70:
			pct = th.Fg("warning", pct)
		default:
			pct = th.Fg("dim", pct)
		}
		if len(stats) > 0 {
			left += " "
		}
		left += pct
	}
	right := f.Model
	if f.Thinking != "" {
		right += " • " + f.Thinking
	}
	line2 := left
	if gap := width - Width(left) - Width(right); gap >= 2 {
		line2 += strings.Repeat(" ", gap) + th.Fg("dim", right)
	} else if Width(left) == 0 {
		line2 = th.Fg("dim", right)
	}
	return []string{Truncate(th.Fg("dim", loc), width, "…"), Truncate(line2, width, "…")}
}

// GitBranch reads the branch (or short commit when detached) of the repository containing dir, without running git. It returns "" outside
// a repository.
func GitBranch(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		head := filepath.Join(d, ".git")
		if st, err := os.Stat(head); err == nil {
			if !st.IsDir() { // a worktree: ".git" is a file pointing at the real directory
				b, err := os.ReadFile(head)
				if err != nil || !strings.HasPrefix(string(b), "gitdir:") {
					return ""
				}
				head = strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
				if !filepath.IsAbs(head) {
					head = filepath.Join(d, head)
				}
			}
			b, err := os.ReadFile(filepath.Join(head, "HEAD"))
			if err != nil {
				return ""
			}
			s := strings.TrimSpace(string(b))
			if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
				return ref
			}
			return s[:min(7, len(s))]
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}
