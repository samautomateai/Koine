package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// unicodeSpaces are the space-like characters models and terminals paste into paths; Pi turns them into plain spaces.
var unicodeSpaces = strings.NewReplacer("\u00a0", " ", "\u2000", " ", "\u2001", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ",
	"\u2005", " ", "\u2006", " ", "\u2007", " ", "\u2008", " ", "\u2009", " ", "\u200a", " ", "\u202f", " ", "\u205f", " ", "\u3000", " ")

// ExpandPath strips a leading "@" (the file-reference marker), normalises unicode spaces and expands "~" and "~/".
func ExpandPath(p, home string) string {
	p = unicodeSpaces.Replace(strings.TrimPrefix(p, "@"))
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// ResolvePath expands p (see ExpandPath) and joins relative paths to cwd.
func ResolvePath(p, cwd, home string) string {
	p = ExpandPath(p, home)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

// ResolveReadPath is ResolvePath for files that must exist. When the file is missing it tries the spellings macOS and models disagree on:
// screenshot names with a narrow no-break space before AM/PM, and straight versus curly apostrophes.
func ResolveReadPath(p, cwd, home string) string {
	full := ResolvePath(p, cwd, home)
	if exists(full) {
		return full
	}
	// The unicode-space normalisation above would also have turned a real narrow no-break space into a plain one, so retry the raw name.
	raw := strings.TrimPrefix(p, "@")
	variants := []string{
		strings.NewReplacer(" AM.", "\u202fAM.", " PM.", "\u202fPM.").Replace(full),
		strings.ReplaceAll(full, "'", "\u2019"),
	}
	variants = append(variants, strings.ReplaceAll(variants[0], "'", "\u2019"), resolveRaw(raw, cwd, home))
	for _, v := range variants {
		if v != full && exists(v) {
			return v
		}
	}
	return full
}

// resolveRaw resolves p without normalising its spaces.
func resolveRaw(p, cwd, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
