package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"koine/core/tools"
)

// Names of the tools that are not a server's own.
const (
	ToolSearchName    = "tool_search"
	ListResourcesName = "list_mcp_resources"
	ListTemplatesName = "list_mcp_resource_templates"
	ReadResourceName  = "read_mcp_resource"
)

// refreshTools reads the server's tool list and brings the registry in line with it: new tools are registered according to their exposure,
// tools that went away are removed, and tools that stay keep their names.
func (s *Server) refreshTools(ctx context.Context, c *Client) error {
	defs, err := c.ListTools(ctx)
	if err != nil {
		return err
	}
	m := s.mgr
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	s.mu.Lock()
	old := map[string]*mcpTool{}
	for _, t := range s.tools {
		old[t.def.Name] = t
	}
	s.mu.Unlock()
	seen := map[string]bool{}
	var next []*mcpTool
	for _, def := range defs {
		if seen[def.Name] || def.Name == "" {
			continue
		}
		seen[def.Name] = true
		var t *mcpTool
		if prev := old[def.Name]; prev != nil {
			t = newTool(prev.name, s, def)
		} else {
			t = newTool(ToolName(s.Name, def.Name, func(n string) bool { return m.used[n] }), s, def)
			m.used[t.name] = true
		}
		next = append(next, t)
	}
	for name, t := range old {
		if !seen[name] {
			m.unregister(t)
		}
	}
	s.mu.Lock()
	s.tools = next
	s.mu.Unlock()
	for _, t := range next {
		m.register(t, s.modeFor(t))
	}
	m.syncSearchTool()
	return nil
}

// register puts a tool in the registry according to its exposure. The caller holds m.mu.
func (m *Manager) register(t *mcpTool, mode string) {
	delete(m.deferred, t.name)
	switch mode {
	case ExposureHidden:
		m.Registry.Remove(t.name)
	case ExposureDeferred:
		m.deferred[t.name] = t
		m.Registry.Register(t)
		if !m.loaded[t.name] {
			m.Registry.Hide(t.name)
		}
	default:
		m.Registry.Register(t)
	}
}

func (m *Manager) unregister(t *mcpTool) {
	delete(m.deferred, t.name)
	delete(m.loaded, t.name)
	delete(m.used, t.name)
	m.Registry.Remove(t.name)
}

// syncSearchTool offers tool_search while some tool is deferred. The caller holds m.mu.
func (m *Manager) syncSearchTool() {
	if len(m.deferred) == 0 {
		m.Registry.Remove(ToolSearchName)
		return
	}
	m.Registry.Register(&tools.Func{N: ToolSearchName, D: m.searchDescription(), S: json.RawMessage(searchSchema), F: m.search,
		Snippet: "Find MCP tools that are not loaded yet and load them for the next step",
		Guidelines: []string{"Use tool_search to find an MCP tool by keyword before saying a capability is unavailable; " +
			"a tool it returns can be called from your next step"}})
}

const searchSchema = `{"type":"object","properties":{"query":{"type":"string","description":"Keywords describing the tool you need"},` +
	`"limit":{"type":"integer","description":"Most tools to load (default 8)"}},"required":["query"]}`

func (m *Manager) searchDescription() string {
	byServer := map[string]int{}
	for _, t := range m.deferred {
		byServer[t.srv.Name]++
	}
	var parts []string
	for name, n := range byServer {
		p := fmt.Sprintf("%s (%d tools)", name, n)
		if d := m.servers[name]; d != nil {
			if desc := describeServer(d); desc != "" {
				p = fmt.Sprintf("%s (%d tools: %s)", name, n, desc)
			}
		}
		parts = append(parts, p)
	}
	sort.Strings(parts)
	return "Search the MCP tools that are not loaded yet and load the best matches so they can be called from the next step. " +
		"Servers with unloaded tools: " + strings.Join(parts, "; ") + "."
}

type scored struct {
	t     *mcpTool
	score int
}

// search ranks the deferred tools by how many query words appear in their name (weighted) and description, and loads the best ones.
func (m *Manager) search(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("tool_search needs a query")
	}
	if in.Limit <= 0 {
		in.Limit = 8
	}
	words := strings.FieldsFunc(strings.ToLower(in.Query), func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') })
	m.mu.Lock()
	defer m.mu.Unlock()
	var hits []scored
	for _, t := range m.deferred {
		name, desc := strings.ToLower(t.name), strings.ToLower(t.Description())
		n := 0
		for _, w := range words {
			if strings.Contains(name, w) {
				n += 3
			}
			if strings.Contains(desc, w) {
				n++
			}
		}
		if n > 0 {
			hits = append(hits, scored{t, n})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].t.name < hits[j].t.name
	})
	if len(hits) > in.Limit {
		hits = hits[:in.Limit]
	}
	if len(hits) == 0 {
		return "No unloaded MCP tool matches that query.", nil
	}
	var sb strings.Builder
	sb.WriteString("Loaded; call these from your next step:\n")
	for _, h := range hits {
		m.loaded[h.t.name] = true
		m.Registry.Register(h.t) // registering shows a hidden tool
		fmt.Fprintf(&sb, "- %s: %s\n", h.t.name, firstLine(h.t.Description(), 200))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// syncResourceTools offers the resource tools while a connected server has resources.
func (m *Manager) syncResourceTools() {
	m.mu.Lock()
	defer m.mu.Unlock()
	have := false
	for _, s := range m.servers {
		s.mu.Lock()
		have = have || (s.resCap && s.client != nil)
		s.mu.Unlock()
	}
	if !have {
		for _, n := range []string{ListResourcesName, ListTemplatesName, ReadResourceName} {
			m.Registry.Remove(n)
		}
		return
	}
	for _, t := range m.resourceTools() {
		m.Registry.Register(t)
	}
}
