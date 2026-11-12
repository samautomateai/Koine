package pkgs

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Manifest is the part of a package.json that matters here.
type Manifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Keywords             []string          `json:"keywords"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	Pi                   *PiField          `json:"pi"`
}

// PiField is the `pi` key of package.json: where a package keeps its resources.
type PiField struct {
	Extensions    []string `json:"extensions"`
	Skills        []string `json:"skills"`
	Prompts       []string `json:"prompts"`
	Themes        []string `json:"themes"`
	ChangelogPath *string  `json:"changelogPath"`
}

func (p *PiField) declares() bool {
	return p != nil && (p.Extensions != nil || p.Skills != nil || p.Prompts != nil || p.Themes != nil)
}

// ReadManifest reads dir/package.json. ok is false when there is none or it is not valid JSON.
func ReadManifest(dir string) (m Manifest, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return Manifest{}, false
	}
	return m, true
}

// Resources is what a package supplies, after the manifest and the conventional directories are read (before entry filters).
type Resources struct {
	Extensions, Skills, Prompts, Themes []Resource
}

// Count is the number of resources of all kinds.
func (r Resources) Count() int {
	return len(r.Extensions) + len(r.Skills) + len(r.Prompts) + len(r.Themes)
}

// Resolve finds a package's resources. With a `pi` key that lists resources, those lists are the package's resources (entries are paths
// relative to the package root, with globs and !exclusions). Otherwise the conventional directories extensions/, skills/, prompts/ and
// themes/ are used. Entries that leave the package are refused and reported.
func Resolve(root string) (Resources, []string) {
	var problems []string
	m, _ := ReadManifest(root)
	lists := map[string][]string{}
	if m.Pi.declares() {
		lists["extensions"], lists["skills"], lists["prompts"], lists["themes"] = m.Pi.Extensions, m.Pi.Skills, m.Pi.Prompts, m.Pi.Themes
	} else {
		for _, k := range []string{"extensions", "skills", "prompts", "themes"} {
			if st, err := os.Stat(filepath.Join(root, k)); err == nil && st.IsDir() {
				lists[k] = []string{k}
			}
		}
	}
	paths := func(kind string) []string {
		out, bad := expandEntries(root, lists[kind])
		for _, b := range bad {
			problems = append(problems, "package.json pi."+kind+": "+b)
		}
		return out
	}
	var res Resources
	for _, p := range paths("extensions") {
		res.Extensions = append(res.Extensions, extensionCandidates(root, p)...)
	}
	for _, p := range paths("skills") {
		res.Skills = append(res.Skills, skillCandidates(root, p)...)
	}
	for _, p := range paths("prompts") {
		res.Prompts = append(res.Prompts, fileCandidates(root, p, ".md")...)
	}
	for _, p := range paths("themes") {
		res.Themes = append(res.Themes, fileCandidates(root, p, ".json")...)
	}
	res.Extensions, res.Skills, res.Prompts, res.Themes = dedupe(res.Extensions), dedupe(res.Skills), dedupe(res.Prompts), dedupe(res.Themes)
	return res, problems
}

func dedupe(in []Resource) []Resource {
	seen := map[string]bool{}
	var out []Resource
	for _, r := range in {
		if !seen[r.Path] {
			seen[r.Path] = true
			out = append(out, r)
		}
	}
	return out
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func relSlash(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

// expandEntries turns the entries of a pi.* list into existing paths inside root. Entries starting with ! remove what the others found.
func expandEntries(root string, entries []string) (paths, problems []string) {
	var drop []string
	for _, e := range entries {
		if strings.HasPrefix(e, "!") {
			drop = append(drop, e[1:])
		}
	}
	seen := map[string]bool{}
	add := func(p string) {
		rel := relSlash(root, p)
		for _, d := range drop {
			if matchGlob(d, rel) {
				return
			}
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, e := range entries {
		if strings.HasPrefix(e, "!") {
			continue
		}
		if filepath.IsAbs(e) || !within(root, filepath.Join(root, e)) {
			problems = append(problems, e+": must stay inside the package")
			continue
		}
		if !hasGlob(e) {
			p := filepath.Join(root, e)
			if _, err := os.Stat(p); err != nil {
				problems = append(problems, e+": not found")
				continue
			}
			add(p)
			continue
		}
		var hits []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			if p != root && matchGlob(e, relSlash(root, p)) {
				hits = append(hits, p)
			}
			return nil
		})
		sort.Strings(hits)
		for _, h := range hits {
			add(h)
		}
	}
	return paths, problems
}

var scriptExt = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true}

// ExtensionEntries lists the entry points a directory declares in the `pi.extensions` list of its package.json, as absolute paths. It is
// nil when the directory declares none. Entries that leave the directory or do not exist are dropped.
func ExtensionEntries(dir string) []string {
	m, ok := ReadManifest(dir)
	if !ok || m.Pi == nil || len(m.Pi.Extensions) == 0 {
		return nil
	}
	paths, _ := expandEntries(dir, m.Pi.Extensions)
	return paths
}

// isExtensionDir says whether dir is one extension: a Koine extension.json, a package.json that lists entry points, or an index file.
func isExtensionDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "extension.json")); err == nil {
		return true
	}
	if ExtensionEntries(dir) != nil {
		return true
	}
	for _, idx := range []string{"index.ts", "index.js", "index.mjs", "index.mts"} {
		if _, err := os.Stat(filepath.Join(dir, idx)); err == nil {
			return true
		}
	}
	return false
}

// extensionCandidates lists the extensions at p as units a filter can name: a script is itself, an extension directory is itself, and
// any other directory contributes its scripts and extension subdirectories (the layout of ~/.koine/extensions).
func extensionCandidates(root, p string) []Resource {
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	mk := func(path string) Resource { return Resource{Path: path, Rel: relSlash(root, path)} }
	if !st.IsDir() {
		if scriptExt[filepath.Ext(p)] && !strings.HasSuffix(p, ".d.ts") || filepath.Base(p) == "extension.json" {
			return []Resource{mk(p)}
		}
		return nil
	}
	if isExtensionDir(p) {
		return []Resource{mk(p)}
	}
	ents, _ := os.ReadDir(p)
	var out []Resource
	for _, e := range ents {
		c := filepath.Join(p, e.Name())
		if e.IsDir() {
			if isExtensionDir(c) {
				out = append(out, mk(c))
			}
		} else if scriptExt[filepath.Ext(e.Name())] && !strings.HasSuffix(e.Name(), ".d.ts") {
			out = append(out, mk(c))
		}
	}
	return out
}

// skillCandidates lists SKILL.md files under p (three levels deep, as the skill loader looks) and loose .md files directly in p.
func skillCandidates(root, p string) []Resource {
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		return []Resource{skillResource(root, p)}
	}
	var out []Resource
	base := strings.Count(p, string(filepath.Separator))
	_ = filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" || strings.Count(f, string(filepath.Separator))-base > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "SKILL.md" || (filepath.Dir(f) == p && strings.HasSuffix(d.Name(), ".md")) {
			out = append(out, skillResource(root, f))
		}
		return nil
	})
	return out
}

func skillResource(root, file string) Resource {
	r := Resource{Path: file, Rel: relSlash(root, file)}
	if filepath.Base(file) == "SKILL.md" {
		r.Rel, r.Alt = relSlash(root, filepath.Dir(file)), relSlash(root, file)
	}
	return r
}

// fileCandidates lists the files with the extension at p: p itself, or the files directly inside it.
func fileCandidates(root, p, ext string) []Resource {
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		if strings.HasSuffix(p, ext) {
			return []Resource{{Path: p, Rel: relSlash(root, p)}}
		}
		return nil
	}
	ents, _ := os.ReadDir(p)
	var out []Resource
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			f := filepath.Join(p, e.Name())
			out = append(out, Resource{Path: f, Rel: relSlash(root, f)})
		}
	}
	return out
}

// hostPackages are the packages the host provides to extensions. A package lists them in peerDependencies with the range "*"; listing
// them in dependencies installs a second copy, which gives duplicate classes and registries (Pi's packages guide).
var hostPackages = []string{
	"@earendil-works/pi-ai", "@earendil-works/pi-agent-core", "@earendil-works/pi-coding-agent", "@earendil-works/pi-tui", "typebox",
	"@mariozechner/pi-ai", "@mariozechner/pi-agent-core", "@mariozechner/pi-coding-agent", "@mariozechner/pi-tui",
}

// DependencyWarnings reports host-provided packages listed in dependencies instead of as peerDependencies "*".
func DependencyWarnings(m Manifest) []string {
	var out []string
	for _, h := range hostPackages {
		if _, bad := m.Dependencies[h]; bad {
			out = append(out, h+" is in dependencies; the host provides it, so list it in peerDependencies with the range \"*\" "+
				"(a second copy duplicates classes and registries)")
		}
	}
	return out
}
