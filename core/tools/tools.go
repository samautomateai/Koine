// Package tools is the tool registry plus the built-in coding tools.
package tools

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"sync"
)

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Prompted is implemented by tools that add to the system prompt: a one-line entry in its <tools> section and rule bullets in <rules>.
// A tool without a snippet is left out of the tools list (Pi's promptSnippet and promptGuidelines).
type Prompted interface {
	PromptSnippet() string
	PromptGuidelines() []string
}

// Func is a Tool backed by a Go function.
type Func struct {
	N, D string
	S    json.RawMessage
	F    func(ctx context.Context, args json.RawMessage) (string, error)
	// Snippet and Guidelines feed the system prompt (see Prompted). Each guideline should name the tool it is about.
	Snippet    string
	Guidelines []string
}

func (f *Func) Name() string                                               { return f.N }
func (f *Func) Description() string                                        { return f.D }
func (f *Func) Schema() json.RawMessage                                    { return f.S }
func (f *Func) Run(ctx context.Context, a json.RawMessage) (string, error) { return f.F(ctx, a) }
func (f *Func) PromptSnippet() string                                      { return f.Snippet }
func (f *Func) PromptGuidelines() []string                                 { return f.Guidelines }

// PromptParts collects what the registered tools add to the system prompt: the "- name: snippet" lines of the tools that have a snippet, and
// their guideline bullets without duplicates. Built-ins come first in Pi's order, then the others alphabetically.
func (r *Registry) PromptParts() (toolLines, rules []string) {
	names := r.Names()
	rank := map[string]int{}
	for i, n := range BuiltinNames {
		rank[n] = i + 1
	}
	sort.SliceStable(names, func(i, j int) bool {
		ri, rj := rank[names[i]], rank[names[j]]
		if ri == 0 || rj == 0 {
			return ri != 0 && rj == 0
		}
		return ri < rj
	})
	seen := map[string]bool{}
	for _, n := range names {
		t, _ := r.Get(n)
		p, ok := t.(Prompted)
		if !ok {
			continue
		}
		if s := strings.TrimSpace(p.PromptSnippet()); s != "" {
			toolLines = append(toolLines, "- "+n+": "+s)
		}
		for _, g := range p.PromptGuidelines() {
			if g = strings.TrimSpace(g); g != "" && !seen[g] {
				seen[g] = true
				rules = append(rules, "- "+g)
			}
		}
	}
	return toolLines, rules
}

type Registry struct {
	mu     sync.RWMutex
	m      map[string]Tool
	hidden map[string]bool // registered but not offered: Names and Get skip them, Filtered copies still see them
}

func NewRegistry() *Registry { return &Registry{m: map[string]Tool{}} }

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	r.m[t.Name()] = t
	delete(r.hidden, t.Name()) // a tool registered later under the name is a deliberate choice
	r.mu.Unlock()
}

// Hide keeps the named tools registered but out of Names and Get, so the model is not offered them. Registering a tool again unhides it.
func (r *Registry) Hide(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hidden == nil {
		r.hidden = map[string]bool{}
	}
	for _, n := range names {
		r.hidden[n] = true
	}
}

func (r *Registry) Remove(name string) {
	r.mu.Lock()
	delete(r.m, name)
	r.mu.Unlock()
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.m[name]
	return t, ok && !r.hidden[name]
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		if !r.hidden[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Filtered returns a copy honouring an allow-list (empty = all) and deny-list.
func (r *Registry) Filtered(allow, deny []string) *Registry {
	out := NewRegistry()
	ok := map[string]bool{}
	for _, a := range allow {
		ok[Canon(a)] = true
	}
	no := map[string]bool{}
	for _, d := range deny {
		no[Canon(d)] = true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for n, t := range r.m {
		if no[n] || (len(ok) > 0 && !ok[n]) {
			continue
		}
		out.m[n] = t
	}
	return out
}

var aliases = map[string]string{"multiedit": "edit", "notebookedit": "edit", "patch": "edit", "list": "ls", "glob": "find", "websearch": "", "webfetch": ""}

// Canon maps Claude/opencode style tool names (Read, Bash, MultiEdit …) to ours.
func Canon(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if a, ok := aliases[n]; ok {
		return a
	}
	return n
}

// Match reports whether a tool name is named by an allow or deny entry: a plain name (Claude and opencode spellings accepted, as in Canon)
// or a case-insensitive glob where * and ? stand for any characters, such as "mcp__radius__*".
func Match(entry, name string) bool {
	e := strings.ToLower(strings.TrimSpace(entry))
	if strings.ContainsAny(e, "*?[") {
		ok, err := path.Match(e, strings.ToLower(name))
		return err == nil && ok
	}
	return Canon(e) == name
}
