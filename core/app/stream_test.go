package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"koine/core/agent"
)

// sseAnthropic is a fake Messages API that streams one text answer. Wire formats are tested against fake servers only.
func sseAnthropic(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":4}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"str"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"eamed"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAppStreamsThroughTheRouter(t *testing.T) {
	srv := sseAnthropic(t)
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	a, _ := settingsApp(t, `{"defaultModel":"claude-sonnet-5-5"}`, "", Options{})
	var evs []agent.Event
	if _, err := a.HandleInput(context.Background(), "hi", func(e agent.Event) { evs = append(evs, e) }); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		if e.Type == "text_delta" || e.Type == "text" {
			got = append(got, e.Type+":"+e.Text)
		}
	}
	if strings.Join(got, ",") != "text_delta:str,text_delta:eamed,text:streamed" {
		t.Errorf("events: %v", evs)
	}
}

func TestQueueModeSettingsReachTheAgent(t *testing.T) {
	cases := []struct{ json, steer, follow string }{
		{`{}`, "", ""},
		{`{"steeringMode":"all","followUpMode":"one-at-a-time"}`, agent.ModeAll, agent.ModeOneAtATime},
		{`{"steeringMode":"sometimes","followUpMode":3}`, "", ""},
	}
	for _, tc := range cases {
		a, _ := settingsApp(t, tc.json, "", Options{})
		if a.Agent.SteeringMode != tc.steer || a.Agent.FollowUpMode != tc.follow {
			t.Errorf("%s: steering %q follow-up %q", tc.json, a.Agent.SteeringMode, a.Agent.FollowUpMode)
		}
	}
	// the project file wins over the user file
	b, _ := settingsApp(t, `{"steeringMode":"all","followUpMode":"all"}`, `{"followUpMode":"one-at-a-time"}`, Options{})
	if b.Agent.SteeringMode != agent.ModeAll || b.Agent.FollowUpMode != agent.ModeOneAtATime {
		t.Errorf("merged: %q %q", b.Agent.SteeringMode, b.Agent.FollowUpMode)
	}
}
