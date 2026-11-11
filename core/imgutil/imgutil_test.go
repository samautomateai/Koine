package imgutil

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testImage(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 13), B: uint8(x ^ y), A: 255})
		}
	}
	return m
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, testImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodeSize(t *testing.T, img Image) (int, int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func TestSniffGoesByContentNotExtension(t *testing.T) {
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 8)...)
	bmp := append([]byte("BM"), make([]byte, 40)...)
	jpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF")
	cases := map[string][]byte{
		"image/png": pngBytes(t, 2, 2), "image/jpeg": jpg, "image/gif": []byte("GIF89a\x01\x00"), "image/webp": webp, "image/bmp": bmp,
		"": []byte("just text, even if it is called a.png"),
	}
	for want, data := range cases {
		if got := Sniff(data); got != want {
			t.Errorf("Sniff(%q...) = %q, want %q", data[:min(len(data), 8)], got, want)
		}
	}
	if Sniff(nil) != "" || Sniff([]byte("BM")) != "" || Sniff([]byte("RIFF....WAVE....")) != "" {
		t.Error("short or look-alike data must not be sniffed as an image")
	}
}

func TestPrepareKeepsSmallImagesUnchanged(t *testing.T) {
	data := pngBytes(t, 40, 30)
	img, err := Prepare(data, true)
	if err != nil || img.Mime != "image/png" || img.Note != "" || img.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("img=%+v err=%v", img, err)
	}
}

func TestPrepareResizesToTheDocumentedLimit(t *testing.T) {
	img, err := Prepare(pngBytes(t, 3000, 60), true)
	if err != nil {
		t.Fatal(err)
	}
	w, h := decodeSize(t, img)
	if w != 2000 || h != 40 || img.Mime != "image/png" || !strings.Contains(img.Note, "3000x60 to 2000x40") {
		t.Errorf("got %dx%d %s note=%q", w, h, img.Mime, img.Note)
	}
	tall, err := Prepare(pngBytes(t, 50, 2500), true)
	if w, h := decodeSize(t, tall); err != nil || w != 40 || h != 2000 {
		t.Errorf("tall image resized to %dx%d, err=%v", w, h, err)
	}
}

func TestPrepareWithoutResizeSendsTheOriginal(t *testing.T) {
	data := pngBytes(t, 2100, 10)
	img, err := Prepare(data, false)
	if err != nil || img.Data != base64.StdEncoding.EncodeToString(data) || img.Note != "" {
		t.Fatalf("img note=%q err=%v", img.Note, err)
	}
	if _, err := Prepare([]byte("plain text"), false); err == nil {
		t.Error("text is not an image")
	}
}

func TestPrepareJPEGStaysJPEGAndGIFBecomesPNG(t *testing.T) {
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, testImage(2400, 20), nil); err != nil {
		t.Fatal(err)
	}
	j, err := Prepare(jb.Bytes(), true)
	if w, _ := decodeSize(t, j); err != nil || j.Mime != "image/jpeg" || w != 2000 {
		t.Errorf("jpeg: %+v w=%d err=%v", j.Mime, w, err)
	}
	pal := image.NewPaletted(image.Rect(0, 0, 2100, 20), color.Palette{color.Black, color.White})
	var gb bytes.Buffer
	if err := gif.Encode(&gb, pal, nil); err != nil {
		t.Fatal(err)
	}
	g, err := Prepare(gb.Bytes(), true)
	if w, _ := decodeSize(t, g); err != nil || g.Mime != "image/png" || w != 2000 {
		t.Errorf("gif: %+v w=%d err=%v", g.Mime, w, err)
	}
	small := image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Black, color.White})
	gb.Reset()
	_ = gif.Encode(&gb, small, nil)
	if g, err := Prepare(gb.Bytes(), true); err != nil || g.Mime != "image/gif" {
		t.Errorf("a small gif is passed through: %+v %v", g.Mime, err)
	}
}

func TestPrepareWebPAndBMPPassThroughAndSayWhy(t *testing.T) {
	bmp := append([]byte("BM"), make([]byte, 40)...)
	img, err := Prepare(bmp, true)
	if err != nil || img.Mime != "image/bmp" || !strings.Contains(img.Note, "cannot resize") {
		t.Fatalf("bmp: %+v %v", img, err)
	}
	old := maxEncoded
	maxEncoded = 20
	defer func() { maxEncoded = old }()
	if _, err := Prepare(bmp, true); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("an oversized bmp cannot be shrunk and must be refused: %v", err)
	}
}

func TestPrepareShrinksUntilThePayloadFits(t *testing.T) {
	data := pngBytes(t, 400, 400)
	old := maxEncoded
	maxEncoded = len(base64.StdEncoding.EncodeToString(data)) / 3
	defer func() { maxEncoded = old }()
	img, err := Prepare(data, true)
	if err != nil || len(img.Data) > maxEncoded {
		t.Fatalf("size %d limit %d err %v", len(img.Data), maxEncoded, err)
	}
	if w, _ := decodeSize(t, img); w >= 400 || !strings.Contains(img.Note, "resized from 400x400") {
		t.Errorf("w=%d note=%q", w, img.Note)
	}
	maxEncoded = 10
	if _, err := Prepare(data, true); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("an impossible limit must fail: %v", err)
	}
}

func TestPrepareReportsCorruptImages(t *testing.T) {
	bad := append(pngBytes(t, 20, 20)[:30], 0, 0, 0)
	if _, err := Prepare(bad, true); err == nil || !strings.Contains(err.Error(), "cannot decode") {
		t.Errorf("truncated png: %v", err)
	}
	// valid header, broken body: DecodeConfig works, Decode fails
	big := pngBytes(t, 2100, 4)
	if _, err := Prepare(big[:len(big)-40], true); err == nil || !strings.Contains(err.Error(), "cannot decode") {
		t.Errorf("broken body: %v", err)
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// a PNG with a misleading extension is still an image; a text file named .png is not
	img, ok, err := ReadFile(write("photo.txt", pngBytes(t, 10, 10)), true)
	if err != nil || !ok || img.Mime != "image/png" {
		t.Errorf("png named .txt: %+v ok=%v err=%v", img, ok, err)
	}
	if _, ok, err := ReadFile(write("fake.png", []byte("hello")), true); ok || err != nil {
		t.Errorf("text named .png: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ReadFile(filepath.Join(dir, "none.png"), true); ok || err == nil {
		t.Errorf("missing file: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ReadFile(write("bad.png", append(pngBytes(t, 20, 20)[:30], 0)), true); !ok || err == nil {
		t.Errorf("corrupt image: ok=%v err=%v", ok, err)
	}
	maxFile = 100
	defer func() { maxFile = MaxFileBytes }()
	huge := write("huge.bmp", append([]byte("BM"), make([]byte, 200)...))
	if _, ok, err := ReadFile(huge, false); !ok || err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("huge file: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ReadFile(dir, true); err != nil && ok {
		t.Errorf("directory: ok=%v err=%v", ok, err)
	}
}
