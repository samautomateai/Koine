package pkgs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var bg = context.Background()

func pkgFiles(version string, extra map[string]string) map[string]string {
	f := map[string]string{
		"package.json":          `{"name":"@acme/tools","version":"` + version + `","pi":{"extensions":["./ext/main.ts"]}}`,
		"ext/main.ts":           "export default function () {}",
		"skills/greet/SKILL.md": "---\nname: greet\ndescription: Greets\n---\nhi",
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func entriesOf(t *testing.T, d Dirs) []Entry {
	t.Helper()
	es, _, err := ReadEntries(d.Settings)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestInstallNPMPinsRecordsAndLocks(t *testing.T) {
	reg, logf := fakeNPM(t)
	publish(t, reg, "@acme/tools", "1.0.0", pkgFiles("1.0.0", nil))
	m, home, cwd := env(t, false)
	var log strings.Builder
	m.Log = &log

	in, err := m.Install(bg, User, "npm:@acme/tools@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	d := DirsFor(User, home, cwd)
	if in.Version != "1.0.0" || !in.Pinned || !strings.HasPrefix(in.Dir, filepath.Join(d.Root, "npm")) {
		t.Errorf("installed: %+v", in)
	}
	if es := entriesOf(t, d); len(es) != 1 || es[0].Source != "npm:@acme/tools@1.0.0" {
		t.Errorf("settings: %+v", es)
	}
	lock, _ := ReadLock(d.Lock)
	e := lock.Packages["npm:@acme/tools"]
	if e.Version != "1.0.0" || !strings.HasPrefix(e.Integrity, "sha256-") || e.RegistryIntegrity != "sha512-fake" ||
		!strings.Contains(e.Resolved, "acme_tools-1.0.0") {
		t.Errorf("lock entry: %+v", e)
	}
	calls, _ := os.ReadFile(logf)
	for _, want := range []string{"--ignore-scripts", "--legacy-peer-deps", "-- @acme/tools@1.0.0"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("npm was called without %s: %s", want, calls)
		}
	}
	if !strings.Contains(log.String(), "npm install @acme/tools@1.0.0") {
		t.Errorf("progress: %q", log.String())
	}
}

func TestInstallScriptsOnlyWhenAllowed(t *testing.T) {
	reg, logf := fakeNPM(t)
	publish(t, reg, "plain", "1.0.0", map[string]string{"package.json": `{"name":"plain","version":"1.0.0"}`})
	m, _, _ := env(t, false)
	m.AllowScripts = true
	if _, err := m.Install(bg, User, "npm:plain"); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(logf)
	if strings.Contains(string(calls), "--ignore-scripts") || !strings.Contains(string(calls), "plain@latest") {
		t.Errorf("scripts were allowed and the version unpinned: %s", calls)
	}
}

func TestNPMFailuresAreClear(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "ok", "1.0.0", map[string]string{"package.json": `{"name":"ok","version":"1.0.0"}`})
	m, home, cwd := env(t, false)
	if _, err := m.Install(bg, User, "npm:missing@9.9.9"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("registry error not reported: %v", err)
	}
	if es := entriesOf(t, DirsFor(User, home, cwd)); len(es) != 0 {
		t.Errorf("a failed install must not be recorded: %+v", es)
	}
	t.Setenv("FAKE_NPM_FAIL", "boom")
	if _, err := m.Install(bg, User, "npm:ok"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("npm output missing from the error: %v", err)
	}
	t.Setenv("FAKE_NPM_FAIL", "")
	m.NpmCommand = []string{"definitely-not-an-npm"}
	if _, err := m.Install(bg, User, "npm:ok"); err == nil || !strings.Contains(err.Error(), "npmCommand") {
		t.Errorf("a missing npm must say how to fix it: %v", err)
	}
}

func TestNPMWithoutNPMOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m, _, _ := env(t, false)
	if _, err := m.Install(bg, User, "npm:x"); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("%v", err)
	}
	if _, err := m.Install(bg, User, "git:h.example/a/b"); err == nil || !strings.Contains(err.Error(), "git is needed") {
		t.Errorf("%v", err)
	}
}

func TestNPMCommandSettingIsUsed(t *testing.T) {
	reg, logf := fakeNPM(t)
	publish(t, reg, "ok", "1.0.0", map[string]string{"package.json": `{"name":"ok","version":"1.0.0"}`})
	m, _, _ := env(t, false)
	m.NpmCommand = []string{"npm", "--loglevel=silent"}
	if _, err := m.Install(bg, User, "npm:ok"); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(logf); !strings.HasPrefix(string(calls), "--loglevel=silent install") {
		t.Errorf("%s", calls)
	}
}

