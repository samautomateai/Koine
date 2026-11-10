package ext

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed sidecar
var sidecarFS embed.FS

// ExtractSidecar writes the embedded JS runner and shims to the user cache dir
// (content-addressed) and returns the directory.
func ExtractSidecar() (string, error) {
	h := sha256.New()
	_ = fs.WalkDir(sidecarFS, "sidecar", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := sidecarFS.ReadFile(p)
			h.Write([]byte(p))
			h.Write(b)
		}
		return nil
	})
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "koine", "sidecar-"+hex.EncodeToString(h.Sum(nil))[:12])
	err = fs.WalkDir(sidecarFS, "sidecar", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("sidecar", p)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := sidecarFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	return dir, err
}

// JSSpec builds a Spec that runs a Pi extension / opencode plugin file (or
// "pkg:<npm-name>") through the sidecar. Bun is preferred (runs TypeScript
// natively); Node >= 22.6 is used with type stripping otherwise.
func JSSpec(name, target string) (Spec, error) {
	dir, err := ExtractSidecar()
	if err != nil {
		return Spec{}, err
	}
	runner := filepath.Join(dir, "runner.mjs")
	pref := os.Getenv("KOINE_JS_RUNTIME") // "node" or "bun" to force one
	if p, err := exec.LookPath("bun"); err == nil && pref != "node" {
		return Spec{Name: name, Command: []string{p, runner, target}}, nil
	}
	if p, err := exec.LookPath("node"); err == nil {
		return Spec{Name: name, Command: []string{p, "--experimental-strip-types", "--no-warnings", "--import", filepath.Join(dir, "register.mjs"), runner, target}}, nil
	}
	return Spec{}, errors.New("JS extensions need bun or node (>=22.6) on PATH")
}
