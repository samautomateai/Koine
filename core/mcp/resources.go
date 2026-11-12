package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"koine/core/tools"
)

// resourceTools are the three tools that expose MCP resources: list_mcp_resources, list_mcp_resource_templates and read_mcp_resource.
func (m *Manager) resourceTools() []tools.Tool {
	serverProp := `"server":{"type":"string","description":"Name of an MCP server; omit to use every server"}`
	return []tools.Tool{
		&tools.Func{N: ListResourcesName, D: "List the resources (files, records, documents) that connected MCP servers offer.",
			S:       json.RawMessage(`{"type":"object","properties":{` + serverProp + `}}`),
			F:       m.listResources,
			Snippet: "List resources offered by connected MCP servers"},
		&tools.Func{N: ListTemplatesName, D: "List the resource templates (URI patterns) that connected MCP servers offer.",
			S:       json.RawMessage(`{"type":"object","properties":{` + serverProp + `}}`),
			F:       m.listTemplates,
			Snippet: "List resource templates offered by connected MCP servers"},
		&tools.Func{N: ReadResourceName, D: "Read one resource from an MCP server by its URI. Text is returned as text and images as images.",
			S: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"Name of the MCP server"},` +
				`"uri":{"type":"string","description":"URI of the resource"}},"required":["server","uri"]}`),
			F:          m.readResource,
			Snippet:    "Read a resource from an MCP server by URI",
			Guidelines: []string{"Use list_mcp_resources to find a resource's URI before calling read_mcp_resource"}},
	}
}

// withResources runs fn for each connected server with resources (or just the named one).
func (m *Manager) withResources(ctx context.Context, name string, fn func(s *Server, c *Client) error) error {
	m.mu.Lock()
	var list []*Server
	for n, s := range m.servers {
		if name == "" || n == name {
			list = append(list, s)
		}
	}
	m.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	if name != "" && len(list) == 0 {
		return fmt.Errorf("no MCP server named %q", name)
	}
	ran := 0
	var errs []string
	for _, s := range list {
		s.mu.Lock()
		has := s.resCap
		s.mu.Unlock()
		if !has {
			if name != "" {
				return fmt.Errorf("MCP server %q does not offer resources", name)
			}
			continue
		}
		c, err := s.ready(ctx)
		if err == nil {
			err = retryOnce(func() error { return fn(s, c) })
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", s.Name, err))
			continue
		}
		ran++
	}
	if len(errs) > 0 && ran == 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("partial result; %s", strings.Join(errs, "; "))
	}
	return nil
}

// retryOnce repeats fn once after a transient failure (not after an error the server itself returned). Listing and reading are safe to
// repeat; tool calls are not and never use this.
func retryOnce(fn func() error) error {
	err := fn()
	var rpc *RPCError
	if err != nil && !errors.As(err, &rpc) && !errors.Is(err, ErrTimeout) {
		err = fn()
	}
	return err
}

func serverArg(args json.RawMessage) string {
	var in struct {
		Server string `json:"server"`
	}
	_ = json.Unmarshal(args, &in)
	return in.Server
}

func (m *Manager) listResources(ctx context.Context, args json.RawMessage) (string, error) {
	var sb strings.Builder
	err := m.withResources(ctx, serverArg(args), func(s *Server, c *Client) error {
		rs, err := c.ListResources(ctx)
		if err != nil {
			return err
		}
		for _, r := range rs {
			if strings.HasPrefix(r.URI, "ui://") { // MCP Apps resources are for a rich UI, not for the model (as in Pi)
				continue
			}
			fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\t%s\n", s.Name, r.URI, r.Name, r.MimeType, firstLine(r.Description, 160))
		}
		return nil
	})
	return finishList(&sb, "server\turi\tname\tmime type\tdescription", "resources", err)
}

func (m *Manager) listTemplates(ctx context.Context, args json.RawMessage) (string, error) {
	var sb strings.Builder
	err := m.withResources(ctx, serverArg(args), func(s *Server, c *Client) error {
		ts, err := c.ListResourceTemplates(ctx)
		if err != nil {
			return err
		}
		for _, r := range ts {
			fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\t%s\n", s.Name, r.URITemplate, r.Name, r.MimeType, firstLine(r.Description, 160))
		}
		return nil
	})
	return finishList(&sb, "server\turi template\tname\tmime type\tdescription", "resource templates", err)
}

func finishList(sb *strings.Builder, header, what string, err error) (string, error) {
	if sb.Len() == 0 {
		if err != nil {
			return "", err
		}
		return "No " + what + ".", nil
	}
	out := header + "\n" + strings.TrimRight(sb.String(), "\n")
	if err != nil {
		out += "\n[" + err.Error() + "]"
	}
	return LimitText(out), nil
}

func (m *Manager) readResource(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct{ Server, URI string }
	if err := json.Unmarshal(args, &in); err != nil || in.Server == "" || in.URI == "" {
		return "", errors.New("read_mcp_resource needs server and uri")
	}
	var res CallResult
	err := m.withResources(ctx, in.Server, func(_ *Server, c *Client) error {
		contents, err := c.ReadResource(ctx, in.URI)
		if err != nil {
			return err
		}
		res = CallResult{}
		for i := range contents {
			if contents[i].URI == "" {
				contents[i].URI = in.URI
			}
			res.Content = append(res.Content, Content{Type: "resource", Resource: &contents[i]})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(res.Content) == 0 {
		return "The resource is empty.", nil
	}
	return deliver(ctx, Render(&res))
}