func TestProjectInstallsNeedTrust(t *testing.T) {
	reg, logf := fakeNPM(t)
	publish(t, reg, "ok", "1.0.0", map[string]string{"package.json": `{"name":"ok","version":"1.0.0"}`})
	m, _, cwd := env(t, false)
	if _, err := m.Install(bg, Project, "npm:ok"); !errors.Is(err, ErrNotTrusted) {
		t.Errorf("install: %v", err)
	}
	if _, err := m.Remove(Project, "npm:ok"); !errors.Is(err, ErrNotTrusted) {
		t.Errorf("remove: %v", err)
	}
	if _, err := m.Update(bg, Project, ""); !errors.Is(err, ErrNotTrusted) {
		t.Errorf("update: %v", err)
	}
	if _, err := os.Stat(logf); err == nil {
		t.Error("npm must not run for an untrusted project")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".koine")); err == nil {
		t.Error("nothing may be written into an untrusted project")
	}
	m.Trusted = true
	if _, err := m.Install(bg, Project, "npm:ok"); err != nil {
		t.Fatal(err)
	}
	d := DirsFor(Project, m.Home, cwd)
	if gi, _ := os.ReadFile(filepath.Join(d.Root, ".gitignore")); string(gi) != "*\n" {
		t.Errorf("installed project files must not be committed: %q", gi)
	}
	if _, err := os.Stat(d.Lock); err != nil {
		t.Errorf("the project lockfile is meant to be committed: %v", err)
	}
}

func TestUpdateMovesUnpinnedAndKeepsPinned(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "free", "1.0.0", map[string]string{"package.json": `{"name":"free","version":"1.0.0"}`})
	publish(t, reg, "fixed", "1.0.0", map[string]string{"package.json": `{"name":"fixed","version":"1.0.0"}`})
	m, home, cwd := env(t, false)
	for _, s := range []string{"npm:free", "npm:fixed@1.0.0"} {
		if _, err := m.Install(bg, User, s); err != nil {
			t.Fatal(err)
		}
	}
	publish(t, reg, "free", "1.1.0", map[string]string{"package.json": `{"name":"free","version":"1.1.0"}`})
	publish(t, reg, "fixed", "1.1.0", map[string]string{"package.json": `{"name":"fixed","version":"1.1.0"}`})
	res, err := m.Update(bg, User, "")
	if err != nil || len(res) != 2 {
		t.Fatalf("%v %v", res, err)
	}
	free, fixed := res[0], res[1]
	if !free.Changed() || free.OldVersion != "1.0.0" || free.NewVersion != "1.1.0" {
		t.Errorf("free: %+v", free)
	}
	if fixed.Changed() || fixed.Note != "pinned" {
		t.Errorf("fixed: %+v", fixed)
	}
	lock, _ := ReadLock(DirsFor(User, home, cwd).Lock)
	if lock.Packages["npm:free"].Version != "1.1.0" || lock.Packages["npm:fixed"].Version != "1.0.0" {
		t.Errorf("lock: %+v", lock.Packages)
	}
	// naming one package, and one that is not there
	if res, err := m.Update(bg, User, "npm:free"); err != nil || len(res) != 1 || res[0].Changed() {
		t.Errorf("single: %+v %v", res, err)
	}
	if _, err := m.Update(bg, User, "npm:ghost"); err == nil || !strings.Contains(err.Error(), "not in the user packages") {
		t.Errorf("ghost: %v", err)
	}
}

