package srcinfo

import "testing"

func TestInfoShapes(t *testing.T) {
	b := Builtin("read")
	if b.Path != "builtin:read" || b.Source != "builtin" || b.Scope != "temporary" || b.Origin != "top-level" || b.BaseDir != "" {
		t.Errorf("%+v", b)
	}
	p := Package("/pkg/a.ts", "npm:acme@1.0.0", "project", "/pkg")
	if p.Origin != "package" || p.Source != "npm:acme@1.0.0" || p.Scope != "project" || p.BaseDir != "/pkg" || p.Path != "/pkg/a.ts" {
		t.Errorf("%+v", p)
	}
	if !(Info{}).IsZero() || b.IsZero() {
		t.Error("IsZero")
	}
}
