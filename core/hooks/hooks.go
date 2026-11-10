// Package hooks runs shell-command hooks in the Claude Code hook protocol:
// the event is sent as JSON on stdin; exit code 2 (or a JSON "decision") blocks.
// Any language that can read stdin works.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"koine/core/events"
)

// Entry accepts both the flat form {matcher, command} and the nested Claude
// form {matcher, hooks:[{type:"command", command}]}.
type Entry struct {
	Matcher string  `json:"matcher,omitempty"`
	Type    string  `json:"type,omitempty"`
	Command string  `json:"command,omitempty"`
	Timeout int     `json:"timeout,omitempty"`
	Hooks   []Entry `json:"hooks,omitempty"`
}

type Runner struct {
	Cwd       string
	SessionID string
	ByEvent   map[string][]Entry // keyed by Claude event name (PreToolUse …)
	Logf      func(format string, args ...any)
}

var eventMap = map[string]string{
	events.ToolCall:     "PreToolUse",
	events.ToolResult:   "PostToolUse",
	events.Input:        "UserPromptSubmit",
	events.SessionStart: "SessionStart",
	events.AgentEnd:     "Stop",
}

var claudeNames = map[string]string{"bash": "Bash", "read": "Read", "write": "Write", "edit": "Edit", "grep": "Grep", "glob": "Glob", "ls": "LS"}

type payload struct {
	ToolName string          `json:"toolName"`
	CallID   string          `json:"callId"`
	Input    json.RawMessage `json:"input"`
	Output   string          `json:"output"`
	IsError  bool            `json:"isError"`
	Text     string          `json:"text"`
}

func (r *Runner) Count() int {
	n := 0
	for _, es := range r.ByEvent {
		n += len(es)
	}
	return n
}

func (r *Runner) Emit(ctx context.Context, name string, pl any) events.Decision {
	ce, ok := eventMap[name]
	if !ok || len(r.ByEvent[ce]) == 0 {
		return events.Decision{}
	}
	raw, _ := json.Marshal(pl)
	var p payload
	_ = json.Unmarshal(raw, &p)
	toolName := p.ToolName
	if cn, ok := claudeNames[toolName]; ok {
		toolName = cn
	}
	in := map[string]any{"hook_event_name": ce, "session_id": r.SessionID, "cwd": r.Cwd}
	switch name {
	case events.ToolCall, events.ToolResult:
		in["tool_name"] = toolName
		in["tool_input"] = p.Input
		if name == events.ToolResult {
			in["tool_response"] = p.Output
		}
	case events.Input:
		in["prompt"] = p.Text
	}
	stdin, _ := json.Marshal(in)

	var out events.Decision
	for _, e := range r.ByEvent[ce] {
		if !matches(e.Matcher, p.ToolName, toolName) {
			continue
		}
		cmds := e.Hooks
		if e.Command != "" {
			cmds = append([]Entry{e}, cmds...)
		}
		for _, c := range cmds {
			if c.Command == "" {
				continue
			}
			out.Merge(r.run(ctx, c, stdin))
			if out.Block {
				return out
			}
		}
	}
	return out
}

func matches(matcher string, names ...string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("(?i)^(?:" + matcher + ")$")
	if err != nil {
		return false
	}
	for _, n := range names {
		if n != "" && re.MatchString(n) {
			return true
		}
	}
	return false
}

func (r *Runner) run(ctx context.Context, e Entry, stdin []byte) events.Decision {
	to := time.Duration(e.Timeout) * time.Second
	if to <= 0 {
		to = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", e.Command)
	cmd.Dir = r.Cwd
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+r.Cwd, "KOINE_PROJECT_DIR="+r.Cwd)
	cmd.Stdin = bytes.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		reason := strings.TrimSpace(se.String())
		if reason == "" {
			reason = "blocked by hook"
		}
		return events.Decision{Block: true, Reason: reason}
	} else if err != nil {
		if r.Logf != nil {
			r.Logf("hook %q failed: %v %s", e.Command, err, strings.TrimSpace(se.String()))
		}
		return events.Decision{}
	}
	txt := strings.TrimSpace(so.String())
	if !strings.HasPrefix(txt, "{") {
		return events.Decision{}
	}
	var res struct {
		Decision           string `json:"decision"`
		Reason             string `json:"reason"`
		Continue           *bool  `json:"continue"`
		StopReason         string `json:"stopReason"`
		HookSpecificOutput struct {
			PermissionDecision       string          `json:"permissionDecision"`
			PermissionDecisionReason string          `json:"permissionDecisionReason"`
			UpdatedInput             json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal([]byte(txt), &res) != nil {
		return events.Decision{}
	}
	d := events.Decision{Args: res.HookSpecificOutput.UpdatedInput}
	if res.Decision == "block" || res.HookSpecificOutput.PermissionDecision == "deny" || (res.Continue != nil && !*res.Continue) {
		d.Block = true
		d.Reason = firstNonEmpty(res.Reason, res.HookSpecificOutput.PermissionDecisionReason, res.StopReason, "blocked by hook")
	}
	return d
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
