package session

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
)

// ExportJSONL writes the session file as stored: header, every branch, labels and entries Koine does not interpret.
func (s *Session) ExportJSONL(w io.Writer) error {
	b, err := s.Raw()
	if err != nil {
		return err
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	_, err = w.Write(b)
	return err
}

const exportCSS = `body{font:15px/1.5 system-ui,sans-serif;max-width:60rem;margin:2rem auto;padding:0 1rem;color:#1b1b1f;background:#fff}
h1{font-size:1.3rem}.meta{color:#666;font-size:.85rem}.m{border-radius:8px;padding:.6rem .9rem;margin:.7rem 0;white-space:pre-wrap;overflow-wrap:anywhere}
.user{background:#e8f0fe}.assistant{background:#f4f4f6}.tool{background:#fff8e1;font:13px ui-monospace,monospace}.note{background:#eef7ee;font-size:.9rem}
.role{font-weight:600;font-size:.8rem;text-transform:uppercase;color:#555}.err{background:#fdecea}details{margin:.3rem 0}summary{cursor:pointer;color:#555}
pre{margin:.3rem 0;white-space:pre-wrap}img{max-width:100%}.label{background:#ffe08a;border-radius:4px;padding:0 .4rem;font-size:.8rem}
.tree{font:13px ui-monospace,monospace;white-space:pre}@media(prefers-color-scheme:dark){body{background:#16161a;color:#e8e8ec}.user{background:#1d2a44}
.assistant{background:#222228}.tool{background:#2c2815}.note{background:#1c2b1c}.err{background:#3a1d1a}.role,summary,.meta{color:#aaa}}`

// ExportHTML writes the active branch as one self-contained HTML page (inline style, no scripts, no external requests), followed by an
// outline of the whole entry tree. All text from the session is escaped.
func (s *Session) ExportHTML(w io.Writer) error {
	var b strings.Builder
	title := s.Name()
	if title == "" {
		title = "Session " + s.ID
	}
	e := html.EscapeString
	fmt.Fprintf(&b, "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">"+
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"+
		"<title>%s</title><style>%s</style></head><body>\n<h1>%s</h1>\n<p class=\"meta\">session %s · %s</p>\n",
		e(title), exportCSS, e(title), e(s.ID), e(s.Cwd))
	labels := s.Labels()
	for _, en := range s.path() {
		lab := ""
		if l := labels[en.ID]; l != "" {
			lab = ` <span class="label">` + e(l) + `</span>`
		}
		switch en.Type {
		case "message":
			renderMessage(&b, en, lab)
		case "compaction":
			fmt.Fprintf(&b, "<div class=\"m note\"><span class=\"role\">compaction</span>%s\n%s</div>\n", lab, e(en.Summary))
		case "branch_summary":
			fmt.Fprintf(&b, "<div class=\"m note\"><span class=\"role\">branch summary</span>%s\n%s</div>\n", lab, e(en.Summary))
		case "custom_message":
			fmt.Fprintf(&b, "<div class=\"m note\"><span class=\"role\">%s</span>%s\n%s</div>\n", e(en.CustomType), lab, e(textOf(en.Content)))
		}
	}
	b.WriteString("<h2>Entry tree</h2>\n<div class=\"tree\">")
	for _, r := range s.Rows(FilterAll) {
		mark := "  "
		if r.IsLeaf {
			mark = "◀ "
		} else if r.OnPath {
			mark = "* "
		}
		l := ""
		if r.Label != "" {
			l = " [" + r.Label + "]"
		}
		b.WriteString(e(fmt.Sprintf("%s%s%s  %s%s", strings.Repeat("  ", r.Depth), mark, r.Entry.ID, r.Text, l)) + "\n")
	}
	b.WriteString("</div>\n</body></html>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

type exportBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Data      string          `json:"data"`
	MimeType  string          `json:"mimeType"`
}

func renderMessage(b *strings.Builder, en Entry, lab string) {
	var pm piMessage
	if json.Unmarshal(en.Message, &pm) != nil {
		return
	}
	e := html.EscapeString
	class, role := pm.Role, pm.Role
	switch pm.Role {
	case "toolResult":
		class, role = "tool", "tool result "+pm.ToolName
		if pm.IsError {
			class += " err"
		}
	case "user", "assistant":
	default:
		class = "note"
	}
	fmt.Fprintf(b, "<div class=\"m %s\"><span class=\"role\">%s</span>%s\n", e(class), e(role), lab)
	var str string
	if json.Unmarshal(pm.Content, &str) == nil {
		b.WriteString(e(str))
	} else {
		var blocks []exportBlock
		_ = json.Unmarshal(pm.Content, &blocks)
		for _, bl := range blocks {
			switch bl.Type {
			case "text":
				b.WriteString(e(bl.Text) + "\n")
			case "thinking":
				fmt.Fprintf(b, "<details><summary>thinking</summary><pre>%s</pre></details>", e(bl.Thinking))
			case "toolCall":
				fmt.Fprintf(b, "<details open><summary>tool call: %s</summary><pre>%s</pre></details>", e(bl.Name), e(string(bl.Arguments)))
			case "image":
				if okImage(bl.MimeType) && isBase64(bl.Data) {
					fmt.Fprintf(b, "<img alt=\"image\" src=\"data:%s;base64,%s\">", bl.MimeType, bl.Data)
				} else {
					b.WriteString("[image]")
				}
			}
		}
	}
	b.WriteString("</div>\n")
}

func okImage(m string) bool {
	switch m {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func isBase64(s string) bool {
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '='
		if !ok {
			return false
		}
	}
	return s != ""
}
