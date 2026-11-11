package tui

import (
	"strings"
	"unicode"
)

type langDef struct {
	keywords map[string]bool
	types    map[string]bool
	line     []string  // line-comment markers
	block    [2]string // block comment delimiters, or empty
	quotes   string    // characters that open a string
	triple   bool      // """ and ''' strings (Python)
	caseless bool
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var cLikeKW = "if else for while do switch case default break continue return new delete this super class struct enum interface extends implements " +
	"public private protected static final abstract const let var function async await try catch finally throw throws import export from as of in " +
	"typeof instanceof void null true false undefined namespace using package fn pub mut impl trait use mod match loop where self Self crate"

var languages = map[string]*langDef{
	"go": {keywords: words("break case chan const continue default defer else fallthrough for func go goto if import interface map package range " +
		"return select struct switch type var nil true false iota"), types: words("string int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 " +
		"uintptr float32 float64 bool byte rune error any complex128"), line: []string{"//"}, block: [2]string{"/*", "*/"}, quotes: "\"'`"},
	"js": {keywords: words(cLikeKW + " yield static get set constructor"), types: words("string number boolean any unknown never object Promise Array Map Set"),
		line: []string{"//"}, block: [2]string{"/*", "*/"}, quotes: "\"'`"},
	"py": {keywords: words("and as assert async await break class continue def del elif else except finally for from global if import in is lambda " +
		"nonlocal not or pass raise return try while with yield None True False self"), types: words("int str float bool list dict set tuple bytes object"),
		line: []string{"#"}, quotes: "\"'", triple: true},
	"sh": {keywords: words("if then else elif fi for while until do done case esac in function select time return exit export local readonly " +
		"unset set shift source alias echo cd"), line: []string{"#"}, quotes: "\"'`"},
	"json": {keywords: words("true false null"), quotes: "\""},
	"c": {keywords: words(cLikeKW + " typedef sizeof extern volatile register inline goto unsigned signed"),
		types: words("int char long short float double bool size_t string vector auto"), line: []string{"//"}, block: [2]string{"/*", "*/"},
		quotes: "\"'"},
	"ruby": {keywords: words("def end class module if elsif else unless while until for in do begin rescue ensure return yield self nil true false " +
		"require include and or not then case when"), line: []string{"#"}, quotes: "\"'"},
	"yaml": {keywords: words("true false null yes no"), line: []string{"#"}, quotes: "\"'"},
	"sql": {keywords: words("select from where insert into values update set delete create table alter drop index join left right inner outer on " +
		"group by order having limit as and or not null primary key foreign references distinct union all"),
		types: words("int integer text varchar boolean date timestamp"), line: []string{"--"}, block: [2]string{"/*", "*/"}, quotes: "'\"",
		caseless: true},
	"toml": {keywords: words("true false"), line: []string{"#"}, quotes: "\"'"},
	"html": {block: [2]string{"<!--", "-->"}, quotes: "\"'"},
	"css":  {keywords: words("important"), block: [2]string{"/*", "*/"}, quotes: "\"'"},
}

var aliases = map[string]string{
	"golang": "go", "javascript": "js", "jsx": "js", "mjs": "js", "cjs": "js", "ts": "js", "typescript": "js", "tsx": "js", "python": "py",
	"bash": "sh", "zsh": "sh", "shell": "sh", "console": "sh", "jsonc": "json", "json5": "json", "rs": "c", "rust": "c", "java": "c", "kotlin": "c",
	"cpp": "c", "c++": "c", "h": "c", "hpp": "c", "cs": "c", "csharp": "c", "swift": "c", "php": "c", "scala": "c", "dart": "c", "rb": "ruby",
	"yml": "yaml", "xml": "html", "svg": "html", "scss": "css", "less": "css", "postgres": "sql", "mysql": "sql", "sqlite": "sql",
}

func langFor(name string) *langDef {
	name = strings.ToLower(name)
	if a, ok := aliases[name]; ok {
		name = a
	}
	return languages[name]
}

// Highlight colours code lines with the theme's syntax tokens. It understands comments, strings, numbers, keywords, types and calls for
// the languages above, and +/- lines for diffs; for any other language the lines come back unchanged. This is a lexer, not a parser, so
// edge cases (regular expression literals, heredocs, nested templates) may be coloured imperfectly.
func Highlight(lang string, code []string, th *Theme) []string {
	if strings.EqualFold(lang, "diff") || strings.EqualFold(lang, "patch") {
		out := make([]string, len(code))
		for i, l := range code {
			out[i] = diffLine(l, th)
		}
		return out
	}
	d := langFor(lang)
	if d == nil || th == nil {
		return append([]string(nil), code...)
	}
	out := make([]string, len(code))
	state := ""
	for i, l := range code {
		out[i], state = d.line1(l, state, th, strings.EqualFold(lang, "json") || strings.EqualFold(lang, "jsonc"))
	}
	return out
}

func diffLine(l string, th *Theme) string {
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"), strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
		return th.Bold(l)
	case strings.HasPrefix(l, "@@"):
		return th.Fg("accent", l)
	case strings.HasPrefix(l, "+"):
		return th.Fg("toolDiffAdded", l)
	case strings.HasPrefix(l, "-"):
		return th.Fg("toolDiffRemoved", l)
	}
	return th.Fg("toolDiffContext", l)
}

