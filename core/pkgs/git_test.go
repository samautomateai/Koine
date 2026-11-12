package pkgs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitPackagePinnedToATag(t *testing.T) {
	repo := newGitRepo(t)
	v1 := repo.commit(pkgFiles("1.0.0", nil), "v1")
	repo.commit(pkgFiles("2.0.0", nil), "v2")
	m, home, cwd := env(t, false)

	in, err := m.Install(bg, User, "git:"+repo.Path+"@v1")
	if err != nil {
		t.Fatal(err)
	}
	if in.Commit != v1 || !in.Pinned || in.Version != "1.0.0" {
		t.Errorf("installed: %+v", in)
	}
	d := DirsFor(User, home, cwd)
	if !strings.HasPrefix(in.Dir, filepath.Join(d.Root, "git")) {
		t.Errorf("dir: %s", in.Dir)
	}
	lock, _ := ReadLock(d.Lock)
	src, _ := Parse("git:"+repo.Path+"@v1", "", "")
	if e := lock.Packages[src.Identity]; e.Commit != v1 || e.Integrity == "" || e.Kind != Git {
		t.Errorf("lock: %+v", e)
	}
	// a pinned tag stays put on update, even though upstream has moved on
	res, err := m.Update(bg, User, "")
	if err != nil || len(res) != 1 || res[0].Changed() || res[0].Note != "pinned" || res[0].Err != nil {
		t.Errorf("update: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(in.Dir, "package.json")); !strings.Contains(string(b), "1.0.0") {
		t.Errorf("the checkout moved: %s", b)
	}
}

func TestGitPackageFollowsTheDefaultBranchOrAnExplicitBranch(t *testing.T) {
	repo := newGitRepo(t)
	first := repo.commit(pkgFiles("1.0.0", nil), "")
	m, _, _ := env(t, false)
	in, err := m.Install(bg, User, "file://"+repo.Path)
	if err != nil || in.Commit != first || in.Pinned {
		t.Fatalf("%+v %v", in, err)
	}
	second := repo.commit(pkgFiles("1.1.0", nil), "")
	res, err := m.Update(bg, User, "")
	if err != nil || len(res) != 1 || !res[0].Changed() || res[0].NewVersion != second || res[0].OldVersion != first {
		t.Fatalf("%+v %v", res, err)
	}
	// a branch other than the default
	repo.run("checkout", "-q", "-b", "next")
	third := repo.commit(pkgFiles("3.0.0", nil), "")
	repo.run("checkout", "-q", "main")
	in, err = m.Install(bg, User, "git:file://"+repo.Path+"@next")
	if err != nil || in.Commit != third || in.Pinned {
		t.Errorf("branch: %+v %v", in, err)
	}
}

func TestGitPackagePinnedToACommit(t *testing.T) {
	repo := newGitRepo(t)
	c1 := repo.commit(pkgFiles("1.0.0", nil), "")
	repo.commit(pkgFiles("2.0.0", nil), "")
	m, _, _ := env(t, false)
	in, err := m.Install(bg, User, "git:"+repo.Path+"@"+c1[:10])
	if err != nil || in.Commit != c1 || !in.Pinned {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := m.Install(bg, User, "git:"+repo.Path+"@nosuchref"); err == nil || !strings.Contains(err.Error(), "no tag, commit or branch") {
		t.Errorf("unknown ref: %v", err)
	}
	if _, err := m.Install(bg, User, "git:"+filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("clone of a repository that is not there")
	}
}

func TestGitPackageInstallsDependenciesWithoutScripts(t *testing.T) {
	reg, logf := fakeNPM(t)
	publish(t, reg, "left-pad", "1.0.0", map[string]string{"package.json": `{"name":"left-pad","version":"1.0.0"}`})
	repo := newGitRepo(t)
	repo.commit(pkgFiles("1.0.0", map[string]string{"package.json": `{"name":"g","version":"1.0.0","dependencies":{"left-pad":"1"}}`}), "v1")
	m, _, _ := env(t, false)
	if _, err := m.Install(bg, User, "git:"+repo.Path+"@v1"); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(logf)
	for _, want := range []string{"--ignore-scripts", "--no-save", "--omit=dev", "--legacy-peer-deps"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("npm install lacks %s: %s", want, calls)
		}
	}
	m.NpmCommand = []string{"definitely-not-an-npm"}
	repo.commit(pkgFiles("1.0.1", map[string]string{"package.json": `{"name":"g","version":"1.0.1","dependencies":{"left-pad":"1"}}`}), "v2")
	if _, err := m.Install(bg, User, "git:"+repo.Path+"@v2"); err == nil || !strings.Contains(err.Error(), "npmCommand") {
		t.Errorf("dependencies need npm and the error must say so: %v", err)
	}
}

func TestGitTamperingIsDetectedOnLoad(t *testing.T) {
	repo := newGitRepo(t)
	repo.commit(pkgFiles("1.0.0", nil), "v1")
	m, home, cwd := env(t, false)
	in, err := m.Install(bg, User, "git:"+repo.Path+"@v1")
	if err != nil {
		t.Fatal(err)
	}
	if r := Load(home, cwd, false); len(r.Packages) != 1 || len(r.Warnings) != 0 {
		t.Fatalf("clean load: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(in.Dir, "ext", "main.ts"), []byte("export default () => evil()"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Load(home, cwd, false)
	if len(r.Packages) != 0 || !strings.Contains(strings.Join(r.Warnings, "\n"), "NOT loaded") {
		t.Errorf("a modified package must not load: %+v", r)
	}
	// an update restores the pinned checkout, files and lock agree again
	if _, err := m.Update(bg, User, ""); err != nil {
		t.Fatal(err)
	}
	if r := Load(home, cwd, false); len(r.Packages) != 1 {
		t.Errorf("after update: %+v", r)
	}
}

func TestGitRemoteHelpersAreNeverRun(t *testing.T) {
	needGit(t)
	m, _, _ := env(t, false)
	marker := filepath.Join(t.TempDir(), "ran")
	for _, src := range []string{"git:ext::sh -c touch% " + marker, "ext::sh -c touch " + marker} {
		if _, err := m.Install(bg, User, src); err == nil {
			t.Errorf("%q must be refused", src)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a remote helper ran")
	}
}
