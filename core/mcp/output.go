package mcp

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"strings"
	"unicode/utf8"
)

// OutputLimit is the longest text a result may have before its middle is cut out (Pi: 20 KB).
const OutputLimit = 20 * 1024

// Image is image data from a result, base64 encoded.
type Image struct{ Mime, Data string }

// Rendered is a tool result as the model sees it: text, images, and whether the server called it an error.
type Rendered struct {
	Text    string
	Images  []Image
	IsError bool
}

// Render converts a tools/call result. Text and embedded text resources become text, images become images, binary resources are saved to a
// temporary file and named in the text, and a result with only structuredContent shows that JSON. Text over OutputLimit is cut in the
// middle, with a marker that counts what was cut, and the full text is saved to a temporary file whose path ends the result.
func Render(res *CallResult) Rendered {
	out := Rendered{IsError: res.IsError}
	var parts []string
	hasText := false
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			hasText = true
			parts = append(parts, c.Text)
		case "image":
			out.Images = append(out.Images, Image{Mime: c.MimeType, Data: c.Data})
			parts = append(parts, fmt.Sprintf("[image: %s]", c.MimeType))
		case "audio":
			parts = append(parts, fmt.Sprintf("[audio %s, %d bytes of base64 omitted]", c.MimeType, len(c.Data)))
		case "resource_link":
			parts = append(parts, fmt.Sprintf("[resource link: %s %s]", c.Name, c.URI))
		case "resource":
			if c.Resource != nil {
				text, img := resourceBody(*c.Resource)
				hasText = hasText || c.Resource.Text != ""
				if img != nil {
					out.Images = append(out.Images, *img)
				}
				parts = append(parts, text)
			}
		default:
			parts = append(parts, fmt.Sprintf("[unsupported content of type %q]", c.Type))
		}
	}
	if !hasText && len(res.StructuredContent) > 0 && string(res.StructuredContent) != "null" {
		parts = append(parts, string(res.StructuredContent))
	}
	out.Text = LimitText(strings.Join(parts, "\n"))
	return out
}

// resourceBody renders one resource body: its text, or for a blob an image (returned) or a temporary file (named in the text).
func resourceBody(r Resource) (string, *Image) {
	if r.Blob == "" {
		return r.Text, nil
	}
	if strings.HasPrefix(r.MimeType, "image/") {
		return fmt.Sprintf("[image resource %s: %s]", r.URI, r.MimeType), &Image{Mime: r.MimeType, Data: r.Blob}
	}
	raw, err := base64.StdEncoding.DecodeString(r.Blob)
	if err != nil {
		return fmt.Sprintf("[binary resource %s: invalid base64]", r.URI), nil
	}
	ext := ".bin"
	if exts, _ := mime.ExtensionsByType(r.MimeType); len(exts) > 0 {
		ext = exts[0]
	}
	path, err := saveTemp("koine-mcp-*"+ext, raw)
	if err != nil {
		return fmt.Sprintf("[binary resource %s (%s, %d bytes) could not be saved: %v]", r.URI, r.MimeType, len(raw), err), nil
	}
	return fmt.Sprintf("[binary resource %s (%s, %d bytes) saved to %s]", r.URI, r.MimeType, len(raw), path), nil
}

func saveTemp(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern) // mode 0600
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return "", werr
	}
	return f.Name(), cerr
}

// LimitText cuts the middle out of text longer than OutputLimit characters and saves the whole text to a temporary file.
func LimitText(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= OutputLimit {
		return s
	}
	runes := []rune(s)
	half := OutputLimit / 2
	cut := n - 2*half
	head, tail := string(runes[:half]), string(runes[n-half:])
	note := ""
	if path, err := saveTemp("koine-mcp-*.txt", []byte(s)); err == nil {
		note = fmt.Sprintf("\n[full output saved to %s]", path)
	}
	return fmt.Sprintf("%s\n…%d chars truncated…\n%s%s", head, cut, tail, note)
}
