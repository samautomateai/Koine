// Package imgutil sniffs images by content and shrinks them to the size Koine sends to a model, using only the standard library.
package imgutil

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // registers the GIF decoder
	"image/jpeg"
	"image/png"
	"math"
	"os"
)

const (
	// MaxDimension is Pi's documented limit for images.autoResize: at most 2000 by 2000 pixels (https://pi.dev/docs/latest/settings).
	MaxDimension = 2000
	// MaxEncodedBytes caps the base64 payload of one image after resizing. Pi's page names the limit (images.resize.maxBytes) but not its
	// default, so this is Koine's own choice: under Anthropic's 5 MB per-image limit.
	MaxEncodedBytes = 4_500_000
	// MaxFileBytes is the largest image file Koine reads at all.
	MaxFileBytes = 25 << 20
	jpegQuality  = 85
)

// Sniff names the image type of data from its first bytes ("image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp"), or "" when
// data is not one of them. The file name plays no part.
func Sniff(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case len(data) >= 26 && bytes.HasPrefix(data, []byte("BM")):
		return "image/bmp"
	}
	return ""
}

// Image is an image ready to send: base64 data, its media type and a note for the model or the user when it was changed.
type Image struct {
	Mime string
	Data string // base64
	Note string
}

// Prepare turns file contents into an Image. It returns an error when data is not an image Koine knows or is too large to send. With
// resize, a PNG, JPEG or GIF larger than MaxDimension on either side, or whose payload exceeds MaxEncodedBytes, is scaled down (a GIF
// becomes a PNG of its first frame); WebP and BMP cannot be decoded with the standard library, so they are sent unchanged and say so.
func Prepare(data []byte, resize bool) (Image, error) {
	mime := Sniff(data)
	if mime == "" {
		return Image{}, errors.New("not a supported image (png, jpeg, gif, webp or bmp)")
	}
	out := Image{Mime: mime, Data: base64.StdEncoding.EncodeToString(data)}
	if !resize {
		return out, nil
	}
	if mime == "image/webp" || mime == "image/bmp" {
		if len(out.Data) > maxEncoded {
			return Image{}, fmt.Errorf("%s image is too large (%d bytes encoded, limit %d) and Koine cannot resize it", mime, len(out.Data), maxEncoded)
		}
		out.Note = mime + " is sent unchanged: Koine cannot resize it"
		return out, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("cannot decode %s: %w", mime, err)
	}
	if cfg.Width <= MaxDimension && cfg.Height <= MaxDimension && len(out.Data) <= maxEncoded {
		return out, nil // an animated GIF that is small enough keeps its frames
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("cannot decode %s: %w", mime, err)
	}
	return shrink(src, mime, cfg.Width, cfg.Height)
}

// shrink scales src to fit MaxDimension and, if the payload is still too big, keeps scaling down by a quarter until it fits.
func shrink(src image.Image, mime string, w, h int) (Image, error) {
	scale := min(1, float64(MaxDimension)/float64(max(w, h)))
	for range 12 {
		nw, nh := max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
		dst := src
		if nw != w || nh != h {
			dst = scaleBox(src, nw, nh)
		}
		var buf bytes.Buffer
		outMime := mime
		var err error
		if mime == "image/jpeg" {
			err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality})
		} else {
			outMime = "image/png"
			err = png.Encode(&buf, dst)
		}
		if err != nil {
			return Image{}, err
		}
		enc := base64.StdEncoding.EncodeToString(buf.Bytes())
		if len(enc) <= maxEncoded {
			note := fmt.Sprintf("image resized from %dx%d to %dx%d", w, h, nw, nh)
			if nw == w && nh == h {
				note = fmt.Sprintf("image re-encoded as %s to fit %d bytes", outMime, maxEncoded)
			}
			return Image{Mime: outMime, Data: enc, Note: note}, nil
		}
		scale *= 0.75
	}
	return Image{}, fmt.Errorf("image is too large to send even after resizing (%dx%d)", w, h)
}

// scaleBox shrinks src to w by h by averaging the source pixels under each target pixel.
func scaleBox(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			dst.Set(x, y, color.RGBA64{R: clamp16(r / n), G: clamp16(g / n), B: clamp16(bl / n), A: clamp16(a / n)})
		}
	}
	return dst
}

// ReadFile reads path and prepares it as an image. A file that is not an image gives (Image{}, false, nil); an image that cannot be
// used gives an error. Only the first bytes are read to decide, so a large text file costs nothing.
func ReadFile(path string, resize bool) (Image, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, false, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 32)
	n, _ := f.Read(head)
	if Sniff(head[:n]) == "" {
		return Image{}, false, nil
	}
	st, err := f.Stat()
	if err != nil {
		return Image{}, true, err
	}
	if st.Size() > maxFile {
		return Image{}, true, fmt.Errorf("image file is %d bytes; the limit is %d", st.Size(), maxFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Image{}, true, err
	}
	img, err := Prepare(data, resize)
	return img, true, err
}

// maxEncoded is MaxEncodedBytes; tests lower it.
var maxEncoded = MaxEncodedBytes
var maxFile int64 = MaxFileBytes

// clamp16 keeps a channel average within 16 bits.
func clamp16(v uint64) uint16 {
	if v > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(v)
}
