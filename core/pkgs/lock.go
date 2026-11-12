package pkgs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// LockEntry records what was installed for one package, so a later install or a start can tell that the files changed.
type LockEntry struct {
	Source  string `json:"source"`
	Kind    Kind   `json:"kind"`
	Version string `json:"version,omitempty"` // npm: the installed version
	Commit  string `json:"commit,omitempty"`  // git: the checked-out commit
	// Resolved and RegistryIntegrity are what npm recorded for the tarball, when it wrote a package-lock.json.
	Resolved          string `json:"resolved,omitempty"`
	RegistryIntegrity string `json:"registryIntegrity,omitempty"`
	// Integrity is Koine's own digest of the installed package files (HashTree), checked every time the package is loaded.
	Integrity string `json:"integrity"`
}

// Lock is the lockfile of one scope (packages.lock.json). Local packages are not in it: they are loaded in place and change on purpose.
type Lock struct {
	Version  int                  `json:"version"`
	Packages map[string]LockEntry `json:"packages"`
}

// ReadLock reads a lockfile; a missing file is an empty lock.
func ReadLock(path string) (*Lock, error) {
	l := &Lock{Version: 1, Packages: map[string]LockEntry{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if l.Packages == nil {
		l.Packages = map[string]LockEntry{}
	}
	return l, nil
}

// Write saves the lock, or removes the file when nothing is left in it.
func (l *Lock) Write(path string) error {
	if len(l.Packages) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	l.Version = 1
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'), 0o644)
}

// HashTree is a digest of the files under dir: names, whether they are executable, and contents, in a fixed order. Directories named
// .git and node_modules are left out (dependencies are covered by their own lock data), and a symbolic link counts as its target text.
func HashTree(dir string) (string, error) {
	h := sha256.New()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%o\x00", filepath.ToSlash(rel), st.Mode()&0o111)
		if st.Mode()&os.ModeSymlink != 0 {
			t, _ := os.Readlink(p)
			fmt.Fprintf(h, "link:%s\x00", t)
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		sum := sha256.New()
		_, err = io.Copy(sum, f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
		h.Write(sum.Sum(nil))
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// npmLockData reads what npm recorded for a package in the install prefix's package-lock.json.
func npmLockData(prefix, name string) (resolved, integrity string) {
	b, err := os.ReadFile(filepath.Join(prefix, "package-lock.json"))
	if err != nil {
		return "", ""
	}
	var pl struct {
		Packages map[string]struct{ Resolved, Integrity string } `json:"packages"`
	}
	if json.Unmarshal(b, &pl) != nil {
		return "", ""
	}
	e := pl.Packages["node_modules/"+name]
	return e.Resolved, e.Integrity
}
