package agent

import (
	"context"
	"fmt"

	"koine/core/llm"
	"koine/core/tools"
)

// ImageOptions are the images.* settings. The zero value is Pi's defaults: images are resized to fit and sent.
type ImageOptions struct {
	NoResize bool // images.autoResize is false
	Block    bool // images.blockImages is true: no image reaches a provider
}

// imageSink is what the tools get to attach images with. The images land in the tool_result block of the call.
func (a *Agent) imageSink(into *[]llm.Block) tools.ImageSink {
	return tools.ImageSink{Resize: !a.Images.NoResize, Add: func(i tools.Image) { *into = append(*into, llm.ImageBlock(i.Mime, i.Data)) }}
}

// forModel is the history as the provider should see it: images the model cannot read, or the settings forbid, become a text note in
// their place, and the user is told once per reason. The history itself keeps them, so a later switch to a model that reads images
// sends them again.
func (a *Agent) forModel(emit func(Event)) []llm.Message {
	why := ""
	switch {
	case a.Images.Block:
		why = "images.blockImages is on"
	case !a.Catalog.AcceptsImages(a.Model):
		why = llm.NoImagesWhy(a.Model)
	}
	if why == "" {
		return a.History
	}
	msgs, n := llm.StripImages(a.History, why)
	if n > 0 && a.imageNotice != why {
		a.imageNotice = why
		emit(Event{Type: "notice", Text: fmt.Sprintf("%d image(s) not sent: %s", n, why)})
	}
	return msgs
}

// RunWithImages is Run for a prompt that comes with images (base64 image blocks, as llm.ImageBlock makes them).
func (a *Agent) RunWithImages(ctx context.Context, prompt string, images []llm.Block, emit func(Event)) error {
	a.pendingImages = images
	return a.Run(ctx, prompt, emit)
}
