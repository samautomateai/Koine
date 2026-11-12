// Package server exposes the app over HTTP + Server-Sent Events, for Electron,
// browser or remote frontends. (The Wails desktop app drives `koine --mode rpc` instead.)
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"koine/core/agent"
	"koine/core/app"
)

type Server struct {
	App *app.App
	mu  sync.Mutex // one run at a time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(s.App.Describe())
	})
	mux.HandleFunc("POST /api/prompt", s.prompt)
	mux.HandleFunc("POST /api/steer", s.queue(s.App.Agent.Steer))
	mux.HandleFunc("POST /api/follow-up", s.queue(s.App.Agent.FollowUp))
	return mux
}

// queue answers a request to queue a message for the running agent (steer or follow up). The queues come back as JSON; the message is
// delivered by the run that is in progress, or by the next one, and the run's event stream reports it as queue_update and user_message.
func (s *Server) queue(add func(string) (steering, followUp []string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
			http.Error(w, "body must be {\"text\": \"...\"}", http.StatusBadRequest)
			return
		}
		steering, followUp := add(req.Text)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"steering": steering, "followUp": followUp})
	}
}

func (s *Server) prompt(w http.ResponseWriter, r *http.Request) {
	var req struct{ Text string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "body must be {\"text\": \"...\"}", http.StatusBadRequest)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if !s.mu.TryLock() {
		http.Error(w, "a run is already in progress", http.StatusConflict)
		return
	}
	defer s.mu.Unlock()
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	emit := func(e agent.Event) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	s.App.NoUserBash = true // text from the network is a prompt, never a shell command
	// The request context cancels the run if the client disconnects.
	if _, err := s.App.HandleInput(r.Context(), req.Text, emit); err != nil {
		emit(agent.Event{Type: "error", Text: err.Error()})
	}
	emit(agent.Event{Type: "done"})
}
