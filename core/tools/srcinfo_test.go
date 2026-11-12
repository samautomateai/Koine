package tools

import (
	"testing"

	"koine/core/srcinfo"
)

func TestRegistryKeepsWhereToolsCameFrom(t *testing.T) {
	r := NewRegistry()
	r.Register(&Func{N: "a"})
	r.Register(&Func{N: "b"})
	if _, ok := r.SourceOf("a"); ok {
		t.Fatal("nothing recorded yet")
	}
	pkg := srcinfo.Package("/pkg/ext.ts", "npm:acme@1.0.0", "user", "/pkg")
	r.SetSource("a", pkg)
	r.SetSource("b", srcinfo.Builtin("b"))
	if got, ok := r.SourceOf("a"); !ok || got != pkg || got.Origin != "package" || got.BaseDir != "/pkg" {
		t.Errorf("a: %+v", got)
	}
	if got, _ := r.SourceOf("b"); got.Path != "builtin:b" || got.Source != "builtin" || got.Scope != "temporary" || got.Origin != "top-level" {
		t.Errorf("b: %+v", got)
	}
	if f := r.Filtered([]string{"a"}, nil); true {
		if got, ok := f.SourceOf("a"); !ok || got != pkg {
			t.Errorf("a filtered copy must keep its source: %+v", got)
		}
		if _, ok := f.SourceOf("b"); ok {
			t.Error("b is not in the copy")
		}
	}
	r.Register(&Func{N: "a"}) // a replacement does not inherit the old provenance
	if _, ok := r.SourceOf("a"); ok {
		t.Error("stale source")
	}
	if !(srcinfo.Info{}).IsZero() || pkg.IsZero() {
		t.Error("IsZero")
	}
}
