package mcp

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestRenderContentKinds(t *testing.T) {
	res := &CallResult{Content: []Content{
		{Type: "text", Text: "one"},
		{Type: "image", Data: "AAAA", MimeType: "image/jpeg"},
		{Type: "audio", Data: "AAAA", MimeType: "audio/wav"},
		{Type: "resource_link", Name: "doc", URI: "file:///d"},
		{Type: "resource", Resource: &Resource{URI: "file:///t", Text: "embedded"}},
		{Type: "resource", Resource: &Resource{URI: "file:///i", MimeType: "image/png", Blob: "AAAA"}},
		{Type: "resource", Resource: &Resource{URI: "file:///bad", MimeType: "application/pdf", Blob: "!!!"}},
		{Type: "resource"},
		{Type: "hologram"},
	}}
	r := Render(res)
	for _, want := range []string{"one", "[image: image/jpeg]", "[audio audio/wav, 4 bytes", "[resource link: doc file:///d]", "embedded",
		"[image resource file:///i: image/png]", "invalid base64", `unsupported content of type "hologram"`} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, r.Text)
		}
	}
	if len(r.Images) != 2 || r.IsError {
		t.Fatalf("images = %v", r.Images)
	}
	if got := Render(&CallResult{IsError: true}); !got.IsError || got.Text != "" {
		t.Fatalf("empty error result = %+v", got)
	}
	// structuredContent is shown only when there is no text, and null is ignored
	if got := Render(&CallResult{Content: []Content{{Type: "text", Text: "t"}}, StructuredContent: []byte(`{"a":1}`)}); got.Text != "t" {
		t.Fatalf("text wins: %q", got.Text)
	}
	if got := Render(&CallResult{StructuredContent: []byte(`null`)}); got.Text != "" {
		t.Fatalf("null: %q", got.Text)
	}
}

func TestBinaryResourceFileExtension(t *testing.T) {
	text, img := resourceBody(Resource{URI: "u", MimeType: "application/pdf", Blob: base64.StdEncoding.EncodeToString([]byte("%PDF"))})
	if img != nil || !strings.Contains(text, "(application/pdf, 4 bytes) saved to ") {
		t.Fatalf("text = %q", text)
	}
	path := text[strings.LastIndex(text, " ")+1 : len(text)-1]
	defer func() { _ = os.Remove(path) }()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("temp file: %v %v", info, err)
	}
	t.Setenv("TMPDIR", "/nonexistent-dir-for-test")
	if text, _ := resourceBody(Resource{URI: "u", Blob: base64.StdEncoding.EncodeToString([]byte("x"))}); !strings.Contains(text, "could not be saved") {
		t.Fatalf("unwritable temp dir: %q", text)
	}
	if out := LimitText(strings.Repeat("x", OutputLimit+5)); !strings.Contains(out, "…5 chars truncated…") {
		t.Fatalf("no temp dir still truncates: %.60q", out)
	}
}

func TestLimitText(t *testing.T) {
	short := strings.Repeat("é", OutputLimit)
	if LimitText(short) != short {
		t.Fatal("text at the limit is untouched")
	}
	long := strings.Repeat("a", 10240) + strings.Repeat("m", 7777) + strings.Repeat("z", 10240)
	out := LimitText(long)
	if !strings.HasPrefix(out, strings.Repeat("a", 10240)+"\n…7777 chars truncated…\n"+strings.Repeat("z", 10240)) {
		t.Fatalf("middle cut wrong: %.60q", out)
	}
	path := out[strings.LastIndex(out, "saved to ")+9 : len(out)-1]
	b, err := os.ReadFile(path)
	defer func() { _ = os.Remove(path) }()
	if err != nil || string(b) != long {
		t.Fatalf("saved text differs (%d bytes, %v)", len(b), err)
	}
}
