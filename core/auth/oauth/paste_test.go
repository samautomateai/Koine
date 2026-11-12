package oauth

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func TestABrokenPasteDoesNotEndTheWaitForTheBrowser(t *testing.T) {
	c := newClock()
	ts := newTokenServer(t, 200, tokenJSON)
	a := anthropicFor(ts, c)
	ui := quickUI(t, nil)
	ui.Race = true
	ui.Prompt = func(string, bool) (string, error) { return "", context.Canceled } // stdin at EOF
	ui.OpenBrowser = func(target string) error {
		u, _ := url.Parse(target)
		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = getQuiet(u.Query().Get("redirect_uri") + "?code=late&state=" + u.Query().Get("state"))
		}()
		return nil
	}
	if _, err := a.Login(context.Background(), ui, Options{}); err != nil {
		t.Fatal(err)
	}
	if ts.last(t)["code"] != "late" {
		t.Error("the browser's answer was not used")
	}
}
