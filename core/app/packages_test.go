package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/pkgs"
	"koine/core/srcinfo"
)

// A package written the way Pi documents it: a package.json with a `pi` manifest, an any-language extension (so no JS runtime is needed), two
// skills and two prompt templates. The extension registers a tool and answers resources_discover with a skill directory of its own.
const pkgExtPy = `
import sys, json
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    m = json.loads(line); meth, id_, p = m.get("method"), m.get("id"), m.get("params", {})
    if meth == "initialize":
        send(method="register_tool", params={"name": "pkg_tool", "description": "from a package"})
        send(method="register_command", params={"name": "pkgcmd", "description": "d"})
        send(id=id_, result={"events": ["resources_discover"]})
    elif meth == "event":
        send(id=id_, result={"resources": {"skillPaths": ["more-skills"], "promptPaths": ["more-prompts"], "themePaths": ["themes"]},
                             "reason": p["payload"]["reason"]})
    elif meth == "tool.execute":
        send(id=id_, result={"content": "pkg ok"})
    elif meth == "shutdown": break
`

func skillMD(name string) string {
	return "---\nname: " + name + "\ndescription: " + name + " skill\n---\nbody of " + name + "\n"
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func samplePackage(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sample")
	writeFiles(t, root, map[string]string{
		"package.json":                  `{"name":"sample","version":"1.0.0","pi":{"extensions":["./ext"],"skills":["./skills"],"prompts":["./prompts/*.md"]}}`,
		"ext/extension.json":            `{"command": ["python3", "./ext.py"]}`,
		"ext/ext.py":                    pkgExtPy,
		"ext/more-skills/dyn/SKILL.md":  skillMD("dyn"),
		"ext/more-prompts/dynprompt.md": "dynamic prompt $ARGUMENTS",
		"skills/alpha/SKILL.md":         skillMD("alpha"),
		"skills/beta/SKILL.md":          skillMD("beta"),
		"prompts/hello.md":              "---\ndescription: hi\n---\nHello $ARGUMENTS",
		"prompts/bye.md":                "Bye $ARGUMENTS",
	})
	return root
}

func settings(t *testing.T, path, body string) {
	t.Helper()
	writeFiles(t, filepath.Dir(path), map[string]string{filepath.Base(path): body})
}

func start(t *testing.T, cwd string) *App {
	t.Helper()
	a, err := New(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func loadedNames(a *App) (skills, prompts []string) {
	for _, s := range a.Agent.Skills {
		skills = append(skills, s.Name)
	}
	for _, p := range a.Prompts {
		prompts = append(prompts, p.Name)
	}
	return skills, prompts
}

func has(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

func TestPackageResourcesFeedTheLoaders(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	pkg := samplePackage(t)
	settings(t, filepath.Join(home, ".koine", "settings.json"), `{"packages": ["`+pkg+`"]}`)
	a := start(t, t.TempDir())
	for _, w := range a.Warnings {
		t.Errorf("warning: %s", w)
	}
	sk, pr := loadedNames(a)
	for _, want := range []string{"alpha", "beta", "dyn"} {
		if !has(sk, want) {
			t.Errorf("skill %s missing: %v", want, sk)
		}
	}
	for _, want := range []string{"hello", "bye", "dynprompt"} {
		if !has(pr, want) {
			t.Errorf("prompt %s missing: %v", want, pr)
		}
	}
	if _, ok := a.Tools.Get("pkg_tool"); !ok {
		t.Fatalf("the package's extension did not start; tools: %v", a.Tools.Names())
	}
	if !has(a.Describe().Commands, "pkgcmd") || len(a.Packages()) != 1 {
		t.Errorf("commands %v packages %d", a.Describe().Commands, len(a.Packages()))
	}
}

func TestSourceInfoIsAttachedToTheLoadedThings(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	pkg := samplePackage(t)
	settings(t, filepath.Join(home, ".koine", "settings.json"), `{"packages": ["`+pkg+`"]}`)
	cwd := t.TempDir()
	writeFiles(t, cwd, map[string]string{".koine/skills/local/SKILL.md": skillMD("local")})
	a := start(t, cwd)

	want := func(kind, name string) srcinfo.Info {
		t.Helper()
		for _, p := range a.Provenance() {
			if p.Kind == kind && p.Name == name {
				return p.Source
			}
		}
		t.Fatalf("no %s %s in %+v", kind, name, a.Provenance())
		return srcinfo.Info{}
	}
	if s := want("tool", "read"); s != srcinfo.Builtin("read") {
		t.Errorf("builtin: %+v", s)
	}
	tool := want("tool", "pkg_tool")
	if tool.Source != pkg || tool.Scope != "user" || tool.Origin != "package" || tool.BaseDir != pkg || !strings.HasSuffix(tool.Path, "ext") {
		t.Errorf("package tool: %+v", tool)
	}
	if c := want("command", "pkgcmd"); c != tool {
		t.Errorf("a command carries its extension's source: %+v vs %+v", c, tool)
	}
	alpha := want("skill", "alpha")
	if alpha.Origin != "package" || alpha.Source != pkg || !strings.HasSuffix(alpha.Path, filepath.Join("alpha", "SKILL.md")) {
		t.Errorf("package skill: %+v", alpha)
	}
	if hello := want("prompt", "hello"); hello.Origin != "package" || hello.BaseDir != pkg {
		t.Errorf("package prompt: %+v", hello)
	}
	if l := want("skill", "local"); l.Origin != "top-level" || l.Scope != "project" || l.Source != "auto" {
		t.Errorf("project skill: %+v", l)
	}
	if d := want("skill", "dyn"); d.Source != "resources_discover" || d.Scope != "temporary" {
		t.Errorf("skill from resources_discover: %+v", d)
	}
}

func TestPackageFiltersAndNoFlags(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	pkg := samplePackage(t)
	settings(t, filepath.Join(home, ".koine", "settings.json"),
		`{"packages": [{"source": "`+pkg+`", "extensions": [], "skills": ["skills/*", "!skills/beta"], "prompts": ["-prompts/bye.md"]}]}`)
	a := start(t, t.TempDir())
	sk, pr := loadedNames(a)
	if !has(sk, "alpha") || has(sk, "beta") || has(sk, "dyn") {
		t.Errorf("skills: %v", sk)
	}
	if !has(pr, "hello") || has(pr, "bye") {
		t.Errorf("prompts: %v", pr)
	}
	if _, ok := a.Tools.Get("pkg_tool"); ok {
		t.Error("an empty extensions list loads no extension")
	}

	settings(t, filepath.Join(home, ".koine", "settings.json"), `{"packages": ["`+pkg+`"]}`)
	a2, err := NewWithOptions(context.Background(), t.TempDir(), Options{Load: LoadOptions{NoSkills: true, NoPromptTemplates: true, NoExtensions: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	sk, pr = loadedNames(a2)
	if len(sk) != 0 || len(pr) != 0 {
		t.Errorf("-ns and -np also switch off package resources: %v %v", sk, pr)
	}
	if _, ok := a2.Tools.Get("pkg_tool"); ok {
		t.Error("-ne also switches off package extensions")
	}
}

func TestProjectPackagesStayBehindTheTrustGate(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_TRUST", "")
	pkg := samplePackage(t)
	cwd := t.TempDir()
	settings(t, filepath.Join(cwd, ".koine", "settings.json"), `{"packages": ["`+pkg+`"]}`)
	a := start(t, cwd)
	if a.Trusted {
		t.Fatal("the folder must not be trusted")
	}
	sk, pr := loadedNames(a)
	if has(sk, "alpha") || has(pr, "hello") {
		t.Errorf("an untrusted project's package supplied skills or prompts: %v %v", sk, pr)
	}
	if _, ok := a.Tools.Get("pkg_tool"); ok {
		t.Error("an untrusted project's package ran code")
	}
	if !strings.Contains(strings.Join(a.Warnings, "\n"), "untrusted folder: 1 project") {
		t.Errorf("the skipped package must be counted in the warning: %v", a.Warnings)
	}
	// trusting the folder loads it, without a restart
	if err := a.TrustProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Tools.Get("pkg_tool"); !ok {
		t.Errorf("after /trust: %v", a.Tools.Names())
	}
}

func TestResourcesDiscoverOnReload(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	pkg := samplePackage(t)
	settings(t, filepath.Join(home, ".koine", "settings.json"), `{"packages": ["`+pkg+`"]}`)
	a := start(t, t.TempDir())
	a.Warnings = nil
	a.Reload(context.Background())
	sk, pr := loadedNames(a)
	if !has(sk, "dyn") || !has(pr, "dynprompt") {
		t.Errorf("after reload: %v %v", sk, pr)
	}
	if n := strings.Count(strings.Join(sk, ","), "dyn"); n != 1 {
		t.Errorf("a reload must not duplicate what resources_discover added: %v", sk)
	}
	if !strings.Contains(strings.Join(a.Info, "\n"), "theme path(s) from extensions ignored") {
		t.Errorf("theme paths are reported as unused: %v", a.Info)
	}
}

func TestPackageAndSourcesCommands(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	pkg := samplePackage(t)
	settings(t, filepath.Join(home, ".koine", "settings.json"), `{"packages": ["`+pkg+`"]}`)
	a := start(t, t.TempDir())
	run := func(line string) string {
		var out []string
		if _, err := a.HandleInput(context.Background(), line, func(e agent.Event) { out = append(out, e.Text) }); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "\n")
	}
	if got := run("/packages"); !strings.Contains(got, pkg+" (user): 1 extensions, 2 skills, 2 prompts, 0 themes") {
		t.Errorf("/packages: %s", got)
	}
	got := run("/sources")
	for _, want := range []string{"tool    read", "builtin", "skill   alpha", "scope=user origin=package", "command pkgcmd"} {
		if !strings.Contains(got, want) {
			t.Errorf("/sources lacks %q:\n%s", want, got)
		}
	}
	if got := run("/sources alpha"); strings.Count(got, "\n") != 0 || !strings.Contains(got, "alpha") {
		t.Errorf("/sources alpha: %s", got)
	}
	if got := run("/sources nomatch-xyz"); !strings.Contains(got, "nothing matches") {
		t.Errorf("%s", got)
	}
	if got := run("/help"); !strings.Contains(got, "/packages") {
		t.Errorf("/help: %s", got)
	}
	// without packages the command says how to get one
	b := start(t, t.TempDir())
	_ = os.Remove(filepath.Join(home, ".koine", "settings.json"))
	b.Reload(context.Background())
	var out []string
	_, _ = b.HandleInput(context.Background(), "/packages", func(e agent.Event) { out = append(out, e.Text) })
	if len(out) != 1 || !strings.Contains(out[0], "koine install") {
		t.Errorf("%v", out)
	}
}

func TestBrokenAndMissingPackagesAreReportedNotFatal(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	settings(t, filepath.Join(home, ".koine", "settings.json"), `{"packages": ["npm:never-installed@1.0.0", "./gone", "what"]}`)
	a := start(t, t.TempDir())
	joined := strings.Join(a.Warnings, "\n")
	for _, want := range []string{"npm:never-installed@1.0.0 (user) is not installed", "check the path", "unrecognised package source"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
	// a settings key the manifest now knows is not reported as unsupported
	if strings.Contains(joined, "packages is not supported yet") {
		t.Errorf("packages is supported: %s", joined)
	}
}

func TestPackageDirectoryLayoutMatchesWhatTheCLIInstalls(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	cwd := t.TempDir()
	pkg := samplePackage(t)
	m := &pkgs.Manager{Home: home, Cwd: cwd, Trusted: true}
	if _, err := m.Install(context.Background(), pkgs.User, pkg); err != nil {
		t.Fatal(err)
	}
	a := start(t, cwd)
	if sk, _ := loadedNames(a); !has(sk, "alpha") {
		t.Errorf("a package installed by the Manager is loaded by the app: %v", sk)
	}
}
