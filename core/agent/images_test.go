package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"koine/core/llm"
	"koine/core/tools"
)

func TestPromptImagesJoinTheUserMessageAndReachTheProvider(t *testing.T) {
	s := &script{}
	a := newAgent(s)
	var persisted []llm.Message
	a.Persist = func(m llm.Message) { persisted = append(persisted, m) }
	img := llm.ImageBlock("image/png", "QUJD")
	if err := a.RunWithImages(context.Background(), "what is this?", []llm.Block{img}, nil); err != nil {
		t.Fatal(err)
	}
	got := s.reqs[0].Messages[0].Content
	if len(got) != 2 || got[0].Text != "what is this?" || got[1].Type != "image" || got[1].Base64() != "QUJD" {
		t.Fatalf("user message: %+v", got)
	}
	if len(persisted[0].Content) != 2 {
		t.Errorf("persisted: %+v", persisted[0])
	}
	// the images belong to that one run only
	if _, err := collect(a, "and now?"); err != nil || llm.CountImages([]llm.Message{a.History[len(a.History)-2]}) != 0 {
		t.Errorf("images must not leak into the next prompt: %v", err)
	}
	// an image alone is a prompt
	b := newAgent(&script{})
	if err := b.RunWithImages(context.Background(), "", []llm.Block{img}, nil); err != nil || len(b.History[0].Content) != 1 {
		t.Errorf("%v %+v", err, b.History)
	}
}

func TestToolImagesRideInTheToolResultAndFollowTheResizeSetting(t *testing.T) {
	var resize bool
	reg := func(a *Agent) {
		a.Tools.Register(&tools.Func{N: "shot", D: "d", S: json.RawMessage(`{"type":"object"}`),
			F: func(ctx context.Context, _ json.RawMessage) (string, error) {
				resize = tools.ResizeImages(ctx)
				if !tools.AttachImage(ctx, tools.Image{Mime: "image/png", Data: "QUJD"}) {
					return "no sink", nil
				}
				return "shot taken", nil
			}})
	}
	s := &script{turns: [][]llm.Block{{use("1", "shot", `{}`)}}}
	a := newAgent(s)
	reg(a)
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	res := lastResults(s)[0]
	if res.Type != "tool_result" || res.Content != "shot taken" || len(res.Images) != 1 || res.Images[0].MimeType() != "image/png" || !resize {
		t.Fatalf("result %+v resize=%v", res, resize)
	}
	a2 := newAgent(&script{turns: [][]llm.Block{{use("1", "shot", `{}`)}}})
	a2.Images = ImageOptions{NoResize: true}
	reg(a2)
	if _, err := collect(a2, "go"); err != nil || resize {
		t.Errorf("images.autoResize=false must reach the tool: resize=%v err=%v", resize, err)
	}
	if tools.AttachImage(context.Background(), tools.Image{}) || tools.ResizeImages(context.Background()) {
		t.Error("outside the agent there is no sink")
	}
}

func TestImagesAreLeftOutForModelsThatCannotReadThem(t *testing.T) {
	s := &script{}
	a := newAgent(s)
	a.Model = "texty"
	a.Catalog = &llm.Catalog{Models: []llm.Model{{ID: "texty", Input: []string{"text"}}, {ID: "vision", Input: []string{"text", "image"}}}}
	img := llm.ImageBlock("image/png", "QUJD")
	var notices []string
	emit := func(e Event) {
		if e.Type == "notice" {
			notices = append(notices, e.Text)
		}
	}
	if err := a.RunWithImages(context.Background(), "look", []llm.Block{img}, emit); err != nil {
		t.Fatal(err)
	}
	sent := s.reqs[0].Messages[0].Content
	if len(sent) != 2 || sent[1].Type != "text" || !strings.Contains(sent[1].Text, "model texty does not accept images") {
		t.Errorf("sent: %+v", sent)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "1 image(s) not sent") {
		t.Errorf("notices: %v", notices)
	}
	if llm.CountImages(a.History) != 1 {
		t.Error("the history keeps the image")
	}
	// the same reason is not announced twice, and a vision model gets the image back
	if err := a.Run(context.Background(), "again", emit); err != nil || len(notices) != 1 {
		t.Errorf("notices after second run: %v %v", notices, err)
	}
	a.Model = "vision"
	if err := a.Run(context.Background(), "third", emit); err != nil || llm.CountImages(s.reqs[len(s.reqs)-1].Messages) != 1 {
		t.Errorf("vision model should see the image again: %v", err)
	}
}

func TestBlockImagesSettingKeepsEveryImageFromTheProvider(t *testing.T) {
	s := &script{}
	a := newAgent(s)
	a.Images = ImageOptions{Block: true}
	var notice string
	emit := func(e Event) {
		if e.Type == "notice" {
			notice = e.Text
		}
	}
	if err := a.RunWithImages(context.Background(), "look", []llm.Block{llm.ImageBlock("image/png", "QUJD")}, emit); err != nil {
		t.Fatal(err)
	}
	if llm.CountImages(s.reqs[0].Messages) != 0 || !strings.Contains(notice, "images.blockImages") {
		t.Errorf("blocked: %+v notice=%q", s.reqs[0].Messages, notice)
	}
}
