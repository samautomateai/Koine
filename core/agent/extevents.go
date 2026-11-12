package agent

import (
	"context"
	"encoding/json"
	"strings"

	"koine/core/events"
	"koine/core/llm"
)

// Input sources, as the input event reports them (Pi: "interactive", "rpc" or "extension").
const (
	SourceInteractive = "interactive"
	SourceRPC         = "rpc"
	SourceExtension   = "extension"
)

// piImages are Pi image content block: {type: "image", data: <base64>, mimeType}.
func piImages(blocks []llm.Block) []map[string]any {
	var out []map[string]any
	for _, b := range blocks {
		if b.Type == "image" {
			out = append(out, map[string]any{"type": "image", "data": b.Base64(), "mimeType": b.MimeType()})
		}
	}
	return out
}

// imagesFromPi reads Pi image blocks an extension answered with. Anything that is not a well-formed image is dropped.
func imagesFromPi(raw json.RawMessage) []llm.Block {
	var in []struct{ Type, Data, MimeType string }
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	var out []llm.Block
	for _, i := range in {
		if i.Type == "image" && i.Data != "" && i.MimeType != "" {
			out = append(out, llm.ImageBlock(i.MimeType, i.Data))
		}
	}
	return out
}

// inputPayload is what the input event carries: the text, its source and the images that came with it.
func (a *Agent) inputPayload(prompt string) map[string]any {
	src := a.InputSource
	if src == "" {
		src = SourceInteractive
	}
	p := map[string]any{"text": prompt, "source": src}
	if imgs := piImages(a.pendingImages); len(imgs) > 0 {
		p["images"] = imgs
	}
	return p
}

// inputResolved tells the caller that the input event is settled: handled says that no run will follow (an extension took the prompt).
func (a *Agent) inputResolved(handled bool) {
	if a.OnInput != nil {
		a.OnInput(handled)
	}
}

// promptOptions is what Koine can tell extensions about how the system prompt was built (Pi's systemPromptOptions). Editing it has no
// effect here: an extension that wants a different prompt returns systemPrompt.
func (a *Agent) promptOptions() map[string]any {
	opts := map[string]any{"cwd": a.Cwd, "customPrompt": a.Base}
	var selected []string
	snippets := map[string]string{}
	if a.Tools != nil {
		selected = a.Tools.Names()
		lines, _ := a.Tools.PromptParts()
		for _, l := range lines {
			if name, text, ok := cutLine(l); ok {
				snippets[name] = text
			}
		}
	}
	var skillNames []string
	for _, s := range a.Skills {
		skillNames = append(skillNames, s.Name)
	}
	opts["selectedTools"], opts["toolSnippets"], opts["skills"] = selected, snippets, skillNames
	return opts
}

// cutLine splits a "- name: snippet" line of the tools section.
func cutLine(l string) (name, text string, ok bool) {
	return strings.Cut(strings.TrimPrefix(l, "- "), ": ")
}

// beforeAgentStart sends before_agent_start, once per prompt. An extension may replace the system prompt for this run and add a
// custom message to the conversation. The function it returns ends the replacement.
func (a *Agent) beforeAgentStart(ctx context.Context, prompt string, content []llm.Block) func() {
	if a.depth != 0 {
		return func() {}
	}
	d := a.dispatch(ctx, events.BeforeAgentStart, map[string]any{
		"prompt": prompt, "images": piImages(content), "systemPrompt": a.baseSystem(), "systemPromptOptions": a.promptOptions(),
	})
	if d.SystemPrompt != nil {
		a.sysOverride = d.SystemPrompt
	}
	if d.Message != nil && d.Message.CustomType != "" {
		a.AddCustom(*d.Message)
	}
	return func() { a.sysOverride = nil }
}

// AddCustom adds a message an extension wants the model to see (Pi's custom message) to the conversation. OnCustom stores it; it is not
// handed to Persist, which stores ordinary messages.
func (a *Agent) AddCustom(m events.CustomMessage) {
	a.History = append(a.History, userText(m.Content))
	if a.OnCustom != nil {
		a.OnCustom(m)
	}
}
