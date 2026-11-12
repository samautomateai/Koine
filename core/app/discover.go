package app

import (
	"encoding/json"
	"koine/core/agentdir"
	"os"
	"path/filepath"
	"strings"

	"koine/core/ext"
	"koine/core/pkgs"
	"koine/core/srcinfo"
)

type extTarget struct {
	name string
	spec *ext.Spec    // non-nil: any-language extension (manifest)
	js   string       // JS target for the sidecar
	src  srcinfo.Info // set for package and -e extensions; the others are classified by where they are (sourceOf)
}

var jsExt = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true}

// dirTarget reads one extension directory: an extension.json with a command (any language), or an index.ts/js/mjs/mts (Pi and opencode).
func dirTarget(p, name string) (extTarget, bool) {
	if b, err := os.ReadFile(filepath.Join(p, "extension.json")); err == nil {
		var s ext.Spec
		if json.Unmarshal(b, &s) != nil || len(s.Command) == 0 {
			return extTarget{}, false
		}
		if s.Name == "" {
			s.Name = name
		}
		if s.Dir == "" {
			s.Dir = p // relative paths in "command" resolve against the extension folder
		}
		if strings.HasPrefix(s.Command[0], "./") {
			s.Command[0] = filepath.Join(p, s.Command[0])
		}
		return extTarget{name: s.Name, spec: &s}, true
	}
	for _, idx := range []string{"index.ts", "index.js", "index.mjs", "index.mts"} {
		if _, err := os.Stat(filepath.Join(p, idx)); err == nil {
			return extTarget{name: name, js: filepath.Join(p, idx)}, true
		}
	}
	return extTarget{}, false
}

// dirTargets is dirTarget plus the package.json form (EXT-02): a directory whose package.json lists `pi.extensions` is an extension
// package with those entry points, which is how multi-file extensions with their own dependencies are laid out. An extension.json wins
// over it and an index file comes after it. Entries that are directories are read the same way, one level down.
func dirTargets(p, name string) []extTarget { return dirTargetsDepth(p, name, 0) }

func dirTargetsDepth(p, name string, depth int) []extTarget {
	if _, err := os.Stat(filepath.Join(p, "extension.json")); err == nil {
		if t, ok := dirTarget(p, name); ok {
			return []extTarget{t}
		}
		return nil
	}
	if entries := pkgs.ExtensionEntries(p); entries != nil {
		var out []extTarget
		for _, e := range entries {
			base := strings.TrimSuffix(filepath.Base(e), filepath.Ext(e))
			if len(entries) > 1 {
				base = name + "/" + base
			} else {
				base = name
			}
			if st, err := os.Stat(e); err != nil {
				continue
			} else if st.IsDir() {
				if depth < 1 {
					out = append(out, dirTargetsDepth(e, base, depth+1)...)
				}
			} else if jsExt[filepath.Ext(e)] && !strings.HasSuffix(e, ".d.ts") {
				out = append(out, extTarget{name: base, js: e})
			}
		}
		return out
	}
	if t, ok := dirTarget(p, name); ok {
		return []extTarget{t}
	}
	return nil
}

// scanDir lists extension entry points in dir:
//   - loose *.ts/*.js files
//   - subdirs with extension.json (any-language: {"command":[...]})
//   - subdirs with a package.json that lists `pi.extensions` entry points
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
		out = append(out, dirTargets(p, e.Name())...)
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
			filepath.Join(agentdir.Of(home), "extensions"),
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
