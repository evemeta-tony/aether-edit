// services/orchestrator/internal/consumer/landed.go
//
// JetStream consumer for the landed-object event (frozen contract 1,
// subject aether.ft.upload.landed.v1). On receipt the service auto-probes
// the object with ffprobe and persists the media info with the source, so a
// later job create does not need a synchronous probe. Malformed events are
// terminated (never redelivered); transient failures are NAKed with delay.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/natsio"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// SourceStore persists probed sources.
type SourceStore interface {
	UpsertSource(ctx context.Context, s store.Source) error
}

// ObjectResolver maps object keys to local paths.
type ObjectResolver interface {
	Path(key string) (string, error)
	Exists(key string) (bool, error)
}

// Landed consumes landed-object events and probes sources.
type Landed struct {
	sources SourceStore
	objects ObjectResolver
	eng     engine.TranscodeEngine
	log     *slog.Logger
}

// New builds the consumer worker.
func New(sources SourceStore, objects ObjectResolver, eng engine.TranscodeEngine, log *slog.Logger) *Landed {
	if log == nil {
		log = slog.Default()
	}
	return &Landed{sources: sources, objects: objects, eng: eng, log: log}
}

// Start binds a durable consumer on the stream carrying the landed subject
// and begins consuming. The returned stop function drains the consumer.
func (l *Landed) Start(ctx context.Context, conn *natsio.Conn, streamName string) (func(), error) {
	cons, err := conn.JS.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Durable:       "orchestrator-landed",
		FilterSubject: events.SubjectUploadLanded,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    10,
		AckWait:       2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("create landed consumer: %w", err)
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		l.handle(ctx, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("consume landed: %w", err)
	}
	return cc.Stop, nil
}

// handle processes one landed-object message.
func (l *Landed) handle(ctx context.Context, msg jetstream.Msg) {
	ev, err := events.ParseUploadLanded(msg.Data())
	if err != nil {
		// Contract violation: reject permanently, never coerce (S1).
		l.log.Error("invalid landed event, terminating", "err", err)
		_ = msg.Term()
		return
	}
	path, err := l.objects.Path(ev.ObjectKey)
	if err != nil {
		l.log.Error("landed event object key rejected", "key", ev.ObjectKey, "err", err)
		_ = msg.Term()
		return
	}
	ok, err := l.objects.Exists(ev.ObjectKey)
	if err != nil {
		l.log.Warn("object store check failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}
	if !ok {
		// The object may still be settling on shared storage; retry.
		l.log.Warn("landed object not yet visible, will retry", "key", ev.ObjectKey)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	mi, err := l.eng.Probe(probeCtx, path)
	if err != nil {
		// A structurally unreadable asset will not become readable on
		// redelivery, but transient conditions might; distinguish by stage.
		var engErr *engine.Error
		if errors.As(err, &engErr) && engErr.Stage == engine.StageAsset {
			l.log.Error("landed object failed probe, terminating", "key", ev.ObjectKey, "err", err)
			_ = msg.Term()
			return
		}
		l.log.Warn("probe failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(30 * time.Second)
		return
	}
	err = l.sources.UpsertSource(ctx, store.Source{
		ObjectKey:   ev.ObjectKey,
		WorkspaceID: ev.WorkspaceID,
		SHA256:      ev.SHA256,
		SizeBytes:   ev.SizeBytes,
		Mime:        ev.Mime,
		MediaInfo:   mi,
	})
	if err != nil {
		l.log.Warn("persist source failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}
	l.log.Info("source probed", "key", ev.ObjectKey, "durationSeconds", mi.DurationSeconds)
	_ = msg.Ack()
}
