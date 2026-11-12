package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif" // registers the GIF decoder for image.Decode
	_ "image/jpeg"
	"image/png"
	"math"
	"strings"
)

// ImageData is one image in the transcript: its media type and base64 data, as the conversation holds it.
type ImageData struct{ MIME, Data string }

// ImageOptions say how one image is drawn. Proto is "kitty", "iterm2" or "" (a text placeholder). Cells is terminal.imageWidthCells and
// Avail the columns left in the transcript; Hidden (terminal.showImages false) forces the placeholder.
type ImageOptions struct {
	Proto  string
	Cells  int
	Avail  int
	Hidden bool
}

const (
	// A terminal cell is taken to be 8 by 16 pixels: no terminal reports its cell size without a query that needs a reply from the tty,
	// and a wrong guess only changes how many rows the picture is given.
	cellW, cellH = 8, 16
	maxImageRows = 40
	kittyChunk   = 4096
)

// DetectImageProtocol decides which inline-image protocol to use. setting is terminal.images: "false" turns images off, "kitty" and
// "iterm2" force a protocol, anything else detects it from the environment. Pi documents the override without saying how it detects, so
// the list of terminals is Koine's own. tmux and screen are never detected (they swallow the sequences unless passthrough is set up),
// and a dumb terminal never draws images, even when forced.
func DetectImageProtocol(setting string, env Env) string {
	term := env("TERM")
	if setting == "false" || term == "dumb" {
		return ""
	}
	if setting == "kitty" || setting == "iterm2" {
		return setting
	}
	switch {
	case env("TMUX") != "" || strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux"):
		return ""
	case env("KITTY_WINDOW_ID") != "" || term == "xterm-kitty" || term == "xterm-ghostty" || env("TERM_PROGRAM") == "ghostty":
		return "kitty"
	case env("TERM_PROGRAM") == "iTerm.app" || env("TERM_PROGRAM") == "WezTerm" || env("LC_TERMINAL") == "iTerm2":
		return "iterm2"
	}
	return ""
}

// decodedLen is the size of the data behind a base64 string, without decoding it.
func decodedLen(b64 string) int {
	n := len(b64) / 4 * 3
	if strings.HasSuffix(b64, "==") {
		return n - 2
	} else if strings.HasSuffix(b64, "=") {
		return n - 1
	}
	return n
}

func dims(d ImageData) (w, h int, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(d.Data)
	if err != nil {
		return 0, 0, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// ImagePlaceholder is the one-line text shown where an image cannot be drawn: its type, pixel size (when it can be read) and byte size.
func ImagePlaceholder(d ImageData) string {
	parts := []string{"[image"}
	if d.MIME != "" {
		parts[0] = "[image: " + d.MIME
	}
	if w, h, ok := dims(d); ok {
		parts = append(parts, fmt.Sprintf("%dx%d", w, h))
	}
	if n := decodedLen(d.Data); n >= 1024 {
		parts = append(parts, fmt.Sprintf("%.1f KB", float64(n)/1024))
	} else {
		parts = append(parts, fmt.Sprintf("%d B", n))
	}
	return strings.Join(parts, " ") + "]"
}

// imageCells is the size, in cells, a w by h pixel picture gets: at most cells wide and avail wide, and never enlarged past its own pixels.
func imageCells(w, h, cells, avail int) (cols, rows int) {
	cols = max(1, min(cells, avail, (w+cellW-1)/cellW))
	rows = int(math.Round(float64(h) * float64(cols*cellW) / float64(w) / cellH))
	return cols, max(1, min(rows, maxImageRows))
}

// RenderImage returns the lines for one image. A drawn image is rows-1 blank lines followed by one line that saves the cursor, moves up
// to the first row, emits the graphics sequence and restores the cursor, so the transcript's own line count stays right and scrolling
// the blank lines into place happens before the picture is placed. Anything that cannot be drawn is a single placeholder line.
func RenderImage(d ImageData, o ImageOptions) []string {
	placeholder := []string{ImagePlaceholder(d)}
	if o.Proto == "" || o.Hidden {
		return placeholder
	}
	w, h, ok := dims(d)
	if !ok {
		return placeholder
	}
	cols, rows := imageCells(w, h, o.Cells, o.Avail)
	var seq string
	switch o.Proto {
	case "kitty":
		seq = kittySeq(d, cols, rows)
	case "iterm2":
		seq = itermSeq(d, cols, rows)
	}
	if seq == "" {
		return placeholder
	}
	up := ""
	if rows > 1 {
		up = fmt.Sprintf("\x1b[%dA", rows-1)
	}
	lines := make([]string, rows)
	lines[rows-1] = "\x1b7" + up + seq + "\x1b8"
	return lines
}

// kittySeq transmits and places a PNG with the kitty graphics protocol (a=T, f=100), in chunks of at most 4096 bytes. q=2 stops the
// terminal answering on the input stream, C=1 keeps the cursor where it is. JPEG and GIF are re-encoded as PNG; other types give "".
func kittySeq(d ImageData, cols, rows int) string {
	data := d.Data
	if d.MIME != "image/png" {
		if d.MIME != "image/jpeg" && d.MIME != "image/gif" {
			return ""
		}
		raw, err := base64.StdEncoding.DecodeString(d.Data)
		if err != nil {
			return ""
		}
		im, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return ""
		}
		var b bytes.Buffer
		if png.Encode(&b, im) != nil {
			return ""
		}
		data = base64.StdEncoding.EncodeToString(b.Bytes())
	}
	var out strings.Builder
	for i := 0; i < len(data) || i == 0; i += kittyChunk {
		end := min(len(data), i+kittyChunk)
		more := 0
		if end < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&out, "\x1b_Ga=T,f=100,q=2,C=1,c=%d,r=%d,m=%d;", cols, rows, more)
		} else {
			fmt.Fprintf(&out, "\x1b_Gm=%d;", more)
		}
		out.WriteString(data[i:end] + "\x1b\\")
	}
	return out.String()
}

// itermSeq is iTerm2's inline image sequence (OSC 1337 File=), which takes PNG, JPEG and GIF as they are.
func itermSeq(d ImageData, cols, rows int) string {
	switch d.MIME {
	case "image/png", "image/jpeg", "image/gif":
		return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1;size=%d:%s\a", cols, rows, decodedLen(d.Data), d.Data)
	}
	return ""
}
