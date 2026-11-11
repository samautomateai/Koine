package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The events Pi's JSON mode names reach HTTP clients too, as ordinary events with their fields under "data".
func TestPromptStreamCarriesSessionAndThinkingEvents(t *testing.T) {
	_, ts := newServer(t)
	post := func(text string) string {
		r, err := http.Post(ts.URL+"/api/prompt", "application/json", strings.NewReader(`{"text":"`+text+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	if body := post("/name served"); !strings.Contains(body, `data: {"type":"session_info_changed","data":{"name":"served"}}`) ||
		!strings.Contains(body, `"type":"entry_appended"`) {
		t.Errorf("/name: %s", body)
	}
	if body := post("/thinking high"); !strings.Contains(body, `data: {"type":"thinking_level_changed","data":{"level":"high"}}`) {
		t.Errorf("/thinking: %s", body)
	}
}
