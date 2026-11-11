package tui

import (
	"sort"
	"strings"
	"unicode"
)

// FuzzyScore matches query against text as a case-insensitive subsequence. A higher score is a better match: consecutive characters, matches
// at the start of a word (after / _ - . or a space) and early matches score more. ok is false when query is not a subsequence of text.
func FuzzyScore(query, text string) (score int, ok bool) {
	q, t := []rune(strings.ToLower(query)), []rune(strings.ToLower(text))
	orig := []rune(text)
	if len(orig) != len(t) {
		orig = t
	}
	if len(q) == 0 {
		return 0, true
	}
	qi, prev := 0, -2
	for ti, c := range t {
		if qi >= len(q) || c != q[qi] {
			continue
		}
		score += 10
		if prev == ti-1 {
			score += 15
		}
		if ti == 0 || strings.ContainsRune("/_-. ", t[ti-1]) || (unicode.IsUpper(orig[ti]) && unicode.IsLower(orig[ti-1])) {
			score += 20
		}
		score -= min(ti, 10) / 2
		prev = ti
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(query)) {
		score += 30
	}
	return score - len(t)/8, true
}

// FuzzyFilter returns the items that match query, best first (ties keep their order).
func FuzzyFilter(query string, items []string) []string {
	type hit struct {
		s     string
		score int
	}
	var hits []hit
	for _, it := range items {
		if sc, ok := FuzzyScore(query, it); ok {
			hits = append(hits, hit{it, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.s
	}
	return out
}
