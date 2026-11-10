package frontmatter

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name, in string
		meta     map[string]string
		body     string
	}{
		{"none", "# hi\n", map[string]string{}, "# hi\n"},
		{"simple", "---\nname: a\ndescription: \"quoted: yes\"\n---\nbody\n", map[string]string{"name": "a", "description": "quoted: yes"}, "body\n"},
		{"folded", "---\ndescription: >\n  line one\n  line two\n---\nB", map[string]string{"description": "line one line two"}, "B"},
		{"literal", "---\nx: |\n  a\n  b\n---\nB", map[string]string{"x": "a\nb"}, "B"},
		{"nested map", "---\ntools:\n  write: false\n  edit: true\n---\nB", map[string]string{"tools": "write: false\nedit: true"}, "B"},
		{"crlf+bom", "\ufeff---\r\nname: x\r\n---\r\nB", map[string]string{"name": "x"}, "B"},
		{"empty block", "---\n---\nB", map[string]string{}, "B"},
		{"unterminated", "---\nname: x\n", map[string]string{}, "---\nname: x\n"},
		{"single quotes", "---\nk: 'v'\n---\n", map[string]string{"k": "v"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, b := Parse(c.in)
			if b != c.body {
				t.Errorf("body = %q, want %q", b, c.body)
			}
			if len(m) != len(c.meta) {
				t.Errorf("meta = %v, want %v", m, c.meta)
			}
			for k, v := range c.meta {
				if m[k] != v {
					t.Errorf("meta[%s] = %q, want %q", k, m[k], v)
				}
			}
		})
	}
}
