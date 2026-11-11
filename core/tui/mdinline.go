package tui

import (
	"regexp"
	"strings"
	"unicode"
)

var bareURL = regexp.MustCompile(`^https?://[^\s<>()\x60]+[^\s<>()\x60.,;:!?'"]`)

// inline renders the inline Markdown of one paragraph: code spans, **bold**, *italic* and _italic_, ~~strike~~, [links](url) and bare
// URLs. Text it does not understand is kept as written.
func inline(s string, o MDOptions) string {
	th := o.Theme
	r := []rune(s)
	var b strings.Builder
	for i := 0; i < len(r); {
		c := r[i]
		rest := string(r[i:])
		switch {
		case c == '\\' && i+1 < len(r) && strings.ContainsRune("\\`*_{}[]()#+-.!|~<>", r[i+1]):
			b.WriteRune(r[i+1])
			i += 2
			continue
		case c == '`':
			n := 1
			for i+n < len(r) && r[i+n] == '`' {
				n++
			}
			if end := strings.Index(string(r[i+n:]), strings.Repeat("`", n)); end >= 0 {
				code := string(r[i+n:])[:end]
				b.WriteString(th.Fg("mdCode", strings.TrimSpace(code)))
				i += n + len([]rune(code)) + n
				continue
			}
			b.WriteString(strings.Repeat("`", n))
			i += n
			continue
		case (c == '*' || c == '_') && strings.HasPrefix(rest, string([]rune{c, c})):
			if inner, n, ok := span(r, i, 2); ok {
				b.WriteString(th.Bold(inline(inner, o)))
				i += n
				continue
			}
		case c == '~' && strings.HasPrefix(rest, "~~"):
			if inner, n, ok := span(r, i, 2); ok {
				b.WriteString(th.Strike(inline(inner, o)))
				i += n
				continue
			}
		case c == '*' || c == '_':
			if inner, n, ok := span(r, i, 1); ok {
				b.WriteString(th.Italic(inline(inner, o)))
				i += n
				continue
			}
		case c == '[':
			if text, url, n, ok := link(r, i); ok {
				b.WriteString(renderLink(inline(text, o), url, o))
				i += n
				continue
			}
		case c == '<' && (strings.HasPrefix(rest, "<http://") || strings.HasPrefix(rest, "<https://")):
			if end := strings.IndexByte(rest, '>'); end > 0 {
				b.WriteString(renderLink(rest[1:end], rest[1:end], o))
				i += len([]rune(rest[:end+1]))
				continue
			}
		case c == 'h' && (i == 0 || !unicode.IsLetter(r[i-1])) && bareURL.MatchString(rest):
			u := bareURL.FindString(rest)
			b.WriteString(renderLink(u, u, o))
			i += len([]rune(u))
			continue
		}
		b.WriteRune(c)
		i++
	}
	return b.String()
}

// span finds the closing delimiter of an emphasis run opened at r[i] with n delimiter characters. It returns the inner text and how many
// runes the whole span takes.
func span(r []rune, i, n int) (string, int, bool) {
	d := r[i]
	if i+n >= len(r) || unicode.IsSpace(r[i+n]) {
		return "", 0, false
	}
	if d == '_' && i > 0 && (unicode.IsLetter(r[i-1]) || unicode.IsDigit(r[i-1])) {
		return "", 0, false
	}
	for j := i + n; j+n <= len(r); j++ {
		if r[j] == '\\' {
			j++
			continue
		}
		if r[j] == '`' { // skip code spans
			k := j + 1
			for k < len(r) && r[k] != '`' {
				k++
			}
			j = k
			continue
		}
		if r[j] != d || unicode.IsSpace(r[j-1]) {
			continue
		}
		if n == 1 && j+1 < len(r) && r[j+1] == d {
			j++ // part of a ** run
			continue
		}
		if n == 2 && (j+1 >= len(r) || r[j+1] != d) {
			continue
		}
		if d == '_' && j+n < len(r) && (unicode.IsLetter(r[j+n]) || unicode.IsDigit(r[j+n])) {
			continue
		}
		if j > i+n {
			return string(r[i+n : j]), j + n - i, true
		}
	}
	return "", 0, false
}

// link parses [text](url) at r[i].
func link(r []rune, i int) (text, url string, n int, ok bool) {
	depth := 0
	j := i
	for ; j < len(r); j++ {
		if r[j] == '[' {
			depth++
		} else if r[j] == ']' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	if j+1 >= len(r) || r[j] != ']' || r[j+1] != '(' {
		return "", "", 0, false
	}
	k := j + 2
	for k < len(r) && r[k] != ')' && r[k] != ' ' {
		k++
	}
	if k >= len(r) {
		return "", "", 0, false
	}
	end := k
	if r[k] == ' ' { // [t](url "title")
		for end < len(r) && r[end] != ')' {
			end++
		}
		if end >= len(r) {
			return "", "", 0, false
		}
	}
	return string(r[i+1 : j]), string(r[j+2 : k]), end + 1 - i, true
}

func renderLink(text, url string, o MDOptions) string {
	th := o.Theme
	if o.Hyperlinks {
		return "\x1b]8;;" + url + "\x1b\\" + th.Underline(th.Fg("mdLink", text)) + "\x1b]8;;\x1b\\"
	}
	if text == url {
		return th.Underline(th.Fg("mdLink", text))
	}
	return th.Underline(th.Fg("mdLink", text)) + th.Fg("mdLinkUrl", " ("+url+")")
}
