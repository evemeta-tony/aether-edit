// services/upload/publisher.go

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// Publisher delivers contract events. The production implementation is
// JetStreamPublisher (at least once: publishes wait for the stream
// ack and the caller retries on failure).
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

// ftStreamName is the JetStream stream capturing the file transcoder
// subjects. Creation is idempotent; FT3 may ensure the same stream.
const ftStreamName = "AETHER_FT"

// JetStreamPublisher publishes to NATS JetStream and waits for acks.
type JetStreamPublisher struct {
	nc *nats.Conn
	js jetstream.JetStream
}

var _ Publisher = (*JetStreamPublisher)(nil)

// NewJetStreamPublisher connects to NATS and ensures the AETHER_FT
// stream exists with the two frozen v1 subjects.
func NewJetStreamPublisher(ctx context.Context, natsURL string) (*JetStreamPublisher, error) {
	nc, err := nats.Connect(natsURL,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     ftStreamName,
		Subjects: []string{contracts.SubjectUploadLanded, contracts.SubjectMetering},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure stream %s: %w", ftStreamName, err)
	}
	return &JetStreamPublisher{nc: nc, js: js}, nil
}

// Publish sends data to subject and waits for the JetStream ack.
func (p *JetStreamPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	if _, err := p.js.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

// Close drains the connection.
func (p *JetStreamPublisher) Close() {
	_ = p.nc.Drain()
}
