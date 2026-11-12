package pkgs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rels(rs []Resource) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Rel)
	}
	return out
}

func TestGlobMatching(t *testing.T) {
	yes := map[string]string{
		"extensions/*.ts": "extensions/a.ts", "**/*.md": "a/b/c.md", "**": "x/y", "a/**/z": "a/z", "a/**/z.md": "a/b/c/z.md",
		"./prompts/*.md": "prompts/r.md", "skills/*": "skills/one", "x?.ts": "xa.ts", "[ab].ts": "a.ts",
	}
	for p, n := range yes {
		if !matchGlob(p, n) {
			t.Errorf("%q should match %q", p, n)
		}
	}
	no := map[string]string{"extensions/*.ts": "extensions/sub/a.ts", "*.md": "a/b.md", "a/**/z": "a/zz", "[": "a"}
	for p, n := range no {
		if matchGlob(p, n) {
			t.Errorf("%q should not match %q", p, n)
		}
	}
}

func TestFilterSemantics(t *testing.T) {
	mk := func(rels ...string) []Resource {
		var out []Resource
		for _, r := range rels {
			out = append(out, Resource{Path: "/pkg/" + r, Rel: r})
		}
		return out
	}
	all := mk("extensions/a.ts", "extensions/b.ts", "extensions/legacy.ts", "prompts/review.md", "prompts/fix.md")
	cases := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"omitted loads everything", nil, rels(all)},
		{"empty list loads none", []string{}, nil},
		{"glob include", []string{"extensions/*.ts"}, []string{"extensions/a.ts", "extensions/b.ts", "extensions/legacy.ts"}},
		{"glob include with exclusion", []string{"extensions/*.ts", "!extensions/legacy.ts"}, []string{"extensions/a.ts", "extensions/b.ts"}},
		{"only exclusions start from everything", []string{"!extensions/legacy.ts", "!prompts/*"}, []string{"extensions/a.ts", "extensions/b.ts"}},
		{"+path adds back an excluded path", []string{"extensions/*.ts", "!extensions/*", "+extensions/b.ts"}, []string{"extensions/b.ts"}},
		{"-path removes one exact path", []string{"-extensions/a.ts"},
			[]string{"extensions/b.ts", "extensions/legacy.ts", "prompts/review.md", "prompts/fix.md"}},
		{"-path has the last word", []string{"prompts/review.md", "+prompts/review.md", "-prompts/review.md"}, nil},
		{"a bare name matches the file name anywhere", []string{"review.md"}, []string{"prompts/review.md"}},
		{"nothing outside the package is ever added", []string{"+elsewhere/x.ts"}, rels(all)},
		{"./ and backslashes are tolerated in exact paths", []string{"prompts/fix.md", "-./prompts/fix.md"}, nil},
	}
	for _, c := range cases {
		if got := rels(Filter(all, c.patterns)); !reflect.DeepEqual(got, c.want) && (len(got) != 0 || len(c.want) != 0) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	skill := []Resource{
		{Path: "/pkg/skills/a/SKILL.md", Rel: "skills/a", Alt: "skills/a/SKILL.md"},
		{Path: "/pkg/skills/b/SKILL.md", Rel: "skills/b", Alt: "skills/b/SKILL.md"},
	}
	if got := rels(Filter(skill, []string{"-skills/a/SKILL.md"})); !reflect.DeepEqual(got, []string{"skills/b"}) {
		t.Errorf("a skill can be named by its SKILL.md: %v", got)
	}
	if got := rels(Filter(skill, []string{"skills/a"})); !reflect.DeepEqual(got, []string{"skills/a"}) {
		t.Errorf("or by its directory: %v", got)
	}
}

func TestConventionalDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"extensions/one.ts":                 "export default () => {}",
		"extensions/types.d.ts":             "declare const x: number",
		"extensions/dir/index.js":           "",
		"extensions/notes.txt":              "",
		"extensions/empty/readme.md":        "",
		"skills/s1/SKILL.md":                "---\nname: s1\ndescription: d\n---\n",
		"skills/loose.md":                   "---\ndescription: d\n---\n",
		"skills/s1/node_modules/x/SKILL.md": "ignored",
		"prompts/review.md":                 "r",
		"prompts/notes.txt":                 "n",
		"themes/dark.json":                  "{}",
	})
	res, problems := Resolve(root)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if got := rels(res.Extensions); !reflect.DeepEqual(got, []string{"extensions/dir", "extensions/one.ts"}) {
		t.Errorf("extensions: %v", got)
	}
	if got := rels(res.Skills); !reflect.DeepEqual(got, []string{"skills/loose.md", "skills/s1"}) {
		t.Errorf("skills: %v", got)
	}
	if !reflect.DeepEqual(rels(res.Prompts), []string{"prompts/review.md"}) || !reflect.DeepEqual(rels(res.Themes), []string{"themes/dark.json"}) {
		t.Errorf("prompts %v themes %v", rels(res.Prompts), rels(res.Themes))
	}
	if res.Count() != 6 {
		t.Errorf("count %d", res.Count())
	}
}

