package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 9, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readWith(t *testing.T, r *Registry, sink ImageSink, args string) (string, error) {
	t.Helper()
	tl, _ := r.Get("read")
	return tl.Run(WithImages(context.Background(), sink), json.RawMessage(args))
}

func TestReadAttachesImagesByContentNotExtension(t *testing.T) {
	r, dir := setup(t)
	writePNG(t, filepath.Join(dir, "shot.txt"), 30, 20) // a PNG with a text name is still an image
	var got []Image
	out, err := readWith(t, r, ImageSink{Add: func(i Image) { got = append(got, i) }, Resize: true}, `{"path":"shot.txt"}`)
	if err != nil || len(got) != 1 || got[0].Mime != "image/png" || got[0].Data == "" || !strings.Contains(out, "Read image file [image/png]") {
		t.Fatalf("out=%q err=%v images=%d", out, err, len(got))
	}
}

func TestReadResizesLargeImagesAndSaysSo(t *testing.T) {
	r, dir := setup(t)
	writePNG(t, filepath.Join(dir, "wide.png"), 2400, 10)
	var got []Image
	out, err := readWith(t, r, ImageSink{Add: func(i Image) { got = append(got, i) }, Resize: true}, `{"path":"wide.png"}`)
	if err != nil || !strings.Contains(out, "resized from 2400x10 to 2000x8") || len(got) != 1 {
		t.Fatalf("out=%q err=%v", out, err)
	}
	got = nil
	out, err = readWith(t, r, ImageSink{Add: func(i Image) { got = append(got, i) }}, `{"path":"wide.png"}`) // images.autoResize off
	if err != nil || strings.Contains(out, "resized") || len(got) != 1 {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestReadImageWithoutASinkSaysItWasNotAttached(t *testing.T) {
	r, dir := setup(t)
	writePNG(t, filepath.Join(dir, "a.png"), 4, 4)
	out, err := run(t, r, "read", `{"path":"a.png"}`)
	if err != nil || !strings.Contains(out, "not attached") {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestReadStillReadsTextAndReportsBadImages(t *testing.T) {
	r, dir := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "fake.png"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, r, "read", `{"path":"fake.png"}`); err != nil || !strings.Contains(out, "hello") {
		t.Errorf("text named .png: %q %v", out, err)
	}
	bad := append([]byte("\x89PNG\r\n\x1a\n"), []byte("not really")...)
	if err := os.WriteFile(filepath.Join(dir, "bad.png"), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWith(t, r, ImageSink{Add: func(Image) {}, Resize: true}, `{"path":"bad.png"}`); err == nil {
		t.Error("a corrupt image must be an error")
	}
}
