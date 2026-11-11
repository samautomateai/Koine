package compat

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed settings.json
var settingsRaw []byte

// SettingKey describes one Pi settings key and what Koine does with it. Status uses the same words as the extension manifest, plus
// "by-design" for Pi features Koine will not copy.
type SettingKey struct {
	Key    string `json:"key"`
	Type   string `json:"type"`  // alternatives separated by |: boolean, string, number, string[], object, array, or a quoted literal
	Scope  string `json:"scope"` // "global" = honoured only in the user's settings file
	Status string `json:"status"`
	Test   string `json:"test,omitempty"`
	Note   string `json:"note,omitempty"`
	// RemovedUpstream is the release in which the drift checker stopped finding this key.
	RemovedUpstream string `json:"removedUpstream,omitempty"`
}

// KoineKey is a setting that exists only in Koine.
type KoineKey struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	Note string `json:"note,omitempty"`
}

// SettingsManifest is compat/settings.json: Pi's settings schema as of a baseline release, mapped onto Koine.
type SettingsManifest struct {
	Schema   int `json:"schema"`
	Upstream struct {
		Package  string `json:"package"`
		Baseline string `json:"baseline"`
		Doc      string `json:"doc"`
	} `json:"upstream"`
	Files struct {
		Global  string   `json:"global"`
		Project string   `json:"project"`
		Legacy  []string `json:"legacy"`
	} `json:"files"`
	Keys  []SettingKey `json:"keys"`
	Koine []KoineKey   `json:"koine"`
}

var settingStatuses = map[string]bool{"verified": true, "inert": true, "unsupported": true, "todo": true, "by-design": true}

// LoadSettings parses and validates the embedded settings manifest.
func LoadSettings() (*SettingsManifest, error) {
	var m SettingsManifest
	if err := json.Unmarshal(settingsRaw, &m); err != nil {
		return nil, err
	}
	return &m, m.Validate()
}

// Validate checks the manifest is well formed: unique keys, known statuses, a test for every verified key.
func (m *SettingsManifest) Validate() error {
	seen := map[string]bool{}
	for _, k := range m.Keys {
		switch {
		case k.Key == "" || k.Type == "":
			return fmt.Errorf("settings key %+v needs a name and a type", k)
		case seen[k.Key]:
			return fmt.Errorf("settings key %s is listed twice", k.Key)
		case !settingStatuses[k.Status]:
			return fmt.Errorf("settings key %s: unknown status %q", k.Key, k.Status)
		case k.Status == "verified" && k.Test == "":
			return fmt.Errorf("settings key %s is verified but names no test", k.Key)
		case k.Scope != "" && k.Scope != "global":
			return fmt.Errorf("settings key %s: unknown scope %q", k.Key, k.Scope)
		}
		seen[k.Key] = true
	}
	for _, k := range m.Koine {
		if k.Key == "" || k.Type == "" || seen[k.Key] {
			return fmt.Errorf("koine settings key %q is empty, untyped or also a Pi key", k.Key)
		}
		seen[k.Key] = true
	}
	return nil
}

func (m *SettingsManifest) lookup(key string) (typ, status, scope string, ok bool) {
	for _, k := range m.Keys {
		if k.Key == key {
			return k.Type, k.Status, k.Scope, true
		}
	}
	for _, k := range m.Koine {
		if k.Key == key {
			return k.Type, "verified", "", true
		}
	}
	return "", "", "", false
}

func (m *SettingsManifest) hasPrefix(p string) bool {
	for _, k := range m.Keys {
		if strings.HasPrefix(k.Key, p+".") {
			return true
		}
	}
	for _, k := range m.Koine {
		if strings.HasPrefix(k.Key, p+".") {
			return true
		}
	}
	return false
}

// matchesType reports whether v fits one of the |-separated alternatives in typ.
func matchesType(typ string, v any) bool {
	for _, alt := range strings.Split(typ, "|") {
		switch {
		case alt == "any":
			return true
		case alt == "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case alt == "string":
			if _, ok := v.(string); ok {
				return true
			}
		case alt == "number":
			if _, ok := v.(float64); ok {
				return true
			}
		case alt == "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		case alt == "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case alt == "string[]":
			if a, ok := v.([]any); ok && allStrings(a) {
				return true
			}
		case strings.HasPrefix(alt, `"`):
			if s, ok := v.(string); ok && `"`+s+`"` == alt {
				return true
			}
		}
	}
	return false
}

func allStrings(a []any) bool {
	for _, x := range a {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}

// Check validates a parsed settings file and returns one message per problem: unknown keys, values of the wrong type, keys Koine does
// not act on yet, and global-only keys in a project file. File is used in messages. Inert keys are accepted silently.
func (m *SettingsManifest) Check(file string, project bool, doc map[string]any) []string {
	var out []string
	var walk func(prefix string, obj map[string]any)
	walk = func(prefix string, obj map[string]any) {
		names := make([]string, 0, len(obj))
		for k := range obj {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			key, v := prefix+n, obj[n]
			typ, status, scope, ok := m.lookup(key)
			switch {
			case ok && !matchesType(typ, v):
				out = append(out, fmt.Sprintf("%s: %s should be %s", file, key, strings.ReplaceAll(typ, "|", " or ")))
			case ok && scope == "global" && project:
				out = append(out, fmt.Sprintf("%s: %s can only be set in the user settings; ignored", file, key))
			case ok && (status == "todo" || status == "unsupported"):
				out = append(out, fmt.Sprintf("%s: %s is not supported yet; ignored", file, key))
			case ok && status == "by-design":
				out = append(out, fmt.Sprintf("%s: %s is not used by Koine by design; ignored", file, key))
			case !ok && m.hasPrefix(key):
				if sub, isObj := v.(map[string]any); isObj {
					walk(key+".", sub)
				} else {
					out = append(out, fmt.Sprintf("%s: %s should be an object", file, key))
				}
			case !ok:
				out = append(out, fmt.Sprintf("%s: unknown setting %s", file, key))
			}
		}
	}
	walk("", doc)
	return out
}
