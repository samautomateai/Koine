package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"koine/core/tools"
)

// maxToolName is the longest tool name providers accept.
const maxToolName = 64

// sanitize keeps letters, digits and _; everything else becomes _ (Pi's rule for tool names).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, s)
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:6]
}

// ToolName is the name the model sees for a server's tool: mcp__<server>__<tool>. taken says whether a name is already in use; a collision
// (or a name longer than providers allow) gets a hash of the original names as a suffix.
func ToolName(server, tool string, taken func(string) bool) string {
	name := "mcp__" + sanitize(server) + "__" + sanitize(tool)
	if len(name) <= maxToolName && !taken(name) {
		return name
	}
	suffix := "_" + shortHash(server+"\x00"+tool)
	if len(name)+len(suffix) > maxToolName {
		name = name[:maxToolName-len(suffix)]
	}
	return name + suffix
}

// Annotated is implemented by MCP tools. The hints are the server's claim, useful to a permission hook and never to be trusted as a guard.
type Annotated interface {
	MCPServer() string
	MCPAnnotations() *Annotations
}

// mcpTool is one server tool as a registry tool.
type mcpTool struct {
	name       string
	srv        *Server
	def        Tool
	snippet    string
	guidelines []string
}

var _ tools.Prompted = (*mcpTool)(nil)

func (t *mcpTool) Name() string { return t.name }

func (t *mcpTool) Description() string {
	switch {
	case t.def.Description != "":
		return t.def.Description
	case t.def.Title != "":
		return t.def.Title
	case t.def.Annotations != nil && t.def.Annotations.Title != "":
		return t.def.Annotations.Title
	}
	return "MCP tool " + t.def.Name + " from server " + t.srv.Name
}

func (t *mcpTool) Schema() json.RawMessage {
	if len(t.def.InputSchema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t.def.InputSchema
}

func (t *mcpTool) PromptSnippet() string        { return t.snippet }
func (t *mcpTool) PromptGuidelines() []string   { return t.guidelines }
func (t *mcpTool) MCPServer() string            { return t.srv.Name }
func (t *mcpTool) MCPAnnotations() *Annotations { return t.def.Annotations }
func (t *mcpTool) MCPToolName() string          { return t.def.Name }
func (t *mcpTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	res, err := t.srv.callTool(ctx, t.def.Name, args)
	if err != nil {
		return "", err
	}
	return deliver(ctx, Render(res))
}

// deliver hands a rendered result to the model: images go through the context's image sink when there is one, and an error result is
// returned as an error with its text, so the agent marks it as failed.
func deliver(ctx context.Context, r Rendered) (string, error) {
	text := r.Text
	for _, img := range r.Images {
		if !tools.AttachImage(ctx, tools.Image{Mime: img.Mime, Data: img.Data}) {
			text += "\n[an image of type " + img.Mime + " was returned but this caller cannot take images]"
		}
	}
	if r.IsError {
		if text == "" {
			text = "the MCP tool reported an error"
		}
		return text, errors.New("MCP tool error")
	}
	return text, nil
}

// firstLine is the first non-empty line of s, cut to max characters.
func firstLine(s string, max int) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if utf8.RuneCountInString(l) > max {
				r := []rune(l)
				return strings.TrimSpace(string(r[:max-1])) + "…"
			}
			return l
		}
	}
	return ""
}

func describeServer(s *Server) string {
	if d := strings.TrimSpace(s.Entry.Config.Description); d != "" {
		return firstLine(d, 200)
	}
	return firstLine(s.instructions(), 200)
}

func newTool(name string, s *Server, def Tool) *mcpTool {
	t := &mcpTool{name: name, srv: s, def: def}
	t.snippet = firstLine(t.Description(), 110)
	if d := describeServer(s); d != "" {
		t.guidelines = []string{fmt.Sprintf("Use the mcp__%s__* tools for %s", sanitize(s.Name), d)}
	}
	return t
}
