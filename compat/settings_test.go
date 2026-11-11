package compat

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSettingsManifestIsValid(t *testing.T) {
	m, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	c := map[string]int{}
	for _, k := range m.Keys {
		c[k.Status]++
	}
	if c["verified"] == 0 || c["inert"] == 0 || c["todo"] == 0 || c["by-design"] == 0 || len(m.Koine) == 0 {
		t.Errorf("manifest looks truncated: %v koine=%d", c, len(m.Koine))
	}
	if m.Upstream.Baseline == "" {
		t.Error("no baseline")
	}
}

func TestSettingsValidateRules(t *testing.T) {
	base := func(k SettingKey) *SettingsManifest { return &SettingsManifest{Keys: []SettingKey{k}} }
	cases := map[string]*SettingsManifest{
		"no type":          base(SettingKey{Key: "a", Status: "todo"}),
		"bad status":       base(SettingKey{Key: "a", Type: "string", Status: "weird"}),
		"verified no test": base(SettingKey{Key: "a", Type: "string", Status: "verified"}),
		"bad scope":        base(SettingKey{Key: "a", Type: "string", Status: "todo", Scope: "x"}),
		"duplicate":        {Keys: []SettingKey{{Key: "a", Type: "string", Status: "todo"}, {Key: "a", Type: "string", Status: "todo"}}},
		"koine dup":        {Keys: []SettingKey{{Key: "a", Type: "string", Status: "todo"}}, Koine: []KoineKey{{Key: "a", Type: "string"}}},
	}
	for name, m := range cases {
		if m.Validate() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSettingsCheck(t *testing.T) {
	m, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"defaultModel": "m", "theme": "dark", // fine; theme is inert
		"compaction":                 map[string]any{"enabled": true, "reserveTokens": "lots", "modelOverrides": map[string]any{}, "bogus": 1},
		"retry":                      map[string]any{"enabled": true, "provider": map[string]any{"maxRetries": 1.0}},
		"enableAnalytics":            true,
		"defaultProjectTrust":        "always",
		"quietStartup":               "header",
		"terminal":                   map[string]any{"images": false, "hyperlinks": "auto"},
		"markdown":                   "nope",
		"nonsense":                   1,
		"extensions":                 []any{"./a.ts", map[string]any{"name": "x"}},
		"defaultTools":               []any{"read", 3.0},
		"fullscreenWheelScrollLines": "auto",
	}
	got := strings.Join(m.Check("proj.json", true, doc), "\n")
	for _, want := range []string{
		"compaction.reserveTokens should be number",
		"compaction.modelOverrides is not supported yet",
		"unknown setting compaction.bogus",
		"retry.enabled is not supported yet",
		"retry.provider.maxRetries is not supported yet",
		"enableAnalytics is not used by Koine by design",
		"defaultProjectTrust can only be set in the user settings",
		"markdown should be an object",
		"unknown setting nonsense",
		"defaultTools should be string[]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	quietKeys := []string{"theme", "quietStartup", "terminal.images", "terminal.hyperlinks", "defaultModel", "fullscreenWheelScrollLines", "extensions"}
	for _, quiet := range quietKeys {
		if strings.Contains(got, ": "+quiet+" ") {
			t.Errorf("%s should be accepted silently:\n%s", quiet, got)
		}
	}
	// user file: global-only keys are fine
	if g := strings.Join(m.Check("user.json", false, map[string]any{"defaultProjectTrust": "ask"}), ""); strings.Contains(g, "only be set") {
		t.Errorf("global key in the user file: %s", g)
	}
	if len(m.Check("x", false, map[string]any{"$schema": "u", "model": "m", "maxTurns": 3.0, "hooks": map[string]any{}})) != 0 {
		t.Error("Koine keys must be accepted")
	}
}

// Every verified settings key must name a Go test that exists.
func TestVerifiedSettingsNameRealTests(t *testing.T) {
	m, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	re := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	_ = filepath.WalkDir("..", func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.HasSuffix(p, "_test.go") {
			b, _ := os.ReadFile(p)
			for _, x := range re.FindAllStringSubmatch(string(b), -1) {
				have[x[1]] = true
			}
		}
		return nil
	})
	for _, k := range m.Keys {
		if k.Status == "verified" && !have[k.Test] {
			t.Errorf("setting %s is verified by %s, which does not exist", k.Key, k.Test)
		}
	}
}
