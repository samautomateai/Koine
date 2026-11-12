package pkgs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var headingVersion = regexp.MustCompile(`^#{1,4}\s*\[?v?(\d+(?:\.\d+){0,2}(?:[-+][0-9A-Za-z.-]+)?)\]?`)

// ChangelogNotice is the part of a package's changelog that is new after an update: the sections for versions above `from` and up to
// `to`. The file is CHANGELOG.md in the package, or the pi.changelogPath of its package.json ("" turns the notice off). Sections are
// recognised by a heading that starts with the version ("## 1.2.0", "## [1.2.0] - 2026-01-01", "## v1.2.0"). Only packages that supply
// at least one extension qualify, as in Pi. It returns "" when there is nothing to show.
func ChangelogNotice(l Loaded, from, to string) string {
	if len(l.All.Extensions) == 0 || to == "" {
		return ""
	}
	rel := "CHANGELOG.md"
	if l.Manifest.Pi != nil && l.Manifest.Pi.ChangelogPath != nil {
		if *l.Manifest.Pi.ChangelogPath == "" {
			return ""
		}
		rel = *l.Manifest.Pi.ChangelogPath
	}
	p := filepath.Join(l.Dir, rel)
	if !within(l.Dir, p) {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return sectionsBetween(string(b), from, to)
}

func sectionsBetween(text, from, to string) string {
	var out, cur []string
	keep := false
	flush := func() {
		if keep {
			out = append(out, strings.TrimRight(strings.Join(cur, "\n"), "\n"))
		}
		cur, keep = nil, false
	}
	for _, line := range strings.Split(text, "\n") {
		if m := headingVersion.FindStringSubmatch(line); m != nil {
			flush()
			keep = compareVersions(m[1], from) > 0 && compareVersions(m[1], to) <= 0
		}
		if keep {
			cur = append(cur, line)
		}
	}
	flush()
	return strings.Join(out, "\n\n")
}

// compareVersions orders two semantic versions; a pre-release sorts before its release and build metadata is ignored.
func compareVersions(a, b string) int {
	pa, prea := splitVersion(a)
	pb, preb := splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case prea == preb:
		return 0
	case prea == "":
		return 1
	case preb == "":
		return -1
	case prea < preb:
		return -1
	}
	return 1
}

func splitVersion(v string) (nums [3]int, pre string) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "-"); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	for i, part := range strings.Split(v, ".") {
		if i < 3 {
			nums[i], _ = strconv.Atoi(part)
		}
	}
	return nums, pre
}

// FirstLine is the one-line form of a changelog notice (collapseChangelog).
func FirstLine(name, from, to string) string { return name + " " + from + " -> " + to }
