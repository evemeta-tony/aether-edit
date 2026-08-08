// services/telemetry/internal/server/server.go

// Package server wires the SSE endpoints of contract 4 behind the shared
// HS256 JWT bearer-auth middleware.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/auth"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/logstream"
)

// Options carries the dependencies of the HTTP surface.
type Options struct {
	// AuthHS256Key is the base64url-decoded HMAC key used to verify the
	// HS256 JWT bearer tokens on the stream endpoints.
	AuthHS256Key []byte
	Hardware     *hub.Hub
	Jobs         *hub.Hub
	Logs         *hub.Hub
	Heartbeat    time.Duration
	// Health reports liveness details for GET /healthz.
	Health func() map[string]string
	// Logger receives debug-level auth-failure diagnostics. Optional; when
	// nil the auth middleware falls back to slog.Default().
	Logger *slog.Logger
}

// New builds the telemetry HTTP handler. It returns an error if the auth key
// is not a valid HS256 signing key.
func New(o Options) (http.Handler, error) {
	verifier, err := auth.NewVerifier(o.AuthHS256Key)
	if err != nil {
		return nil, err
	}
	if o.Logger != nil {
		verifier = verifier.WithLogger(o.Logger)
	}

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

	protect := verifier.Middleware
	mux.Handle("GET /v1/streams/hardware", protect(streamHandler(o.Hardware, o.Heartbeat, nil)))
	mux.Handle("GET /v1/streams/jobs", protect(streamHandler(o.Jobs, o.Heartbeat, nil)))
	mux.Handle("GET /v1/streams/logs", protect(logsHandler(o.Logs, o.Heartbeat)))
	return mux, nil
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
