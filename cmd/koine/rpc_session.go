package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"koine/core/app"
	"koine/core/session"
)

// sessionCommand answers the commands about the session file and its contents; anything else is an unknown command.
func (s *rpcServer) sessionCommand(c rpcCmd) {
	a := s.a
	switch c.Type {
	case "get_session_stats":
		s.reply(c, s.stats())
	case "export_html":
		if a.Session == nil {
			s.fail(c, "sessions are off (--no-session)")
			return
		}
		out := c.OutputPath
		if out == "" {
			out = filepath.Join(a.Cwd, a.Session.ID+".html")
		}
		if err := app.ExportSession(a.Session, out); err != nil {
			s.fail(c, err.Error())
			return
		}
		s.reply(c, map[string]any{"path": out})
	case "get_fork_messages":
		s.reply(c, map[string]any{"messages": s.forkMessages()})
	case "get_entries":
		entries, leaf := s.entries()
		if c.Since != "" {
			i := indexOfEntry(entries, c.Since)
			if i < 0 {
				s.fail(c, fmt.Sprintf("entry %q not found", c.Since))
				return
			}
			entries = entries[i+1:]
		}
		s.reply(c, map[string]any{"entries": entries, "leafId": leaf})
	case "get_tree":
		entries, leaf := s.entries()
		s.reply(c, map[string]any{"tree": buildTree(entries), "leafId": leaf})
	case "get_last_assistant_text":
		s.reply(c, map[string]any{"text": s.lastAssistantText()})
	case "set_session_name":
		if strings.TrimSpace(c.Name) == "" {
			s.fail(c, "name is required")
			return
		}
		s.slash(c, "/name "+strings.TrimSpace(c.Name), map[string]any{})
	default:
		s.fail(c, "Unknown command: "+c.Type)
	}
}

func (s *rpcServer) stats() map[string]any {
	st := s.a.Stats()
	d := map[string]any{
		"userMessages": st.UserMessages, "assistantMessages": st.AssistantMessages, "toolCalls": st.ToolCalls, "toolResults": st.ToolResults,
		"totalMessages": st.TotalMessages,
		"tokens": map[string]any{"input": st.Tokens.Input, "output": st.Tokens.Output, "cacheRead": st.Tokens.CacheRead,
			"cacheWrite": st.Tokens.CacheWrite, "total": st.Tokens.Total()},
		"cost": st.Cost,
	}
	if sess := s.a.Session; sess != nil {
		d["sessionFile"], d["sessionId"] = sess.Path, sess.ID
	} else {
		d["sessionId"] = ""
	}
	if st.ContextWindow > 0 {
		d["contextUsage"] = map[string]any{"tokens": st.ContextTokens, "contextWindow": st.ContextWindow,
			"percent": float64(st.ContextTokens) * 100 / float64(st.ContextWindow)}
	}
	return d
}

// entries reads the session file as stored: every entry in append order (the header left out), and the id of the current leaf.
func (s *rpcServer) entries() ([]json.RawMessage, any) {
	out := []json.RawMessage{}
	if s.a.Session == nil {
		return out, nil
	}
	raw, err := s.a.Session.Raw()
	if err != nil {
		return out, nil
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		var head struct{ Type string }
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &head) != nil || head.Type == "session" {
			continue
		}
		out = append(out, json.RawMessage(bytes.TrimSpace(line)))
	}
	if len(out) == 0 {
		return out, nil
	}
	return out, s.a.Session.Leaf()
}

func idOf(e json.RawMessage) (id string, parent *string) {
	var h struct {
		ID       string  `json:"id"`
		ParentID *string `json:"parentId"`
	}
	_ = json.Unmarshal(e, &h)
	return h.ID, h.ParentID
}

func indexOfEntry(entries []json.RawMessage, id string) int {
	for i, e := range entries {
		if eid, _ := idOf(e); eid == id {
			return i
		}
	}
	return -1
}

// buildTree arranges entries as Pi's get_tree does: nodes {entry, children, label?, labelTimestamp?}. An entry whose parent is missing is a root.
func buildTree(entries []json.RawMessage) []any {
	type node struct {
		Entry          json.RawMessage `json:"entry"`
		Children       []*node         `json:"children"`
		Label          string          `json:"label,omitempty"`
		LabelTimestamp string          `json:"labelTimestamp,omitempty"`
	}
	nodes := map[string]*node{}
	for _, e := range entries {
		id, _ := idOf(e)
		nodes[id] = &node{Entry: e, Children: []*node{}}
	}
	var roots []*node
	for _, e := range entries {
		id, parent := idOf(e)
		var h struct {
			Type, TargetID, Label, Timestamp string
		}
		_ = json.Unmarshal(e, &h)
		if h.Type == "label" {
			if t := nodes[h.TargetID]; t != nil {
				t.Label, t.LabelTimestamp = h.Label, h.Timestamp
			}
		}
		if parent != nil && nodes[*parent] != nil {
			nodes[*parent].Children = append(nodes[*parent].Children, nodes[id])
		} else {
			roots = append(roots, nodes[id])
		}
	}
	out := make([]any, 0, len(roots))
	for _, r := range roots {
		out = append(out, r)
	}
	return out
}

// userText is the text of a user message entry (what /fork puts back in the editor); "" for any other entry.
func userText(e session.Entry) string {
	_, text := session.Destination(e)
	return text
}

// forkMessages lists the user messages on the active branch, oldest first, as {entryId, text}.
func (s *rpcServer) forkMessages() []any {
	out := []any{}
	sess := s.a.Session
	if sess == nil {
		return out
	}
	entries, _ := s.entries()
	byID := map[string]json.RawMessage{}
	for _, e := range entries {
		id, _ := idOf(e)
		byID[id] = e
	}
	var chain []string
	for id := sess.Leaf(); id != ""; {
		e, ok := byID[id]
		if !ok {
			break
		}
		chain = append([]string{id}, chain...)
		_, p := idOf(e)
		if p == nil {
			break
		}
		id = *p
	}
	for _, id := range chain {
		if e, ok := sess.Entry(id); ok {
			if t := userText(e); t != "" {
				out = append(out, map[string]any{"entryId": id, "text": t})
			}
		}
	}
	return out
}

// lastAssistantText is the text of the newest assistant message that has any, or nil.
func (s *rpcServer) lastAssistantText() any {
	h := s.a.Agent.History
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Role != "assistant" {
			continue
		}
		var parts []string
		for _, b := range h[i].Content {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return nil
}
