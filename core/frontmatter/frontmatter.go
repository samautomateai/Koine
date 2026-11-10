// Package frontmatter parses the small YAML subset used by SKILL.md, agent and
// command markdown files (Pi, opencode and Claude conventions all share it).
package frontmatter

import (
	"regexp"
	"strings"
)

var keyRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+):\s*(.*)$`)

// Parse returns the flat key/value metadata and the markdown body. Nested
// values (lists, maps) are returned as their raw indented lines joined by "\n".
func Parse(src string) (map[string]string, string) {
	meta := map[string]string{}
	src = strings.ReplaceAll(strings.TrimPrefix(src, "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return meta, src
	}
	rest := src[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		if strings.HasPrefix(rest, "---") { // empty block
			return meta, strings.TrimPrefix(rest[3:], "\n")
		}
		return meta, src
	}
	block, body := rest[:end], rest[end+4:]
	if i := strings.Index(body, "\n"); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}

	var key string
	var folded bool
	var lines []string
	flush := func() {
		if key != "" {
			sep := "\n"
			if folded {
				sep = " "
			}
			meta[key] = strings.TrimSpace(strings.Join(lines, sep))
		}
		key, lines, folded = "", nil, false
	}
	for _, ln := range strings.Split(block, "\n") {
		indented := strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")
		if m := keyRe.FindStringSubmatch(ln); m != nil && !indented {
			flush()
			key = m[1]
			v := strings.TrimSpace(m[2])
			switch v {
			case ">", ">-", ">+":
				folded, v = true, ""
			case "|", "|-", "|+":
				v = ""
			}
			if v != "" {
				folded = true
				lines = []string{unquote(v)}
			}
			continue
		}
		if key != "" {
			lines = append(lines, strings.TrimSpace(ln))
		}
	}
	flush()
	return meta, body
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
