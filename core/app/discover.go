package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"koine/core/ext"
)

type extTarget struct {
	name string
	spec *ext.Spec // non-nil: any-language extension (manifest)
	js   string    // JS target for the sidecar
}

var jsExt = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true}

// scanDir lists extension entry points in dir:
//   - loose *.ts/*.js files
//   - subdirs with extension.json (any-language: {"command":[...]})
//   - subdirs with index.ts/js (Pi/opencode package style)
func scanDir(dir string) []extTarget {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []extTarget
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if !e.IsDir() {
			if jsExt[filepath.Ext(e.Name())] && !strings.HasSuffix(e.Name(), ".d.ts") {
				out = append(out, extTarget{name: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), js: p})
			}
			continue
		}
		if b, err := os.ReadFile(filepath.Join(p, "extension.json")); err == nil {
			var s ext.Spec
			if json.Unmarshal(b, &s) == nil && len(s.Command) > 0 {
				if s.Name == "" {
					s.Name = e.Name()
				}
				if s.Dir == "" {
					s.Dir = p // relative paths in "command" resolve against the extension folder
				}
				if strings.HasPrefix(s.Command[0], "./") {
					s.Command[0] = filepath.Join(p, s.Command[0])
				}
				out = append(out, extTarget{name: s.Name, spec: &s})
			}
			continue
		}
		for _, idx := range []string{"index.ts", "index.js", "index.mjs", "index.mts"} {
			if _, err := os.Stat(filepath.Join(p, idx)); err == nil {
				out = append(out, extTarget{name: e.Name(), js: filepath.Join(p, idx)})
				break
			}
		}
	}
	return out
}

// discoverExtensions finds extension entry points. Project-local locations are
// only scanned when trusted; skipped reports how many were left out.
func discoverExtensions(cwd, home string, cfg Config, trusted bool) (out []extTarget, skipped int) {
	projectDirs := []string{
		filepath.Join(cwd, ".koine", "extensions"),
		filepath.Join(cwd, ".pi", "extensions"),
		filepath.Join(cwd, ".opencode", "plugin"), filepath.Join(cwd, ".opencode", "plugins"),
	}
	var userDirs []string
	if home != "" {
		userDirs = []string{
			filepath.Join(home, ".koine", "extensions"),
			filepath.Join(home, ".pi", "agent", "extensions"),
			filepath.Join(home, ".config", "opencode", "plugin"), filepath.Join(home, ".config", "opencode", "plugins"),
		}
	}
	for _, d := range projectDirs {
		found := scanDir(d)
		if trusted {
			out = append(out, found...)
		} else {
			skipped += len(found)
		}
	}
	for _, d := range userDirs {
		out = append(out, scanDir(d)...)
	}

	// Pi settings.json: {"extensions": ["path", ...]}
	settings := []struct {
		path    string
		project bool
	}{{filepath.Join(cwd, ".pi", "settings.json"), true}}
	if home != "" {
		settings = append(settings, struct {
			path    string
			project bool
		}{filepath.Join(home, ".pi", "agent", "settings.json"), false})
	}
	for _, st := range settings {
		var s struct {
			Extensions []string `json:"extensions"`
		}
		if !readJSON(st.path, &s) {
			continue
		}
		if st.project && !trusted {
			skipped += len(s.Extensions)
			continue
		}
		for _, e := range s.Extensions {
			if !filepath.IsAbs(e) {
				e = filepath.Join(filepath.Dir(st.path), e)
			}
			if jsExt[filepath.Ext(e)] {
				out = append(out, extTarget{name: filepath.Base(e), js: e})
			} else {
				out = append(out, scanDir(e)...)
			}
		}
	}

	// opencode.json "plugin": local paths or npm packages installed in node_modules
	for _, pl := range cfg.Plugin {
		switch {
		case strings.HasPrefix(pl, ".") || filepath.IsAbs(pl):
			if !filepath.IsAbs(pl) {
				pl = filepath.Join(cwd, pl)
			}
			out = append(out, extTarget{name: filepath.Base(pl), js: pl})
		default:
			name := pl
			if i := strings.LastIndex(name, "@"); i > 0 { // strip @version
				name = name[:i]
			}
			out = append(out, extTarget{name: name, js: "pkg:" + name})
		}
	}

	// Explicit any-language extensions from config.
	for i := range cfg.Extensions {
		s := cfg.Extensions[i]
		out = append(out, extTarget{name: s.Name, spec: &s})
	}
	return out, skipped
}
