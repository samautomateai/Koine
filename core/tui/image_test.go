package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"testing"
)

func envOf(m map[string]string) Env { return func(k string) string { return m[k] } }

func testImage(w, h int) image.Image {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			im.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	return im
}

func pngData(t *testing.T, w, h int) ImageData {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, testImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return ImageData{MIME: "image/png", Data: base64.StdEncoding.EncodeToString(b.Bytes())}
}

func TestDetectImageProtocol(t *testing.T) {
	cases := []struct {
		name, setting string
		env           map[string]string
		want          string
	}{
		{"kitty window id", "auto", map[string]string{"KITTY_WINDOW_ID": "3", "TERM": "xterm-256color"}, "kitty"},
		{"kitty term", "auto", map[string]string{"TERM": "xterm-kitty"}, "kitty"},
		{"ghostty", "auto", map[string]string{"TERM_PROGRAM": "ghostty"}, "kitty"},
		{"ghostty term", "auto", map[string]string{"TERM": "xterm-ghostty"}, "kitty"},
		{"iterm", "auto", map[string]string{"TERM_PROGRAM": "iTerm.app"}, "iterm2"},
		{"iterm lc_terminal over ssh", "auto", map[string]string{"LC_TERMINAL": "iTerm2"}, "iterm2"},
		{"wezterm", "auto", map[string]string{"TERM_PROGRAM": "WezTerm"}, "iterm2"},
		{"plain xterm", "auto", map[string]string{"TERM": "xterm-256color"}, ""},
		{"dumb", "auto", map[string]string{"TERM": "dumb", "KITTY_WINDOW_ID": "1"}, ""},
		{"inside tmux", "auto", map[string]string{"TMUX": "/tmp/tmux-0/default,1,0", "KITTY_WINDOW_ID": "1"}, ""},
		{"inside screen", "auto", map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "iTerm.app"}, ""},
		{"nothing", "auto", nil, ""},
		{"off", "false", map[string]string{"KITTY_WINDOW_ID": "3"}, ""},
		{"forced kitty", "kitty", map[string]string{"TERM": "xterm"}, "kitty"},
		{"forced iterm2", "iterm2", nil, "iterm2"},
		{"forced kitty still off on dumb", "kitty", map[string]string{"TERM": "dumb"}, ""},
	}
	for _, c := range cases {
		if got := DetectImageProtocol(c.setting, envOf(c.env)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestImagePlaceholder(t *testing.T) {
	p := pngData(t, 640, 480)
	got := ImagePlaceholder(p)
	if !strings.HasPrefix(got, "[image: image/png 640x480 ") || !strings.HasSuffix(got, "B]") {
		t.Errorf("placeholder: %q", got)
	}
	if got := ImagePlaceholder(ImageData{MIME: "image/webp", Data: "AAAA"}); got != "[image: image/webp 3 B]" {
		t.Errorf("unknown dimensions: %q", got)
	}
	big := ImageData{MIME: "image/bmp", Data: strings.Repeat("A", 4*2048)}
	if got := ImagePlaceholder(big); got != "[image: image/bmp 6.0 KB]" {
		t.Errorf("kilobytes: %q", got)
	}
	if got := ImagePlaceholder(ImageData{}); got != "[image 0 B]" {
		t.Errorf("empty: %q", got)
	}
}

func TestImageSize(t *testing.T) {
	cols, rows := imageCells(800, 400, 60, 100)
	if cols != 60 || rows != 15 {
		t.Errorf("wide image: %dx%d", cols, rows)
	}
	if cols, rows = imageCells(40, 40, 60, 100); cols != 5 || rows != 3 { // not enlarged beyond its pixels (8x16 px cells)
		t.Errorf("small image: %dx%d", cols, rows)
	}
	if cols, _ = imageCells(800, 400, 60, 20); cols != 20 {
		t.Errorf("narrow terminal: %d", cols)
	}
	if _, rows = imageCells(100, 4000, 60, 100); rows != maxImageRows {
		t.Errorf("tall image: %d", rows)
	}
}

func TestKittyImageSequence(t *testing.T) {
	d := pngData(t, 100, 100)
	lines := RenderImage(d, ImageOptions{Proto: "kitty", Cells: 10, Avail: 80})
	if len(lines) != 5 { // 10 cells of 8x16 px are 80 px wide, so a square image is 5 rows tall
		t.Fatalf("rows: %d", len(lines))
	}
	for _, l := range lines[:len(lines)-1] {
		if l != "" {
			t.Errorf("rows above the last must be blank: %q", l)
		}
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "\x1b7\x1b[4A\x1b_Ga=T,f=100,q=2,C=1,c=10,r=5,") || !strings.HasSuffix(last, "\x1b\\\x1b8") {
		t.Errorf("last line: %q", last[:min(len(last), 80)])
	}
	if !strings.Contains(last, ";") || strings.Count(last, "\x1b_G") != strings.Count(last, "\x1b\\") {
		t.Errorf("unbalanced APC: %q", last[:60])
	}
}

func TestKittyChunksLongData(t *testing.T) {
	noise := image.NewRGBA(image.Rect(0, 0, 120, 120))
	rand.New(rand.NewSource(1)).Read(noise.Pix) // incompressible, so the PNG is far over one 4096-byte chunk
	var buf bytes.Buffer
	if err := png.Encode(&buf, noise); err != nil {
		t.Fatal(err)
	}
	d := ImageData{MIME: "image/png", Data: base64.StdEncoding.EncodeToString(buf.Bytes())}
	lines := RenderImage(d, ImageOptions{Proto: "kitty", Cells: 20, Avail: 80})
	last := lines[len(lines)-1]
	parts := strings.Split(last, "\x1b_G")
	if len(parts) < 3 {
		t.Fatalf("expected several chunks, got %d", len(parts)-1)
	}
	if !strings.Contains(parts[1], "m=1;") || !strings.HasPrefix(parts[2], "m=1;") && !strings.HasPrefix(parts[2], "m=0;") {
		t.Errorf("chunk headers: %q / %q", parts[1][:40], parts[2][:12])
	}
	if !strings.HasPrefix(parts[len(parts)-1], "m=0;") {
		t.Errorf("last chunk must end the transfer: %q", parts[len(parts)-1][:12])
	}
	for _, p := range parts[1:] {
		body := p[strings.Index(p, ";")+1 : strings.Index(p, "\x1b\\")]
		if len(body) > 4096 {
			t.Errorf("chunk of %d bytes", len(body))
		}
	}
}

func TestKittyConvertsJPEGAndGIFToPNG(t *testing.T) {
	var j, g bytes.Buffer
	if err := jpeg.Encode(&j, testImage(64, 32), nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&g, testImage(64, 32), nil); err != nil {
		t.Fatal(err)
	}
	for mime, raw := range map[string][]byte{"image/jpeg": j.Bytes(), "image/gif": g.Bytes()} {
		d := ImageData{MIME: mime, Data: base64.StdEncoding.EncodeToString(raw)}
		lines := RenderImage(d, ImageOptions{Proto: "kitty", Cells: 8, Avail: 80})
		last := lines[len(lines)-1]
		if !strings.Contains(last, "f=100") || strings.Contains(last, d.Data[:40]) {
			t.Errorf("%s was not converted to PNG", mime)
		}
	}
}

func TestITermImageSequence(t *testing.T) {
	d := pngData(t, 160, 80)
	lines := RenderImage(d, ImageOptions{Proto: "iterm2", Cells: 20, Avail: 80})
	if len(lines) != 5 {
		t.Fatalf("rows: %d", len(lines))
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "\x1b7\x1b[4A\x1b]1337;File=inline=1;width=20;height=5;preserveAspectRatio=1;size=") ||
		!strings.Contains(last, ":"+d.Data+"\a") || !strings.HasSuffix(last, "\x1b8") {
		t.Errorf("last line: %q", last[:min(len(last), 120)])
	}
}

func TestImageFallsBackToAPlaceholder(t *testing.T) {
	p := pngData(t, 100, 50)
	for name, o := range map[string]ImageOptions{
		"no protocol": {Cells: 10, Avail: 80},
		"hidden":      {Proto: "kitty", Cells: 10, Avail: 80, Hidden: true},
	} {
		lines := RenderImage(p, o)
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "[image: image/png 100x50") {
			t.Errorf("%s: %q", name, lines)
		}
	}
	webp := ImageData{MIME: "image/webp", Data: base64.StdEncoding.EncodeToString([]byte("RIFFxxxxWEBPVP8 "))}
	if lines := RenderImage(webp, ImageOptions{Proto: "kitty", Cells: 10, Avail: 80}); len(lines) != 1 || !strings.HasPrefix(lines[0], "[image: image/webp") {
		t.Errorf("webp: %q", lines)
	}
	corrupt := ImageData{MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not a png at all"))}
	lines := RenderImage(corrupt, ImageOptions{Proto: "iterm2", Cells: 10, Avail: 80})
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "[image: image/png") {
		t.Errorf("corrupt: %q", lines)
	}
	if lines := RenderImage(ImageData{MIME: "image/png", Data: "!!!"}, ImageOptions{Proto: "kitty", Cells: 10, Avail: 80}); len(lines) != 1 {
		t.Errorf("bad base64: %q", lines)
	}
}

func TestBlocksCarryImages(t *testing.T) {
	d := pngData(t, 100, 50)
	th := Builtin("dark")
	b := Block{Kind: "user", Text: "look", Final: true, Images: []ImageData{d}}
	plain := RenderBlock(b, RenderCtx{Width: 60, Theme: th, OutputPad: 1})
	if len(plain) < 2 || !strings.Contains(plain[len(plain)-1], "[image: image/png 100x50") {
		t.Errorf("placeholder block: %q", plain)
	}
	drawn := RenderBlock(b, RenderCtx{Width: 60, Theme: th, OutputPad: 1, ImageProto: "kitty", ImageCells: 10, ShowImages: true})
	if got := drawn[len(drawn)-1]; !strings.Contains(got, "\x1b_Ga=T") || !strings.HasPrefix(got, " \x1b7") {
		t.Errorf("drawn block: %q", got[:min(len(got), 40)])
	}
	tool := Block{Kind: "tool", Final: true, Tool: &ToolCall{Name: "read", Args: []byte(`{"path":"a.png"}`), Done: true, Result: "Read image file [image/png]",
		Images: []ImageData{d}}}
	out := RenderBlock(tool, RenderCtx{Width: 60, Theme: th, ImageProto: "iterm2", ImageCells: 10, ShowImages: true})
	if !strings.Contains(strings.Join(out, "\n"), "1337;File=") {
		t.Errorf("tool image not drawn: %q", out)
	}
}
