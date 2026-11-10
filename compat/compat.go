// Package compat holds koine's Pi-compatibility manifest: what upstream Pi
// surface exists, and honestly how far koine implements it.
//
// Statuses:
//
//	verified     implemented AND covered by a named test (the only status that may claim parity)
//	inert        accepted and harmless, but has no effect; extensions using it are told via /compat
//	unsupported  not implemented; calls resolve to a stand-in and are reported via /compat
//	todo         wanted; not implemented yet
package compat

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed pi.json
var raw []byte

type Item struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Host   string `json:"host,omitempty"`  // koine's own event name when it differs
	Shape  string `json:"shape,omitempty"` // "method" | "prop" (api/ctx/ui kinds)
	Test   string `json:"test,omitempty"`
	// Behavior is what a non-implemented property evaluates to: "undefined" (default) or "identity" (callable helpers returning their last argument).
	Behavior string `json:"behavior,omitempty"`
	Note     string `json:"note,omitempty"`
	Since    string `json:"since,omitempty"` // first upstream release that has it, when known
	// RemovedUpstream is the release in which the drift checker stopped finding this item.
	RemovedUpstream string `json:"removedUpstream,omitempty"`
}

type Manifest struct {
	Schema   int `json:"schema"`
	Upstream struct {
		Package        string   `json:"package"`
		LegacyPackage  string   `json:"legacyPackage"`
		Repo           string   `json:"repo"`
		Baseline       string   `json:"baseline"`
		BaselineSource string   `json:"baselineSource"`
		Observed       []string `json:"observed"`
	} `json:"upstream"`
	Extract map[string]any `json:"extract"`
	Items   []Item         `json:"items"`
}

var statuses = map[string]bool{"verified": true, "inert": true, "unsupported": true, "todo": true}

func Load() (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, m.Validate()
}

// Validate enforces the manifest's own rules.
func (m *Manifest) Validate() error {
	if m.Upstream.Baseline == "" {
		return fmt.Errorf("upstream.baseline is required")
	}
	seen := map[string]bool{}
	for _, it := range m.Items {
		key := it.Kind + ":" + it.Name
		switch {
		case it.Kind == "" || it.Name == "":
			return fmt.Errorf("item needs kind and name: %+v", it)
		case seen[key]:
			return fmt.Errorf("duplicate item %s", key)
		case !statuses[it.Status]:
			return fmt.Errorf("%s: unknown status %q", key, it.Status)
		case it.Status == "verified" && it.Test == "":
			return fmt.Errorf("%s: verified requires a test (that is what 'verified' means)", key)
		case it.Status != "verified" && it.Test != "" && it.Status != "inert" && it.Status != "unsupported":
			return fmt.Errorf("%s: only verified/inert/unsupported items may name a test", key)
		}
		seen[key] = true
	}
	return nil
}

// Counts returns items per status.
func (m *Manifest) Counts() map[string]int {
	c := map[string]int{}
	for _, it := range m.Items {
		c[it.Status]++
	}
	return c
}

// Report is a human-readable summary: counts, then everything not verified.
func (m *Manifest) Report() string {
	var b strings.Builder
	c := m.Counts()
	fmt.Fprintf(&b, "Pi compatibility — pinned to %s %s (%d items)\n", m.Upstream.Package, m.Upstream.Baseline, len(m.Items))
	fmt.Fprintf(&b, "  verified %d · inert %d · unsupported %d · todo %d\n", c["verified"], c["inert"], c["unsupported"], c["todo"])
	fmt.Fprintf(&b, "  source: %s\n", m.Upstream.BaselineSource)
	for _, o := range m.Upstream.Observed {
		fmt.Fprintf(&b, "  observed: %s\n", o)
	}
	for _, st := range []string{"todo", "unsupported", "inert"} {
		var names []string
		for _, it := range m.Items {
			if it.Status == st {
				names = append(names, it.Name)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			fmt.Fprintf(&b, "\n%s (%d):\n  %s\n", st, len(names), strings.Join(names, ", "))
		}
	}
	return b.String()
}
