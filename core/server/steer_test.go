package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSteerAndFollowUpQueueMessagesForTheAgent(t *testing.T) {
	s, ts := newServer(t)
	post := func(path, body string) (int, string) {
		r, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, strings.TrimSpace(string(b))
	}
	if code, body := post("/api/steer", `{"text":"turn left"}`); code != http.StatusAccepted || body != `{"followUp":null,"steering":["turn left"]}` {
		t.Errorf("steer: %d %s", code, body)
	}
	want := `{"followUp":["then right"],"steering":["turn left"]}`
	if code, body := post("/api/follow-up", `{"text":"then right"}`); code != http.StatusAccepted || body != want {
		t.Errorf("follow-up: %d %s", code, body)
	}
	if st, fu := s.App.Agent.Queued(); len(st) != 1 || len(fu) != 1 {
		t.Errorf("queued: %v %v", st, fu)
	}
	for _, path := range []string{"/api/steer", "/api/follow-up"} {
		if code, _ := post(path, `{"text":""}`); code != http.StatusBadRequest {
			t.Errorf("%s with no text: %d", path, code)
		}
	}
}
