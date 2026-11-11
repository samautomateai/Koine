package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ImageSource is the data of an image block: base64 bytes and their media type. The JSON is Anthropic's source object.
type ImageSource struct {
	Type      string `json:"type"` // always base64
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// ImageBlock is an image block for base64 data of the given media type ("image/png").
func ImageBlock(mime, base64Data string) Block {
	return Block{Type: "image", Source: &ImageSource{Type: "base64", MediaType: mime, Data: base64Data}}
}

// MimeType and Base64 read an image block; both are empty for other blocks.
func (b Block) MimeType() string {
	if b.Source == nil {
		return ""
	}
	return b.Source.MediaType
}

func (b Block) Base64() string {
	if b.Source == nil {
		return ""
	}
	return b.Source.Data
}

// MarshalJSON writes a tool_result that carries images with a content array (text, then images), as the Anthropic API wants it.
func (b Block) MarshalJSON() ([]byte, error) {
	type plain Block
	if b.Type != "tool_result" || len(b.Images) == 0 {
		return json.Marshal(plain(b))
	}
	var parts []Block
	if b.Content != "" {
		parts = append(parts, Block{Type: "text", Text: b.Content})
	}
	parts = append(parts, b.Images...)
	return json.Marshal(struct {
		plain
		Content []Block `json:"content"`
	}{plain(b), parts})
}

// CountImages is the number of image blocks in msgs, tool results' images included.
func CountImages(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "image" {
				n++
			}
			n += len(b.Images)
		}
	}
	return n
}

// StripImages returns msgs with every image replaced by a text note naming why it is missing ("[image omitted: <why>]"), and how many were
// replaced. msgs is not changed. With no images it returns msgs itself.
func StripImages(msgs []Message, why string) ([]Message, int) {
	if CountImages(msgs) == 0 {
		return msgs, 0
	}
	note := "[image omitted: " + why + "]"
	n := 0
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		out[i].Content = make([]Block, 0, len(m.Content))
		for _, b := range m.Content {
			switch {
			case b.Type == "image":
				n++
				out[i].Content = append(out[i].Content, Block{Type: "text", Text: note})
			case len(b.Images) > 0:
				n += len(b.Images)
				b.Content = strings.TrimSpace(b.Content + "\n" + strings.Repeat(note+"\n", len(b.Images)))
				b.Images = nil
				out[i].Content = append(out[i].Content, b)
			default:
				out[i].Content = append(out[i].Content, b)
			}
		}
	}
	return out, n
}

// AcceptsImages reports whether the model takes image input. A model the catalog does not know, or one it lists without an input list,
// is allowed: the provider then answers for itself.
func (c *Catalog) AcceptsImages(id string) bool {
	m, ok := c.Lookup(id)
	if !ok || len(m.Input) == 0 {
		return true
	}
	for _, in := range m.Input {
		if in == "image" {
			return true
		}
	}
	return false
}

// NoImagesWhy is the reason given for images left out because the model cannot read them.
func NoImagesWhy(model string) string { return fmt.Sprintf("model %s does not accept images", model) }
