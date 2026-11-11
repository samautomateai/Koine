package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/llm"
)

func writeImage(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExpandFilesMakesImagesFromContentAndKeepsTextFiles(t *testing.T) {
	dir := t.TempDir()
	writeImage(t, dir, "plain.dat", 8, 8)
	writeImage(t, dir, "wide.png", 2300, 4)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.bmp"), append([]byte("BM"), make([]byte, 40)...), 0o600); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "a.txt")
	at, err := ExpandFiles(dir, []string{"a.txt", abs, "plain.dat", "wide.png", "x.bmp"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(at.Text, "alpha") != 2 || !strings.Contains(at.Text, `<file name="a.txt">`) ||
		!strings.Contains(at.Text, `<file name="plain.dat"></file>`) {
		t.Errorf("text: %q", at.Text)
	}
	if len(at.Images) != 3 || at.Images[0].MimeType() != "image/png" || at.Images[2].MimeType() != "image/bmp" {
		t.Fatalf("images: %d", len(at.Images))
	}
	if len(at.Notes) != 2 || !strings.Contains(at.Notes[0], "@wide.png: image resized from 2300x4 to 2000x3") ||
		!strings.Contains(at.Notes[1], "cannot resize") {
		t.Errorf("notes: %q", at.Notes)
	}
	if !strings.Contains(at.Text, `<file name="wide.png">[image resized from 2300x4 to 2000x3]</file>`) {
		t.Errorf("the model is told about the resize: %q", at.Text)
	}
	if _, err := ExpandFiles(dir, []string{"missing.txt"}, true); err == nil || !strings.Contains(err.Error(), "@missing.txt") {
		t.Errorf("want a named error, got %v", err)
	}
	if _, err := ExpandFiles(dir, []string{dir}, true); err == nil || !strings.Contains(err.Error(), "@"+dir) {
		t.Errorf("a directory is not a file: %v", err)
	}
	if at, _ := ExpandFiles(dir, []string{"wide.png"}, false); len(at.Notes) != 0 {
		t.Errorf("no resize note when autoResize is off: %q", at.Notes)
	}
}

func TestImageSettingsMapToTheAgentAndProjectOverridesUser(t *testing.T) {
	a, _ := settingsApp(t, `{"images":{"autoResize":false}}`, "", Options{})
	if o := a.Agent.Images; !o.NoResize || o.Block {
		t.Errorf("user settings: %+v", o)
	}
	a, _ = settingsApp(t, `{"images":{"autoResize":false,"blockImages":true}}`, `{"images":{"autoResize":true}}`, Options{})
	if o := a.Agent.Images; o.NoResize || !o.Block {
		t.Errorf("a project value wins and the rest is kept: %+v", o)
	}
	a, _ = settingsApp(t, "", "", Options{})
	if o := a.Agent.Images; o.NoResize || o.Block {
		t.Errorf("Pi's defaults are resize on and block off: %+v", o)
	}
	a, _ = settingsApp(t, `{"images":{"autoResize":"yes","blockImages":1}}`, "", Options{})
	if o := a.Agent.Images; o.NoResize || o.Block {
		t.Errorf("values of the wrong type are ignored: %+v", o)
	}
}

func TestAnUntrustedProjectMayStillSetTheImageSettings(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_TRUST", "")
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".koine", "settings.json"), `{"images":{"blockImages":true,"autoResize":false}}`)
	a, err := NewWithOptions(context.Background(), proj, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Trusted || !a.Agent.Images.Block || !a.Agent.Images.NoResize {
		t.Errorf("trusted=%v images=%+v", a.Trusted, a.Agent.Images)
	}
}

func TestPipedInputIsAnImageOrText(t *testing.T) {
	a, proj := settingsApp(t, "", "", Options{})
	at, err := a.PipedInput([]byte("  some log\n"))
	if err != nil || at.Text != "some log" || len(at.Images) != 0 {
		t.Errorf("text: %+v %v", at, err)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2100, 2)))
	at, err = a.PipedInput(b.Bytes())
	if err != nil || len(at.Images) != 1 || at.Text != "" || len(at.Notes) != 1 || !strings.HasPrefix(at.Notes[0], "stdin: image resized") {
		t.Errorf("image: %+v %v", at, err)
	}
	if _, err := a.PipedInput([]byte("\x89PNG\r\n\x1a\njunk")); err == nil || !strings.HasPrefix(err.Error(), "stdin:") {
		t.Errorf("broken image: %v", err)
	}
	_ = proj
}

func TestHandleInputWithImagesSendsThemAndIgnoresThemForSlashCommands(t *testing.T) {
	a, _ := settingsApp(t, `{"defaultModel":"claude-sonnet-5-5"}`, "", Options{})
	c := &capture{}
	a.Agent.Provider = c
	img := llm.ImageBlock("image/png", "QUJD")
	var evs []agent.Event
	emit := func(e agent.Event) { evs = append(evs, e) }
	if _, err := a.HandleInputImages(context.Background(), "", []llm.Block{img}, emit); err != nil || llm.CountImages(c.reqs[0].Messages) != 1 {
		t.Fatalf("image-only prompt: %v", err)
	}
	if _, err := a.HandleInputImages(context.Background(), "/tools", []llm.Block{img}, emit); err != nil || !strings.Contains(notices(evs), "1 dropped") {
		t.Errorf("slash command with images: %v %q", err, notices(evs))
	}
	if q, err := a.HandleInputImages(context.Background(), "", nil, emit); q || err != nil || len(c.reqs) != 1 {
		t.Error("nothing at all is not a prompt")
	}
}
