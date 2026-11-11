package tools

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Edit is one replacement of a multi-edit call. Every OldText is matched against the original file, not against earlier edits.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

const bom = "\xef\xbb\xbf"

// SplitBOM separates a leading byte-order mark from the text.
func SplitBOM(s string) (string, string) {
	if strings.HasPrefix(s, bom) {
		return bom, s[len(bom):]
	}
	return "", s
}

// DetectLineEnding is "\r\n" when the first line break in s is a CRLF, otherwise "\n".
func DetectLineEnding(s string) string {
	i := strings.Index(s, "\n")
	if i > 0 && s[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF turns CRLF and lone CR into LF.
func NormalizeToLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// RestoreLineEnding converts LF text back to the file's own line ending.
func RestoreLineEnding(s, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}

var fuzzyRunes = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
	"\u201c", `"`, "\u201d", `"`, "\u201e", `"`, "\u201f", `"`,
	"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-", "\u2014", "-", "\u2015", "-", "\u2212", "-",
	"\u00a0", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ", "\u2005", " ", "\u2006", " ", "\u2007", " ", "\u2008", " ",
	"\u2009", " ", "\u200a", " ", "\u202f", " ", "\u205f", " ", "\u3000", " ")

// NormalizeForFuzzyMatch strips trailing whitespace from every line and folds smart quotes, dashes and unusual spaces to ASCII. Unlike Pi it does
// not apply Unicode NFKC (the standard library has no normaliser).
func NormalizeForFuzzyMatch(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	return fuzzyRunes.Replace(strings.Join(lines, "\n"))
}

type matchedEdit struct {
	index, start, end int
	newText           string
}

// ApplyEdits applies edits to LF-normalised content and returns the new content. Each OldText must occur exactly once in the original. When any
// OldText is not found verbatim, all matching is done on the fuzzy-normalised text (as Pi does), and the result is that normalised text.
func ApplyEdits(content string, edits []Edit, path string) (string, error) {
	if len(edits) == 0 {
		return "", fmt.Errorf("edits must contain at least one replacement in %s", path)
	}
	olds := make([]string, len(edits))
	fuzzy := false
	for i, e := range edits {
		olds[i] = NormalizeToLF(e.OldText)
		if olds[i] == "" {
			return "", fmt.Errorf("%s must not be empty in %s", editName(i, len(edits), "oldText"), path)
		}
		if !strings.Contains(content, olds[i]) {
			fuzzy = true
		}
	}
	base := content
	if fuzzy {
		base = NormalizeForFuzzyMatch(content)
	}
	matches := make([]matchedEdit, 0, len(edits))
	for i, e := range edits {
		old := olds[i]
		if fuzzy {
			old = NormalizeForFuzzyMatch(old)
		}
		switch n := strings.Count(base, old); {
		case n == 0:
			//nolint:staticcheck // Pi's wording, which is shown to the model
			return "", fmt.Errorf("Could not find %s in %s. The old text must match exactly including all whitespace and newlines.",
				target(i, len(edits)), path)
		case n > 1:
			//nolint:staticcheck // Pi's wording, which is shown to the model
			return "", fmt.Errorf("Found %d occurrences of %s in %s. The text must be unique. Please provide more context to make it unique.",
				n, target(i, len(edits)), path)
		}
		at := strings.Index(base, old)
		matches = append(matches, matchedEdit{i, at, at + len(old), NormalizeToLF(e.NewText)})
	}
	sort.Slice(matches, func(a, b int) bool { return matches[a].start < matches[b].start })
	for k := 1; k < len(matches); k++ {
		if matches[k].start < matches[k-1].end {
			//nolint:staticcheck // Pi's wording, which is shown to the model
			return "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				matches[k-1].index, matches[k].index, path)
		}
	}
	out := base
	for k := len(matches) - 1; k >= 0; k-- {
		m := matches[k]
		out = out[:m.start] + m.newText + out[m.end:]
	}
	if out == base {
		//nolint:staticcheck // Pi's wording, which is shown to the model
		return "", fmt.Errorf("No changes made to %s. The replacement produced identical content. "+
			"This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return out, nil
}

func editName(i, n int, what string) string {
	if n == 1 {
		return what
	}
	return fmt.Sprintf("edits[%d].%s", i, what)
}

// target names the text an error is about.
func target(i, n int) string {
	if n == 1 {
		return "the exact text"
	}
	return fmt.Sprintf("edits[%d]", i)
}
