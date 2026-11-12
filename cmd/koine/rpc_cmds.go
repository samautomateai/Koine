package main

import (
	"context"
	"fmt"
	"strings"

	"koine/core/agent"
)

// command carries out every command except prompt (rpc.go). Each answers with exactly one response.
func (s *rpcServer) command(c rpcCmd) {
	a := s.a
	switch c.Type {
	case "steer", "follow_up":
		s.queued(c)
	case "abort":
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		s.idle()
		s.reply(c, nil)
	case "clear_queue":
		steer, follow := a.Agent.ClearQueues()
		s.reply(c, map[string]any{"steering": nonNil(steer), "followUp": nonNil(follow)})
	case "new_session":
		s.slash(c, "/new", map[string]any{"cancelled": false})
	case "switch_session":
		if c.SessionPath == "" {
			s.fail(c, "sessionPath is required")
			return
		}
		s.slash(c, "/resume "+c.SessionPath, map[string]any{"cancelled": false})
	case "clone":
		s.slash(c, "/clone", map[string]any{"cancelled": false})
	case "fork":
		s.fork(c)
	case "compact":
		s.compact(c)
	case "bash":
		s.bash(c)
	case "abort_bash":
		s.mu.Lock()
		cancel := s.bashCancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		s.reply(c, nil)
	case "get_state":
		s.reply(c, s.state())
	case "get_messages":
		s.reply(c, map[string]any{"messages": s.messages()})
	case "get_commands":
		s.reply(c, map[string]any{"commands": a.CommandList()})
	case "set_model":
		info, err := a.SetModel(c.Provider, c.ModelID)
		s.result(c, info, err)
	case "cycle_model":
		info, scoped, ok := a.CycleModel()
		if !ok {
			s.reply(c, nil)
			return
		}
		s.reply(c, map[string]any{"model": info, "thinkingLevel": a.Agent.ThinkingLevel(), "isScoped": scoped})
	case "get_available_models":
		s.reply(c, map[string]any{"models": a.AvailableModels()})
	case "set_thinking_level":
		s.result(c, nil, a.SetThinkingLevel(c.Level))
	case "cycle_thinking_level":
		if level, ok := a.CycleThinkingLevel(); ok {
			s.reply(c, map[string]any{"level": level})
		} else {
			s.reply(c, nil)
		}
	case "get_available_thinking_levels":
		s.reply(c, map[string]any{"levels": a.ThinkingLevelsFor()})
	case "set_steering_mode", "set_follow_up_mode":
		s.queueMode(c)
	case "set_auto_compaction":
		if c.Enabled == nil {
			s.fail(c, "enabled is required")
			return
		}
		a.Agent.AutoCompact = *c.Enabled
		s.reply(c, nil)
	case "set_auto_retry", "abort_retry":
		s.fail(c, c.Type+" is not supported: Koine always retries transient provider errors (see the retry.* settings)")
	default:
		s.sessionCommand(c)
	}
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// result answers with data, or with the error.
func (s *rpcServer) result(c rpcCmd, data any, err error) {
	if err != nil {
		s.fail(c, err.Error())
		return
	}
	s.reply(c, data)
}

// queued handles steer and follow_up: skill and template expansion apply; extension commands are refused.
func (s *rpcServer) queued(c rpcCmd) {
	text := strings.TrimSpace(c.Message)
	if s.a.IsExtensionCommand(text) {
		s.fail(c, "Extension commands are not allowed with "+c.Type+"; use prompt")
		return
	}
	if t, ok := s.a.ExpandTemplate(text); ok {
		text = t
	}
	if c.Type == "steer" {
		s.a.Agent.Steer(text)
	} else {
		s.a.Agent.FollowUp(text)
	}
	s.reply(c, map[string]any{"disposition": "queued"})
}

func (s *rpcServer) queueMode(c rpcCmd) {
	if c.Mode != agent.ModeAll && c.Mode != agent.ModeOneAtATime {
		s.fail(c, `mode must be "all" or "one-at-a-time"`)
		return
	}
	if c.Type == "set_steering_mode" {
		s.a.Agent.SteeringMode = c.Mode
	} else {
		s.a.Agent.FollowUpMode = c.Mode
	}
	s.reply(c, nil)
}

// slash runs a built-in slash command for a command that has no API of its own and answers with data, where cancelled says that an
// extension cancelled it (session_before_*). Errors become a failed response.
func (s *rpcServer) slash(c rpcCmd, line string, data map[string]any) {
	s.idle()
	cancelled, errText := false, ""
	_, _ = s.a.HandleInput(s.ctx, line, func(e agent.Event) {
		switch {
		case e.Type == "error":
			errText = e.Text
		case e.Type == "notice" && e.Text == "cancelled by an extension":
			cancelled = true
		case e.Type == "notice":
			fmt.Fprintln(s.errW, e.Text)
		default:
			s.event(nil, e)
		}
	})
	if errText != "" {
		s.fail(c, errText)
		return
	}
	data["cancelled"] = cancelled
	s.reply(c, data)
}

func (s *rpcServer) fork(c rpcCmd) {
	sess := s.a.Session
	if sess == nil {
		s.fail(c, "sessions are off (--no-session)")
		return
	}
	e, err := sess.Find(c.EntryID)
	if err != nil {
		s.fail(c, err.Error())
		return
	}
	text := userText(e)
	s.slash(c, "/fork "+e.ID, map[string]any{"text": text})
}

func (s *rpcServer) compact(c rpcCmd) {
	s.idle()
	s.mu.Lock()
	s.compacting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.compacting = false
		s.mu.Unlock()
	}()
	r, err := s.a.Agent.CompactFor(s.ctx, c.CustomInstructions, agent.ReasonManual, func(e agent.Event) { s.event(nil, e) })
	if err != nil {
		s.fail(c, err.Error())
		return
	}
	s.reply(c, map[string]any{"summary": r.Summary, "firstKeptEntryId": r.FirstKeptEntryID, "tokensBefore": r.TokensBefore,
		"estimatedTokensAfter": r.TokensAfter})
}

