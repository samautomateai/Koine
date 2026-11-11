package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"koine/core/imgutil"
	"koine/core/llm"
)

// Attachments are what @file arguments and piped input turn into: text for the prompt, images to send with it, and notes for the user
// (an image was resized, or could not be).
type Attachments struct {
	Text   string
	Images []llm.Block
	Notes  []string
}

// ExpandFiles reads the @file arguments relative to dir. A file that is an image by its content (not its name) becomes an image block,
// resized when resize is set, and an empty <file> element naming it; any other file is included as text in a <file> element. A file that
// is missing or an image Koine cannot use is an error that names the argument.
func ExpandFiles(dir string, files []string, resize bool) (Attachments, error) {
	var at Attachments
	var b strings.Builder
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		img, isImage, err := imgutil.ReadFile(p, resize)
		if err != nil {
			return Attachments{}, fmt.Errorf("@%s: %w", f, err)
		}
		if isImage {
			at.Images = append(at.Images, llm.ImageBlock(img.Mime, img.Data))
			if img.Note != "" {
				at.Notes = append(at.Notes, fmt.Sprintf("@%s: %s", f, img.Note))
			}
			fmt.Fprintf(&b, "<file name=%q>%s</file>\n", f, noteText(img.Note))
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return Attachments{}, fmt.Errorf("@%s: %w", f, err)
		}
		fmt.Fprintf(&b, "<file name=%q>\n%s\n</file>\n", f, strings.TrimRight(string(data), "\n"))
	}
	at.Text = b.String()
	return at, nil
}

func noteText(note string) string {
	if note == "" {
		return ""
	}
	return "[" + note + "]"
}

// FileArgs is ExpandFiles for this project, honouring images.autoResize.
func (a *App) FileArgs(files []string) (Attachments, error) {
	return ExpandFiles(a.Cwd, files, !a.Agent.Images.NoResize)
}

// PipedInput turns what was piped to stdin into attachments: image data (by content) becomes an image, anything else is text.
func (a *App) PipedInput(data []byte) (Attachments, error) {
	if imgutil.Sniff(data) == "" {
		return Attachments{Text: strings.TrimSpace(string(data))}, nil
	}
	img, err := imgutil.Prepare(data, !a.Agent.Images.NoResize)
	if err != nil {
		return Attachments{}, fmt.Errorf("stdin: %w", err)
	}
	at := Attachments{Images: []llm.Block{llm.ImageBlock(img.Mime, img.Data)}}
	if img.Note != "" {
		at.Notes = append(at.Notes, "stdin: "+img.Note)
	}
	return at, nil
}