func TestPiManifestDeclaresResources(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"package.json": `{"name":"p","pi":{"extensions":["./src/extension.ts","src/more/*.ts","!src/more/skip.ts"],"skills":["./resources/skills"],
			"prompts":["./resources/prompts/*.md"],"themes":["../outside","/etc"]}}`,
		"src/extension.ts": "", "src/more/a.ts": "", "src/more/skip.ts": "", "src/unlisted.ts": "",
		"resources/skills/x/SKILL.md": "", "resources/prompts/p.md": "", "resources/prompts/q.txt": "",
		"extensions/ignored.ts": "", // the manifest wins over the conventional directory
	})
	res, problems := Resolve(root)
	if got := rels(res.Extensions); !reflect.DeepEqual(got, []string{"src/extension.ts", "src/more/a.ts"}) {
		t.Errorf("extensions: %v", got)
	}
	if !reflect.DeepEqual(rels(res.Skills), []string{"resources/skills/x"}) || !reflect.DeepEqual(rels(res.Prompts), []string{"resources/prompts/p.md"}) {
		t.Errorf("skills %v prompts %v", rels(res.Skills), rels(res.Prompts))
	}
	if len(res.Themes) != 0 || len(problems) != 2 {
		t.Errorf("entries outside the package must be refused and reported: themes %v problems %v", res.Themes, problems)
	}
	for _, p := range problems {
		if !strings.Contains(p, "inside the package") {
			t.Errorf("problem: %s", p)
		}
	}
}

func TestManifestWithoutResourceListsFallsBackToConvention(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"package.json": `{"name":"p","pi":{"changelogPath":"docs/C.md"}}`, "extensions/a.ts": ""})
	if res, _ := Resolve(root); len(res.Extensions) != 1 {
		t.Errorf("%v", rels(res.Extensions))
	}
	// a missing declared path is reported, not fatal
	write(t, root, map[string]string{"package.json": `{"pi":{"skills":["nope"]}}`})
	if res, problems := Resolve(root); len(res.Skills) != 0 || len(problems) != 1 || !strings.Contains(problems[0], "not found") {
		t.Errorf("%v %v", res.Skills, problems)
	}
}

func TestSubdirectoryPackageJSONListsExtensionEntries(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"extensions/with-deps/package.json": `{"name":"with-deps","pi":{"extensions":["./src/main.ts","./src/other.ts","../../escape.ts"]}}`,
		"extensions/with-deps/src/main.ts":  "", "extensions/with-deps/src/other.ts": "", "extensions/with-deps/src/helper.ts": "",
		"escape.ts": "",
	})
	dir := filepath.Join(root, "extensions", "with-deps")
	got := ExtensionEntries(dir)
	want := []string{filepath.Join(dir, "src", "main.ts"), filepath.Join(dir, "src", "other.ts")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries: %v", got)
	}
	if ExtensionEntries(root) != nil || ExtensionEntries(filepath.Join(root, "extensions")) != nil {
		t.Error("a directory without a pi.extensions list declares no entries")
	}
	if res, _ := Resolve(root); !reflect.DeepEqual(rels(res.Extensions), []string{"extensions/with-deps"}) {
		t.Errorf("the directory is one extension: %v", rels(res.Extensions))
	}
}

func TestDependencyWarnings(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"package.json": `{"name":"p","dependencies":{"typebox":"^1","left-pad":"1","@earendil-works/pi-tui":"*"},
		"peerDependencies":{"@earendil-works/pi-ai":"*"}}`})
	m, ok := ReadManifest(root)
	if !ok {
		t.Fatal("manifest")
	}
	w := DependencyWarnings(m)
	if len(w) != 2 || !strings.Contains(strings.Join(w, "\n"), "typebox") || !strings.Contains(strings.Join(w, "\n"), "pi-tui") {
		t.Errorf("warnings: %v", w)
	}
	if strings.Contains(strings.Join(w, "\n"), "pi-ai") || strings.Contains(strings.Join(w, "\n"), "left-pad") {
		t.Errorf("peer dependencies and ordinary packages are fine: %v", w)
	}
	if _, ok := ReadManifest(t.TempDir()); ok {
		t.Error("no manifest")
	}
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte("{"), 0o644)
	if _, ok := ReadManifest(root); ok {
		t.Error("broken manifest")
	}
}
