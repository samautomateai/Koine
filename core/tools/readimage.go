package tools

import (
	"context"
	"fmt"

	"koine/core/imgutil"
)

// Image is an image a tool wants to hand to the model next to its text result: base64 data and its media type.
type Image struct{ Mime, Data string }

// ImageSink collects the images a tool run produces. The agent puts one in the context of every tool call (WithImages); Resize is the
// images.autoResize setting.
type ImageSink struct {
	Add    func(Image)
	Resize bool
}

type imageKey struct{}

// WithImages returns a context in which tools can attach images to their result.
func WithImages(ctx context.Context, s ImageSink) context.Context {
	return context.WithValue(ctx, imageKey{}, s)
}

// AttachImage hands an image to the model with the result of the tool call that ctx belongs to. It reports false when nobody is taking images.
func AttachImage(ctx context.Context, img Image) bool {
	s, _ := ctx.Value(imageKey{}).(ImageSink)
	if s.Add == nil {
		return false
	}
	s.Add(img)
	return true
}

// ResizeImages is the images.autoResize setting as the tool call that ctx belongs to sees it.
func ResizeImages(ctx context.Context) bool {
	s, _ := ctx.Value(imageKey{}).(ImageSink)
	return s.Resize
}

// readImage handles the read tool for an image file. ok is false when path is not an image (the caller reads it as text). Otherwise the
// image is attached to the result through the context's ImageSink and the text says so, with a note when it was resized or could not be.
func readImage(ctx context.Context, path string) (text string, ok bool, err error) {
	img, ok, err := imgutil.ReadFile(path, ResizeImages(ctx))
	if !ok || err != nil {
		return "", ok, err
	}
	if !AttachImage(ctx, Image{Mime: img.Mime, Data: img.Data}) {
		return fmt.Sprintf("Image file [%s]: this caller cannot take images, so it was not attached.", img.Mime), true, nil
	}
	text = fmt.Sprintf("Read image file [%s]", img.Mime)
	if img.Note != "" {
		text += "\n[" + img.Note + "]"
	}
	return text, true, nil
}
