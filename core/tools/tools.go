// Package tools is the tool registry plus the built-in coding tools.
package tools

import (
	"context"
	"encoding/json"
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

// Func is a Tool backed by a Go function.
type Func struct {
	N, D string
	S    json.RawMessage
	F    func(ctx context.Context, args json.RawMessage) (string, error)
}

func (f *Func) Name() string                                               { return f.N }
func (f *Func) Description() string                                        { return f.D }
func (f *Func) Schema() json.RawMessage                                    { return f.S }
func (f *Func) Run(ctx context.Context, a json.RawMessage) (string, error) { return f.F(ctx, a) }

type Registry struct {
	mu sync.RWMutex
	m  map[string]Tool
}

func NewRegistry() *Registry { return &Registry{m: map[string]Tool{}} }

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	r.m[t.Name()] = t
	r.mu.Unlock()
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
	return t, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
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

var aliases = map[string]string{"multiedit": "edit", "notebookedit": "edit", "patch": "edit", "list": "ls", "websearch": "", "webfetch": ""}

// Canon maps Claude/opencode style tool names (Read, Bash, MultiEdit …) to ours.
func Canon(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if a, ok := aliases[n]; ok {
		return a
	}
	return n
}
