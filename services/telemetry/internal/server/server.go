// services/telemetry/internal/server/server.go

// Package server wires the SSE endpoints of contract 4 behind the shared
// bearer-auth middleware.
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/auth"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/logstream"
)

// Options carries the dependencies of the HTTP surface.
type Options struct {
	AuthToken string
	Hardware  *hub.Hub
	Jobs      *hub.Hub
	Logs      *hub.Hub
	Heartbeat time.Duration
	// Health reports liveness details for GET /healthz.
	Health func() map[string]string
}

// New builds the telemetry HTTP handler.
func New(o Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]string{"status": "ok"}
		if o.Health != nil {
			for k, v := range o.Health() {
				body[k] = v
			}
		}
		json.NewEncoder(w).Encode(body)
	})

	protect := auth.Middleware(o.AuthToken)
	mux.Handle("GET /v1/streams/hardware", protect(streamHandler(o.Hardware, o.Heartbeat, nil)))
	mux.Handle("GET /v1/streams/jobs", protect(streamHandler(o.Jobs, o.Heartbeat, nil)))
	mux.Handle("GET /v1/streams/logs", protect(logsHandler(o.Logs, o.Heartbeat)))
	return mux
}

func streamHandler(h *hub.Hub, heartbeat time.Duration, filter func(hub.Event) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) != 0 {
			badRequest(w, "unexpected query parameters")
			return
		}
		h.ServeSSE(w, r, filter, heartbeat)
	})
}

func logsHandler(h *hub.Hub, heartbeat time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for k := range q {
			if k != "tag" {
				badRequest(w, "unexpected query parameter "+k)
				return
			}
		}
		var filter func(hub.Event) bool
		if tag := q.Get("tag"); tag != "" {
			if !logstream.TagPattern.MatchString(tag) {
				badRequest(w, "invalid tag filter")
				return
			}
			filter = func(ev hub.Event) bool { return ev.Tag == tag }
		}
		h.ServeSSE(w, r, filter, heartbeat)
	})
}

func badRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
