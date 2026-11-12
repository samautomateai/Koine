package app

import (
	"path/filepath"
	"reflect"
	"testing"
)

func jsPaths(ts []extTarget) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.js)
	}
	return out
}

func TestSubdirectoryPackageJSONListsTheEntryPoints(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"single/package.json":   `{"name":"single","pi":{"extensions":["./src/main.ts"]}}`,
		"single/src/main.ts":    "",
		"single/index.ts":       "// the manifest wins over the index file",
		"multi/package.json":    `{"name":"multi","pi":{"extensions":["./a.ts","./b.mjs","./notes.txt","./types.d.ts","./nested"]}}`,
		"multi/a.ts":            "",
		"multi/b.mjs":           "",
		"multi/notes.txt":       "",
		"multi/types.d.ts":      "",
		"multi/nested/index.js": "",
		"plain/package.json":    `{"name":"plain"}`,
		"plain/index.js":        "",
		"both/package.json":     `{"name":"both","pi":{"extensions":["./x.ts"]}}`,
		"both/extension.json":   `{"command":["true"]}`,
		"both/x.ts":             "",
	})
	got := scanDir(root)
	byName := map[string][]string{}
	for _, tg := range got {
		byName[tg.name] = append(byName[tg.name], tg.js)
	}
	if want := []string{filepath.Join(root, "single", "src", "main.ts")}; !reflect.DeepEqual(byName["single"], want) {
		t.Errorf("single: %v", byName["single"])
	}
	want := []string{filepath.Join(root, "multi", "a.ts"), filepath.Join(root, "multi", "b.mjs"), filepath.Join(root, "multi", "nested", "index.js")}
	if !reflect.DeepEqual(jsPaths(filterName(got, "multi/")), want) {
		t.Errorf("multi: %v", jsPaths(filterName(got, "multi/")))
	}
	if want := []string{filepath.Join(root, "plain", "index.js")}; !reflect.DeepEqual(byName["plain"], want) {
		t.Errorf("a package.json without pi.extensions still falls back to the index file: %v", byName["plain"])
	}
	for _, tg := range got {
		if tg.name == "both" && tg.spec == nil {
			t.Errorf("extension.json wins over package.json: %+v", tg)
		}
	}
	if len(got) != 6 { // single, a, b, nested, plain, both
		t.Errorf("%d targets: %v", len(got), got)
	}
}

func filterName(ts []extTarget, prefix string) []extTarget {
	var out []extTarget
	for _, t := range ts {
		if len(t.name) >= len(prefix) && t.name[:len(prefix)] == prefix {
			out = append(out, t)
		}
	}
	return out
}

func TestExplicitExtensionDirectoryUsesPackageJSONEntries(t *testing.T) {
	isolate(t)
	dir := filepath.Join(t.TempDir(), "ext")
	writeFiles(t, dir, map[string]string{"package.json": `{"pi":{"extensions":["./one.ts","./two.ts"]}}`, "one.ts": "", "two.ts": ""})
	a := &App{Cwd: t.TempDir(), opts: Options{Load: LoadOptions{Extensions: []string{dir}}}}
	got := a.explicitExtensions()
	if want := []string{filepath.Join(dir, "one.ts"), filepath.Join(dir, "two.ts")}; !reflect.DeepEqual(jsPaths(got), want) {
		t.Errorf("%v warnings %v", jsPaths(got), a.Warnings)
	}
	// and an extension.json inside a directory given with -e still works
	writeFiles(t, dir, map[string]string{"extension.json": `{"command":["true"]}`})
	a = &App{Cwd: t.TempDir(), opts: Options{Load: LoadOptions{Extensions: []string{filepath.Join(dir, "extension.json")}}}}
	if got := a.explicitExtensions(); len(got) != 1 || got[0].spec == nil {
		t.Errorf("%+v %v", got, a.Warnings)
	}
}
