// services/orchestrator/internal/natsio/natsio.go
//
// NATS JetStream wiring: idempotent stream setup and typed publishers.
// Streams are created only if absent; existing stream configuration (for
// example one already created by the FT-2 upload service) is left untouched.
package natsio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
)

// Conn bundles the core NATS connection and its JetStream context.
type Conn struct {
	NC *nats.Conn
	JS jetstream.JetStream
}

// Connect dials NATS and initializes JetStream.
func Connect(url string) (*Conn, error) {
	nc, err := nats.Connect(url, nats.Name("aether-orchestrator"))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream init: %w", err)
	}
	return &Conn{NC: nc, JS: js}, nil
}

// Close drains the connection.
func (c *Conn) Close() {
	if c.NC != nil {
		_ = c.NC.Drain()
	}
}

// EnsureStreamForSubject returns the stream carrying subject, creating a
// stream with the given fallback name if none exists. It never modifies an
// existing stream.
func (c *Conn) EnsureStreamForSubject(ctx context.Context, fallbackName, subject string) (string, error) {
	name, err := c.JS.StreamNameBySubject(ctx, subject)
	if err == nil {
		return name, nil
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return "", fmt.Errorf("stream lookup for %s: %w", subject, err)
	}
	_, err = c.JS.CreateStream(ctx, jetstream.StreamConfig{
		Name:      fallbackName,
		Subjects:  []string{subject},
		Retention: jetstream.LimitsPolicy,
		Storage:   jetstream.FileStorage,
	})
	if err != nil {
		if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			return fallbackName, nil
		}
		return "", fmt.Errorf("create stream %s: %w", fallbackName, err)
	}
	return fallbackName, nil
}

// PublishJSON publishes v as JSON on subject through JetStream.
func (c *Conn) PublishJSON(ctx context.Context, subject string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.JS.Publish(ctx, subject, data)
	return err
}

// ProgressPublisher publishes job progress and state transitions for FT-4 on
// core NATS (ephemeral fan-out; the telemetry service subscribes live).
type ProgressPublisher struct {
	nc *nats.Conn
}

// NewProgressPublisher builds a publisher over the core connection.
func NewProgressPublisher(c *Conn) *ProgressPublisher {
	return &ProgressPublisher{nc: c.NC}
}

// Publish sends one JobProgress payload.
func (p *ProgressPublisher) Publish(ev events.JobProgress) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return p.nc.Publish(events.SubjectJobProgress, data)
}
