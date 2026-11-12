// Package srcinfo says where a loaded tool, command, skill or prompt came from. It is Pi's SourceInfo (path, source, scope, origin,
// baseDir) and is a leaf package so every loader can attach it without importing the others.
package srcinfo

// Info is the provenance of one loaded resource.
type Info struct {
	// Path is the file the resource was loaded from, or "builtin:<tool>" for a built-in tool.
	Path string `json:"path"`
	// Source names where it came from: "builtin", "auto" (found in a conventional directory), "local" (named in a setting), "cli" (-e,
	// --skill and the like), or the package srcinfo as configured ("npm:name@1.2.0", "git:github.com/x/y@v1", a path).
	Source string `json:"source"`
	// Scope is "user", "project" or "temporary" (built in, or given for this run only).
	Scope string `json:"scope"`
	// Origin is "package" for resources a package supplies, otherwise "top-level".
	Origin string `json:"origin"`
	// BaseDir is the package root for package resources.
	BaseDir string `json:"baseDir,omitempty"`
}

// IsZero reports whether nothing is known about the source.
func (i Info) IsZero() bool { return i == Info{} }

// Builtin is the provenance of a tool Koine ships.
func Builtin(tool string) Info {
	return Info{Path: "builtin:" + tool, Source: "builtin", Scope: "temporary", Origin: "top-level"}
}

// Package is the provenance of a resource inside a package.
func Package(path, pkgSource, scope, baseDir string) Info {
	return Info{Path: path, Source: pkgSource, Scope: scope, Origin: "package", BaseDir: baseDir}
}