func (s *rpcServer) bash(c rpcCmd) {
	if strings.TrimSpace(c.Command) == "" {
		s.fail(c, "command is required")
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	s.bashCancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.bashCancel = nil
		s.mu.Unlock()
	}()
	update := func(out string) {
		r := record{{"type", "bash_execution_update"}}
		if len(c.ID) > 0 {
			r = append(r, kv{"id", c.ID})
		}
		s.put(append(r, kv{"output", out}))
	}
	res, err := s.a.RunBash(ctx, c.Command, c.ExcludeFromContext, update)
	s.result(c, res, err)
}

// state is get_state's answer.
func (s *rpcServer) state() map[string]any {
	a := s.a
	st, fu := a.Agent.Queued()
	d := map[string]any{
		"thinkingLevel": a.Agent.ThinkingLevel(), "isStreaming": a.Agent.Running(), "isCompacting": s.isCompacting(),
		"steeringMode": modeOf(a.Agent.SteeringMode), "followUpMode": modeOf(a.Agent.FollowUpMode),
		"autoCompactionEnabled": a.Agent.AutoCompact, "messageCount": len(a.Agent.History), "pendingMessageCount": len(st) + len(fu),
	}
	if a.Agent.Model != "" {
		d["model"] = a.CurrentModel()
	}
	if a.Session != nil {
		d["sessionFile"], d["sessionId"] = a.Session.Path, a.Session.ID
		if n := a.Session.Name(); n != "" {
			d["sessionName"] = n
		}
	} else {
		d["sessionId"] = ""
	}
	return d
}

func (s *rpcServer) isCompacting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compacting
}

func modeOf(m string) string {
	if m == "" {
		return agent.ModeOneAtATime
	}
	return m
}

// messages converts the conversation to Pi's AgentMessage objects with the same code that writes the JSON event stream.
func (s *rpcServer) messages() []any {
	js := newJSONStream(discard{}, s.a)
	for _, m := range s.a.Agent.History {
		js.Message(m)
	}
	js.closeTurn()
	if js.messages == nil {
		return []any{}
	}
	return js.messages
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
