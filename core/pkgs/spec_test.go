package pkgs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSources(t *testing.T) {
	base, home := "/proj/.koine", "/home/u"
	cases := []struct {
		raw      string
		kind     Kind
		identity string
		name     string
		version  string
		url      string
		ref      string
		pinned   bool
	}{
		{"npm:left-pad", NPM, "npm:left-pad", "left-pad", "", "", "", false},
		{"npm:@scope/tools@1.2.3", NPM, "npm:@scope/tools", "@scope/tools", "1.2.3", "", "", true},
		{"npm:tools@latest", NPM, "npm:tools", "tools", "latest", "", "", false},
		{"git:github.com/example/pi-tools@v1", Git, "git:github.com/example/pi-tools", "", "", "https://github.com/example/pi-tools", "v1", true},
		{"git:github.com/example/pi-tools", Git, "git:github.com/example/pi-tools", "", "", "https://github.com/example/pi-tools", "", false},
		{"https://GitHub.com/example/pi-tools.git@abc1234", Git, "git:github.com/example/pi-tools", "", "",
			"https://GitHub.com/example/pi-tools.git", "abc1234", true},
		{"git:git@github.com:example/pi-tools@v2", Git, "git:github.com/example/pi-tools", "", "", "git@github.com:example/pi-tools", "v2", true},
		{"ssh://git@host.example/a/b", Git, "git:host.example/a/b", "", "", "ssh://git@host.example/a/b", "", false},
		{"git:/srv/repos/tools@v3", Git, "git:/srv/repos/tools", "", "", "/srv/repos/tools", "v3", true},
		{"file:///srv/repos/tools", Git, "git:/srv/repos/tools", "", "", "file:///srv/repos/tools", "", false},
		{"./local-pkg", Local, "/proj/.koine/local-pkg", "", "", "", "", true},
		{"../up", Local, "/proj/up", "", "", "", "", true},
		{"~/pkgs/x", Local, "/home/u/pkgs/x", "", "", "", "", true},
		{"/abs/pkg/", Local, "/abs/pkg", "", "", "", "", true},
	}
	for _, c := range cases {
		s, err := Parse(c.raw, base, home)
		if err != nil {
			t.Errorf("%s: %v", c.raw, err)
			continue
		}
		if s.Kind != c.kind || s.Identity != filepath.FromSlash(c.identity) ||
			s.Name != c.name || s.Version != c.version || s.URL != c.url || s.Ref != c.ref || s.Pinned() != c.pinned {
			t.Errorf("%s: got %+v pinned=%v", c.raw, s, s.Pinned())
		}
	}
}

func TestParseRejectsUnsafeOrUnknownSources(t *testing.T) {
	for _, raw := range []string{
		"", "left-pad", "npm:", "npm:../evil", "npm:Foo Bar", "npm:pkg@", "npm:pkg@-x", "npm:@scope", "npm:.hidden",
		"git:", "git:-oops", "git:ext::sh -c touch% /tmp/x", "git:nohost", "git:a/../b", "git:github.com/x/y@-bad", "https://", "ftp://x/y",
	} {
		if s, err := Parse(raw, "/p", "/h"); err == nil {
			t.Errorf("%q should be refused, got %+v", raw, s)
		}
	}
}

func TestSameRepositoryHasOneIdentity(t *testing.T) {
	a, _ := Parse("git:github.com/Example/Tools@v1", "/", "/")
	b, _ := Parse("https://github.com/Example/Tools.git", "/", "/")
	c, _ := Parse("git@github.com:Example/Tools", "/", "/")
	if a.Identity != b.Identity || b.Identity != c.Identity {
		t.Errorf("identities differ: %q %q %q", a.Identity, b.Identity, c.Identity)
	}
}

func TestSafeNameCannotEscape(t *testing.T) {
	for _, raw := range []string{"npm:@scope/tools", "git:github.com/x/y", "git:/srv/..hidden/tools"} {
		s, err := Parse(raw, "/", "/")
		if err != nil {
			continue
		}
		if n := s.safeName(); strings.ContainsAny(n, "/\\") || strings.HasPrefix(n, ".") {
			t.Errorf("%s: unsafe directory name %q", raw, n)
		}
	}
	if (Source{Identity: "x"}).safeName() == "" {
		t.Error("empty name")
	}
}