func TestUpdateInstallsWhatIsMissingAndReportsFailuresPerPackage(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "a", "1.0.0", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`})
	m, home, cwd := env(t, true)
	d := DirsFor(Project, home, cwd)
	if err := UpdateEntries(d.Settings, func(es []Entry) []Entry {
		return append(es, Entry{Source: "npm:a@1.0.0"}, Entry{Source: "npm:gone@2.0.0"}, Entry{Source: "bogus"}, Entry{Source: "./nowhere"})
	}); err != nil {
		t.Fatal(err)
	}
	res, err := m.Update(bg, Project, "")
	if err != nil || len(res) != 4 {
		t.Fatalf("%+v %v", res, err)
	}
	if !res[0].Installed || res[0].Err != nil || res[0].NewVersion != "1.0.0" {
		t.Errorf("a: %+v", res[0])
	}
	for i := 1; i < 4; i++ {
		if res[i].Err == nil {
			t.Errorf("entry %d should have failed: %+v", i, res[i])
		}
	}
}

func TestIntegrityMismatchIsRefusedAndNotRecorded(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "a", "1.0.0", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`, "x.txt": "good"})
	m, home, cwd := env(t, false)
	if _, err := m.Install(bg, User, "npm:a@1.0.0"); err != nil {
		t.Fatal(err)
	}
	// the registry now serves different bytes under the same version
	publish(t, reg, "a", "1.0.0", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`, "x.txt": "tampered"})
	_, err := m.Install(bg, User, "npm:a@1.0.0")
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("a republished version must be refused: %v", err)
	}
	d := DirsFor(User, home, cwd)
	lock, _ := ReadLock(d.Lock)
	if lock.Packages["npm:a"].Integrity == "" {
		t.Fatal("lock lost")
	}
	// and Load refuses to use the files that are now on disk
	r := Load(home, cwd, false)
	if len(r.Packages) != 0 || !strings.Contains(strings.Join(r.Warnings, "\n"), "NOT loaded") {
		t.Errorf("load: %+v", r)
	}
}

func TestRemoveDeletesSettingsLockAndFiles(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "a", "1.0.0", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`})
	m, home, cwd := env(t, false)
	local := t.TempDir()
	for _, s := range []string{"npm:a", local} {
		if _, err := m.Install(bg, User, s); err != nil {
			t.Fatal(err)
		}
	}
	d := DirsFor(User, home, cwd)
	srcA, _ := Parse("npm:a", "", "")
	dir := filepath.Join(d.Root, "npm", srcA.safeName())
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.Remove(User, "npm:a@1.0.0"); !ok || err != nil { // any spelling of the same package
		t.Fatalf("%v %v", ok, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the installed files are still there")
	}
	if lock, _ := ReadLock(d.Lock); len(lock.Packages) != 0 {
		t.Errorf("lock: %+v", lock.Packages)
	}
	if ok, err := m.Remove(User, "npm:a"); ok || err != nil {
		t.Errorf("removing twice: %v %v", ok, err)
	}
	if ok, err := m.Remove(User, local); !ok || err != nil {
		t.Fatalf("local: %v %v", ok, err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Error("a local package's directory is never deleted")
	}
	if es := entriesOf(t, d); len(es) != 0 {
		t.Errorf("settings: %+v", es)
	}
	if _, err := m.Remove(User, "what"); err == nil {
		t.Error("an unparsable source")
	}
}

func TestReinstallKeepsFilters(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "a", "1.0.0", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`})
	publish(t, reg, "a", "2.0.0", map[string]string{"package.json": `{"name":"a","version":"2.0.0"}`})
	m, home, cwd := env(t, false)
	if _, err := m.Install(bg, User, "npm:a@1.0.0"); err != nil {
		t.Fatal(err)
	}
	d := DirsFor(User, home, cwd)
	_ = UpdateEntries(d.Settings, func(es []Entry) []Entry { es[0].Skills = []string{}; return es })
	if _, err := m.Install(bg, User, "npm:a@2.0.0"); err != nil {
		t.Fatal(err)
	}
	es := entriesOf(t, d)
	if len(es) != 1 || es[0].Source != "npm:a@2.0.0" || es[0].Skills == nil {
		t.Errorf("%+v", es)
	}
}

func TestLocalPackages(t *testing.T) {
	m, home, cwd := env(t, true)
	local := filepath.Join(cwd, "tools", "mine")
	write(t, local, map[string]string{"package.json": `{"name":"mine","version":"0.3.0"}`})
	in, err := m.Install(bg, Project, "./tools/mine")
	if err != nil || in.Dir != local || in.Version != "0.3.0" {
		t.Fatalf("%+v %v", in, err)
	}
	d := DirsFor(Project, home, cwd)
	if es := entriesOf(t, d); es[0].Source != "../tools/mine" { // relative to the settings file's own directory
		t.Errorf("stored as %q", es[0].Source)
	}
	if _, err := os.Stat(d.Lock); err == nil {
		t.Error("local packages are not locked")
	}
	if _, err := m.Install(bg, User, filepath.Join(cwd, "nothing")); err == nil {
		t.Error("a missing directory")
	}
	if _, err := m.Install(bg, User, filepath.Join(local, "package.json")); err == nil {
		t.Error("a file is not a package")
	}
	if res, err := m.Update(bg, Project, ""); err != nil || len(res) != 1 || res[0].Note == "" || res[0].Err != nil {
		t.Errorf("update: %+v %v", res, err)
	}
	// user scope stores an absolute path
	if _, err := m.Install(bg, User, "./tools/mine"); err != nil {
		t.Fatal(err)
	}
	if es := entriesOf(t, DirsFor(User, home, cwd)); es[0].Source != local {
		t.Errorf("user entry: %+v", es)
	}
}