// line1 colours one line. state carries an open block comment ("*/") or triple-quoted string across lines.
func (d *langDef) line1(l, state string, th *Theme, jsonKeys bool) (string, string) {
	r := []rune(l)
	var b strings.Builder
	put := func(tok, text string) {
		if tok == "" {
			b.WriteString(text)
		} else {
			b.WriteString(th.Fg(tok, text))
		}
	}
	i := 0
	if state != "" { // inside a block comment or multi-line string
		tok := "syntaxComment"
		if state == `"""` || state == `'''` {
			tok = "syntaxString"
		}
		if end := strings.Index(l, closerOf(state, d)); end >= 0 {
			n := end + len(closerOf(state, d))
			put(tok, l[:n])
			i, state = len([]rune(l[:n])), ""
		} else {
			put(tok, l)
			return b.String(), state
		}
	}
	for i < len(r) {
		rest := string(r[i:])
		switch {
		case hasAnyPrefix(rest, d.line) != "":
			put("syntaxComment", rest)
			return b.String(), ""
		case d.block[0] != "" && strings.HasPrefix(rest, d.block[0]):
			if end := strings.Index(rest[len(d.block[0]):], d.block[1]); end >= 0 {
				n := len(d.block[0]) + end + len(d.block[1])
				put("syntaxComment", rest[:n])
				i += len([]rune(rest[:n]))
				continue
			}
			put("syntaxComment", rest)
			return b.String(), d.block[0]
		case d.triple && (strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, `'''`)):
			q := rest[:3]
			if end := strings.Index(rest[3:], q); end >= 0 {
				put("syntaxString", rest[:3+end+3])
				i += len([]rune(rest[:3+end+3]))
				continue
			}
			put("syntaxString", rest)
			return b.String(), q
		case strings.ContainsRune(d.quotes, r[i]):
			j := i + 1
			for j < len(r) && r[j] != r[i] {
				if r[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(r))
			tok := "syntaxString"
			if jsonKeys && strings.HasPrefix(strings.TrimLeft(string(r[j:]), " "), ":") {
				tok = "syntaxVariable"
			}
			put(tok, string(r[i:j]))
			i = j
		case unicode.IsDigit(r[i]) || (r[i] == '.' && i+1 < len(r) && unicode.IsDigit(r[i+1]) && (i == 0 || !isWordRune(r[i-1]))):
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '.' || r[j] == '_') {
				j++
			}
			put("syntaxNumber", string(r[i:j]))
			i = j
		case isWordRune(r[i]) || r[i] == '$' || r[i] == '@':
			j := i + 1
			for j < len(r) && (isWordRune(r[j]) || r[j] == '$') {
				j++
			}
			w := string(r[i:j])
			key := w
			if d.caseless {
				key = strings.ToLower(w)
			}
			put(d.classify(key, w, r, j), w)
			i = j
		case strings.ContainsRune("+-*/%=<>!&|^~?:", r[i]):
			put("syntaxOperator", string(r[i]))
			i++
		case strings.ContainsRune("()[]{},;.", r[i]):
			put("syntaxPunctuation", string(r[i]))
			i++
		default:
			b.WriteRune(r[i])
			i++
		}
	}
	return b.String(), state
}

func (d *langDef) classify(key, w string, r []rune, after int) string {
	switch {
	case d.keywords[key]:
		return "syntaxKeyword"
	case d.types[key]:
		return "syntaxType"
	case after < len(r) && r[after] == '(':
		return "syntaxFunction"
	case unicode.IsUpper([]rune(w)[0]) && len(w) > 1 && d != languages["sh"]:
		return "syntaxType"
	}
	return ""
}

func closerOf(state string, d *langDef) string {
	if state == d.block[0] {
		return d.block[1]
	}
	return state
}

func hasAnyPrefix(s string, prefixes []string) string {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return p
		}
	}
	return ""
}
