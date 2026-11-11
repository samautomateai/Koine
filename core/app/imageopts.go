package app

import "koine/core/agent"

// ImageSettings are Pi's images.* settings. They are embedded in Config, so the JSON key is "images".
type ImageSettings struct {
	Images struct {
		AutoResize  opt[bool] `json:"autoResize"`  // default true: images are resized to at most 2000 by 2000 pixels before they are sent
		BlockImages opt[bool] `json:"blockImages"` // default false: true keeps every image from the provider
	} `json:"images"`
}

// mergeImages lets the values set in o win.
func (s *ImageSettings) mergeImages(o ImageSettings) {
	if o.Images.AutoResize.Set {
		s.Images.AutoResize = o.Images.AutoResize
	}
	if o.Images.BlockImages.Set {
		s.Images.BlockImages = o.Images.BlockImages
	}
}

// imageOptions maps the settings onto the agent's options.
func (s ImageSettings) imageOptions() agent.ImageOptions {
	return agent.ImageOptions{NoResize: s.Images.AutoResize.Set && !s.Images.AutoResize.V, Block: s.Images.BlockImages.V}
}
