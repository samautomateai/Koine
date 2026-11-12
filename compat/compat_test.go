package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestIsValid(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if m.Upstream.Package != "@earendil-works/pi-coding-agent" || len(m.Items) < 150 {
		t.Fatalf("%+v", m.Upstream)
	}
	c := m.Counts()
	if c["verified"] == 0 || c["todo"] == 0 {
		t.Fatalf("%v", c)
	}
}

func TestValidateRules(t *testing.T) {
	base := func(items ...Item) *Manifest {
		m := &Manifest{Items: items}
		m.Upstream.Baseline = "1.0.0"
		return m
	}
	cases := map[string]*Manifest{
		"no baseline":        {Items: []Item{{Kind: "api", Name: "x", Status: "todo"}}},
		"missing kind":       base(Item{Name: "x", Status: "todo"}),
		"duplicate":          base(Item{Kind: "api", Name: "x", Status: "todo"}, Item{Kind: "api", Name: "x", Status: "inert"}),
		"bad status":         base(Item{Kind: "api", Name: "x", Status: "done"}),
		"verified, no test":  base(Item{Kind: "api", Name: "x", Status: "verified"}),
		"todo naming a test": base(Item{Kind: "api", Name: "x", Status: "todo", Test: "t"}),
	}
	for name, m := range cases {
		if err := m.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	a := Item{Kind: "api", Name: "x", Status: "verified", Test: "t"}
	b := Item{Kind: "api", Name: "y", Status: "inert", Test: "t"}
	if err := base(a, b).Validate(); err != nil {
		t.Error(err)
	}
}

// "verified" must mean something: every named test has to exist.
func TestVerifiedItemsNameRealTests(t *testing.T) {
	m, _ := Load()
	goTests := ""
	_ = filepath.Walk("..", func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, "_test.go") {
			b, _ := os.ReadFile(p)
			goTests += string(b)
		}
		return nil
	})
	jsTests := ""
	for _, p := range []string{"../core/ext/jstest/shims.test.mjs", "../core/ext/jstest/conformance.test.mjs"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		jsTests += string(b)
	}
	for _, it := range m.Items {
		if it.Test == "" {
			continue
		}
		if i := strings.LastIndex(it.Test, "."); i > 0 { // "pkg.TestName"
			if !strings.Contains(goTests, "func "+it.Test[i+1:]+"(") {
				t.Errorf("%s:%s names missing Go test %s", it.Kind, it.Name, it.Test)
			}
		} else if !strings.Contains(jsTests, it.Test) { // JS test title or "conformance"
			t.Errorf("%s:%s names missing JS test %q", it.Kind, it.Name, it.Test)
		}
	}
}

func TestReport(t *testing.T) {
	m, _ := Load()
	r := m.Report()
	wants := []string{
		"pinned to @earendil-works/pi-coding-agent " + m.Upstream.Baseline, "verified", "todo (", "unsupported (", "observed:", "message_end",
	}
	for _, want := range wants {
		if !strings.Contains(r, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestLoadRejectsCorruptManifest(t *testing.T) {
	old := raw
	defer func() { raw = old }()
	raw = []byte("{")
	if _, err := Load(); err == nil {
		t.Error("corrupt JSON must error")
	}
}
