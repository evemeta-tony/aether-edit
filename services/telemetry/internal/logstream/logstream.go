// services/telemetry/internal/logstream/logstream.go

// Package logstream consumes the structured log subject published by the
// FT-2 and FT-3 services and feeds the tabbed log stream.
package logstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
)

// SubjectLog is the structured log subject (FT-4 API addendum to contract 4).
const SubjectLog = "aether.ft.log.v1"

// TagPattern constrains log tags (and the ?tag= filter) at the boundary.
var TagPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

var levels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Consumer validates incoming log events and publishes them to the logs hub.
type Consumer struct {
	hub *hub.Hub
}

// New creates a Consumer publishing to h.
func New(h *hub.Hub) *Consumer {
	return &Consumer{hub: h}
}

// Handle consumes one SubjectLog message. Invalid payloads are rejected with
// an error and nothing is published.
func (c *Consumer) Handle(data []byte) error {
	var ev contracts.LogStreamEvent
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ev); err != nil {
		return fmt.Errorf("log event: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("log event: trailing data after JSON value")
	}
	if ev.Line == "" {
		return fmt.Errorf("log event: line is required")
	}
	if len(ev.Line) > 8192 {
		return fmt.Errorf("log event: line exceeds 8192 bytes")
	}
	if !TagPattern.MatchString(ev.Tag) {
		return fmt.Errorf("log event: invalid tag %q", ev.Tag)
	}
	if !levels[ev.Level] {
		return fmt.Errorf("log event: invalid level %q", ev.Level)
	}
	if ev.At.IsZero() {
		return fmt.Errorf("log event: at is required")
	}
	ev.At = ev.At.UTC().Truncate(time.Millisecond)
	out, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("log event: %w", err)
	}
	c.hub.Publish(hub.Event{Name: "log", Data: out, Tag: ev.Tag})
	return nil
}
