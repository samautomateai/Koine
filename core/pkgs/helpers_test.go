package pkgs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNPM puts a stand-in `npm` first on PATH. `npm install --prefix P -- name@spec` copies <registry>/<name with / as _>/<spec> to
// P/node_modules/<name> (spec "latest" reads the version from <registry>/<name>/latest). Every call is appended to the returned log.
// Nothing touches the network.
func fakeNPM(t *testing.T) (registry, logFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake npm is a shell script")
	}
	dir := t.TempDir()
	registry, logFile = filepath.Join(dir, "registry"), filepath.Join(dir, "npm.log")
	script := `#!/bin/sh
echo "$@" >> "$FAKE_NPM_LOG"
case " $* " in *" install "*) ;; *) exit 1;; esac
[ -n "$FAKE_NPM_FAIL" ] && { echo "npm ERR! $FAKE_NPM_FAIL" >&2; exit 1; }
prefix=.; spec=
while [ $# -gt 0 ]; do
  case "$1" in --prefix) prefix=$2; shift;; --) spec=$2; shift;; esac
  shift
done
if [ -z "$spec" ]; then exit 0; fi
name=${spec%@*}; ver=${spec##*@}
flat=$(echo "$name" | tr / _)
if [ "$ver" = latest ]; then ver=$(cat "$FAKE_NPM_REGISTRY/$flat/latest") || exit 1; fi
src="$FAKE_NPM_REGISTRY/$flat/$ver"
[ -d "$src" ] || { echo "npm ERR! 404 $spec" >&2; exit 1; }
rm -rf "$prefix/node_modules/$name"
mkdir -p "$prefix/node_modules/$name"
cp -R "$src/." "$prefix/node_modules/$name/"
printf '{"packages":{"node_modules/%s":{"resolved":"http://registry.invalid/%s-%s.tgz","integrity":"sha512-fake"}}}' \
  "$name" "$flat" "$ver" > "$prefix/package-lock.json"
`
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NPM_REGISTRY", registry)
	t.Setenv("FAKE_NPM_LOG", logFile)
	return registry, logFile
}

// publish adds a version of a package to the fake registry; the first file set also becomes "latest" unless a later one is newer.
func publish(t *testing.T, registry, name, version string, files map[string]string) {
	t.Helper()
	flat := strings.ReplaceAll(name, "/", "_")
	write(t, filepath.Join(registry, flat, version), files)
	if err := os.WriteFile(filepath.Join(registry, flat, "latest"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, root string, files map[string]string) {
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

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// gitRepo makes a local git repository holding files and returns its path. Each call to commit adds a commit and tag.
type gitRepo struct {
	t    *testing.T
	Path string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	needGit(t)
	r := &gitRepo{t: t, Path: t.TempDir()}
	r.run("init", "-q", "-b", "main")
	return r
}

func (r *gitRepo) run(args ...string) string {
	r.t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	c.Dir = r.Path
	c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := c.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files, commits them and tags the commit when tag is not empty. It returns the commit id.
func (r *gitRepo) commit(files map[string]string, tag string) string {
	r.t.Helper()
	write(r.t, r.Path, files)
	r.run("add", "-A")
	r.run("commit", "-q", "-m", "c")
	if tag != "" {
		r.run("tag", tag)
	}
	return r.run("rev-parse", "HEAD")
}

// env gives a test a home, a project and a Manager for them.
func env(t *testing.T, trusted bool) (*Manager, string, string) {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentdirEnv, "")
	return &Manager{Home: home, Cwd: cwd, Trusted: trusted}, home, cwd
}

const agentdirEnv = "KOINE_AGENT_DIR"
