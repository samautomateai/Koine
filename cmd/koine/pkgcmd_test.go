package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNPM is a stand-in `npm` first on PATH: `npm install --prefix P -- name@spec` copies <registry>/<name with / as _>/<spec> into
// P/node_modules/<name>; "latest" reads the version from <registry>/<name>/latest. No network is involved.
func fakeNPM(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake npm is a shell script")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
prefix=.; spec=
while [ $# -gt 0 ]; do
  case "$1" in --prefix) prefix=$2; shift;; --) spec=$2; shift;; esac
  shift
done
[ -z "$spec" ] && exit 0
name=${spec%@*}; ver=${spec##*@}
flat=$(echo "$name" | tr / _)
if [ "$ver" = latest ]; then ver=$(cat "$FAKE_NPM_REGISTRY/$flat/latest") || exit 1; fi
src="$FAKE_NPM_REGISTRY/$flat/$ver"
[ -d "$src" ] || { echo "npm ERR! 404 $spec" >&2; exit 1; }
rm -rf "$prefix/node_modules/$name"
mkdir -p "$prefix/node_modules/$name"
cp -R "$src/." "$prefix/node_modules/$name/"
`
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := filepath.Join(dir, "registry")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NPM_REGISTRY", reg)
	return reg
}

func put(t *testing.T, root string, files map[string]string) {
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

func publishVersion(t *testing.T, reg, version, changelog string) {
	t.Helper()
	put(t, filepath.Join(reg, "@acme_tools", version), map[string]string{
		"package.json":        `{"name":"@acme/tools","version":"` + version + `","pi":{"extensions":["./ext/main.ts"],"skills":["./skills"]}}`,
		"ext/main.ts":         "export default function () {}",
		"skills/one/SKILL.md": "---\nname: one\ndescription: d\n---\n",
		"skills/two/SKILL.md": "---\nname: two\ndescription: d\n---\n",
		"CHANGELOG.md":        changelog,
	})
	put(t, filepath.Join(reg, "@acme_tools"), map[string]string{"latest": version})
}

// pkgEnv isolates HOME and returns it with a project directory.
func pkgEnv(t *testing.T) (home, proj string) {
	t.Helper()
	home, proj = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KOINE_AGENT_DIR", "")
	t.Setenv("KOINE_TRUST", "")
	return home, proj
}

func TestInstallListUpdateRemoveACycle(t *testing.T) {
	reg := fakeNPM(t)
	publishVersion(t, reg, "1.0.0", "## 1.0.0\n- first\n")
	home, proj := pkgEnv(t)

	code, out, errs := cli("", "install", "-C", proj, "npm:@acme/tools")
	if code != 0 || !strings.Contains(out, "installed npm:@acme/tools (user) 1.0.0: 1 extensions, 2 skills, 0 prompts, 0 themes") {
		t.Fatalf("install: %d\n%s\n%s", code, out, errs)
	}
	settings, _ := os.ReadFile(filepath.Join(home, ".koine", "settings.json"))
	if !strings.Contains(string(settings), `"npm:@acme/tools"`) {
		t.Errorf("settings: %s", settings)
	}
	if _, err := os.Stat(filepath.Join(home, ".koine", "packages.lock.json")); err != nil {
		t.Errorf("lockfile: %v", err)
	}

	code, out, _ = cli("", "list", "-C", proj)
	if code != 0 || !strings.Contains(out, "user packages") || !strings.Contains(out, "npm:@acme/tools 1.0.0: 1/1 extensions, 2/2 skills") ||
		!strings.Contains(out, "project packages: none") {
		t.Errorf("list: %d\n%s", code, out)
	}

	publishVersion(t, reg, "1.1.0", "## 1.1.0\n- shiny new thing\n\n## 1.0.0\n- first\n")
	code, out, errs = cli("", "update", "-C", proj)
	if code != 0 || !strings.Contains(out, "updated npm:@acme/tools (user) 1.0.0 -> 1.1.0") {
		t.Fatalf("update: %d\n%s\n%s", code, out, errs)
	}
	code, out, _ = cli("", "update", "-C", proj, "npm:@acme/tools")
	if code != 0 || !strings.Contains(out, "up to date") || strings.Contains(out, "Extension Updates") {
		t.Errorf("second update: %d\n%s", code, out)
	}
	if code, _, errs = cli("", "update", "-C", proj, "npm:ghost"); code != 1 || !strings.Contains(errs, "not in the user or project packages") {
		t.Errorf("unknown package: %d %s", code, errs)
	}

	code, out, _ = cli("", "remove", "-C", proj, "npm:@acme/tools")
	if code != 0 || !strings.Contains(out, "removed npm:@acme/tools") {
		t.Errorf("remove: %d %s", code, out)
	}
	if code, _, errs = cli("", "remove", "-C", proj, "npm:@acme/tools"); code != 1 || !strings.Contains(errs, "not in the user packages") {
		t.Errorf("removing again: %d %s", code, errs)
	}
}

func TestUpdateShowsTheChangelogOfUpdatedPackages(t *testing.T) {
	reg := fakeNPM(t)
	publishVersion(t, reg, "1.0.0", "## 1.0.0\n- first\n")
	home, proj := pkgEnv(t)
	if code, _, errs := cli("", "install", "-C", proj, "npm:@acme/tools"); code != 0 {
		t.Fatal(errs)
	}
	log := "## [1.2.0] - 2026-02-01\n- third\n\n## 1.1.0\n- shiny new thing\n\n## 1.0.0\n- first\n"
	publishVersion(t, reg, "1.2.0", log)
	code, out, _ := cli("", "update", "-C", proj)
	if code != 0 || !strings.Contains(out, "Extension Updates") || !strings.Contains(out, "@acme/tools 1.0.0 -> 1.2.0") ||
		!strings.Contains(out, "shiny new thing") || !strings.Contains(out, "- third") || strings.Contains(out, "- first") {
		t.Fatalf("the sections newer than the old version are shown:\n%s", out)
	}

	// collapseChangelog: one line per package
	put(t, filepath.Join(home, ".koine"), map[string]string{"settings.json": `{"collapseChangelog": true, "packages": ["npm:@acme/tools"]}`})
	publishVersion(t, reg, "1.3.0", "## 1.3.0\n- x\n"+log)
	code, out, _ = cli("", "update", "-C", proj)
	if code != 0 || !strings.Contains(out, "@acme/tools 1.2.0 -> 1.3.0") || strings.Contains(out, "- x") {
		t.Errorf("collapsed:\n%s", out)
	}
}

func TestPinnedPackagesAreLeftAlone(t *testing.T) {
	reg := fakeNPM(t)
	publishVersion(t, reg, "1.0.0", "")
	_, proj := pkgEnv(t)
	if code, _, errs := cli("", "install", "-C", proj, "npm:@acme/tools@1.0.0"); code != 0 {
		t.Fatal(errs)
	}
	publishVersion(t, reg, "2.0.0", "")
	code, out, _ := cli("", "update", "-C", proj)
	if code != 0 || !strings.Contains(out, "pinned") {
		t.Errorf("%d\n%s", code, out)
	}
	if _, out, _ = cli("", "list", "-C", proj); !strings.Contains(out, "npm:@acme/tools@1.0.0 1.0.0 pinned") {
		t.Errorf("list: %s", out)
	}
}

func TestProjectPackagesNeedATrustedFolder(t *testing.T) {
	_, proj := pkgEnv(t)
	pkg := filepath.Join(t.TempDir(), "p")
	put(t, pkg, map[string]string{"package.json": `{"name":"p","version":"0.1.0"}`, "prompts/a.md": "hi"})

	code, _, errs := cli("", "install", "-C", proj, "-l", pkg)
	if code != 1 || !strings.Contains(errs, "not trusted") {
		t.Fatalf("untrusted install -l: %d %s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(proj, ".koine")); err == nil {
		t.Error("nothing may be written into an untrusted project")
	}
	if code, _, errs = cli("", "remove", "-C", proj, "-l", pkg); code != 1 || !strings.Contains(errs, "not trusted") {
		t.Errorf("remove: %d %s", code, errs)
	}
	if code, _, errs = cli("", "update", "-C", proj, "-l"); code != 1 || !strings.Contains(errs, "not trusted") {
		t.Errorf("update -l: %d %s", code, errs)
	}

	code, out, errs := cli("", "install", "-C", proj, "-l", "--trust", pkg)
	if code != 0 || !strings.Contains(out, "(project)") {
		t.Fatalf("trusted install -l: %d\n%s\n%s", code, out, errs)
	}
	settings, _ := os.ReadFile(filepath.Join(proj, ".koine", "settings.json"))
	if !strings.Contains(string(settings), pkg) { // outside the project: stored absolute
		t.Errorf("settings: %s", settings)
	}

	// listing needs no trust, and says the packages are not loaded
	code, out, _ = cli("", "list", "-C", proj)
	if code != 0 || !strings.Contains(out, "project packages") || !strings.Contains(out, "not trusted") {
		t.Errorf("list: %d\n%s", code, out)
	}
	// the user's update skips the project's packages and says so
	code, _, errs = cli("", "update", "-C", proj)
	if code != 0 || !strings.Contains(errs, "skipped: this folder is not trusted") {
		t.Errorf("update: %d %s", code, errs)
	}
}

func TestConfigEnablesAndDisablesResources(t *testing.T) {
	home, proj := pkgEnv(t)
	pkg := filepath.Join(t.TempDir(), "p")
	put(t, pkg, map[string]string{
		"package.json": `{"name":"p","version":"0.1.0"}`, "skills/a/SKILL.md": "---\nname: a\ndescription: d\n---\n",
		"skills/b/SKILL.md": "---\nname: b\ndescription: d\n---\n", "prompts/x.md": "x",
	})
	if code, _, errs := cli("", "install", "-C", proj, pkg); code != 0 {
		t.Fatal(errs)
	}
	code, out, _ := cli("", "config", "-C", proj)
	if code != 0 || !strings.Contains(out, "[x] skills     skills/a") || !strings.Contains(out, "[x] prompts    prompts/x.md") {
		t.Fatalf("config:\n%s", out)
	}
	if code, out, errs := cli("", "config", "-C", proj, "disable", pkg, "skills/b"); code != 0 || !strings.Contains(out, "disabled skills/b") {
		t.Fatalf("disable: %d %s %s", code, out, errs)
	}
	_, out, _ = cli("", "config", "-C", proj)
	if !strings.Contains(out, "[ ] skills     skills/b") || !strings.Contains(out, "[x] skills     skills/a") {
		t.Errorf("after disable:\n%s", out)
	}
	settings, _ := os.ReadFile(filepath.Join(home, ".koine", "settings.json"))
	if !strings.Contains(string(settings), `"-skills/b"`) || !strings.Contains(string(settings), `"source"`) {
		t.Errorf("the entry becomes an object with a -path filter:\n%s", settings)
	}
	if code, _, _ = cli("", "config", "-C", proj, "enable", pkg, "skills/b"); code != 0 {
		t.Fatal("enable")
	}
	settings, _ = os.ReadFile(filepath.Join(home, ".koine", "settings.json"))
	if strings.Contains(string(settings), "skills/b") {
		t.Errorf("enabling again removes the filter:\n%s", settings)
	}
	for _, bad := range [][]string{{"enable"}, {"toggle", pkg, "x"}, {"enable", pkg, "nope"}} {
		if code, _, _ := cli("", append([]string{"config", "-C", proj}, bad...)...); code == 0 {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestConfigInTheTerminalTogglesByNumber(t *testing.T) {
	_, proj := pkgEnv(t)
	pkg := filepath.Join(t.TempDir(), "p")
	put(t, pkg, map[string]string{"package.json": `{"name":"p"}`, "prompts/x.md": "x", "prompts/y.md": "y"})
	if code, _, errs := cli("", "install", "-C", proj, pkg); code != 0 {
		t.Fatal(errs)
	}
	var o, e bytes.Buffer
	st := streams{In: strings.NewReader("2\nzzz\n9\n\n"), Out: &o, Err: &e, InTTY: true}
	code, _ := packageCommand([]string{"config", "-C", proj}, st)
	out := o.String()
	if code != 0 || strings.Count(out, "number to toggle") != 4 || !strings.Contains(out, `no resource "zzz"`) || !strings.Contains(out, `no resource "9"`) {
		t.Fatalf("%d\n%s\n%s", code, out, e.String())
	}
	if !strings.Contains(out, "2 [ ] prompts    prompts/y.md") && !strings.Contains(out, "  2 [ ] prompts    prompts/y.md") {
		t.Errorf("toggling 2 should show prompts/y.md off in the next listing:\n%s", out)
	}
	// end of input also finishes
	o.Reset()
	st.In = strings.NewReader("")
	if code, _ := packageCommand([]string{"config", "-C", proj}, st); code != 0 {
		t.Errorf("EOF: %d", code)
	}
}

func TestPackageCommandLineErrors(t *testing.T) {
	pkgEnv(t)
	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"install"}, 2, "needs a package source"},
		{[]string{"remove"}, 2, "needs a package source"},
		{[]string{"install", "--bogus", "x"}, 2, "unknown option --bogus"},
		{[]string{"install", "-C"}, 2, "needs a directory"},
		{[]string{"update", "a", "b"}, 2, "at most one"},
		{[]string{"install", "left-pad"}, 1, "unrecognised package source"},
		{[]string{"install", "git:ext::sh -c x"}, 1, "install"},
	} {
		code, _, errs := cli("", c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("%v: exit %d, %q", c.args, code, errs)
		}
	}
	code, out, _ := cli("", "install", "-h")
	if code != 0 || !strings.Contains(out, "koine install") || !strings.Contains(out, "--allow-scripts") {
		t.Errorf("help: %d %s", code, out)
	}
	if code, out, _ := cli("", "--help"); code != 0 || !strings.Contains(out, "Pi packages") {
		t.Errorf("main help mentions packages: %d", code)
	}
}

func TestNoNPMGivesAClearError(t *testing.T) {
	pkgEnv(t)
	t.Setenv("PATH", t.TempDir())
	code, _, errs := cli("", "install", "npm:something")
	if code != 1 || !strings.Contains(errs, "npm is needed") || !strings.Contains(errs, "npmCommand") {
		t.Errorf("%d %s", code, errs)
	}
}
