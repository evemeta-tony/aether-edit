// services/orchestrator/internal/metering/metering.go
//
// Metering event emission per frozen contract 2. FT-3 emits job_queued,
// job_started, job_completed (with encodeSeconds) and job_failed on
// aether.ft.metering.v1. Emission is faithful and non-blocking for the job
// path: a publish failure is logged by the caller but never fails the job.
package metering

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
)

// Publisher is the transport seam (JetStream in production).
type Publisher interface {
	PublishJSON(ctx context.Context, subject string, v any) error
}

// Emitter builds and publishes metering events.
type Emitter struct {
	pub Publisher
}

// New constructs an Emitter.
func New(pub Publisher) *Emitter { return &Emitter{pub: pub} }

// Emit publishes one metering event. bytes and encodeSeconds are optional
// per the contract; pass nil to omit.
func (e *Emitter) Emit(ctx context.Context, workspaceID, userID string, kind events.MeteringKind, jobID string, bytes *int64, encodeSeconds *float64) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("metering: uuidv7: %w", err)
	}
	ev := events.Metering{
		EventID:       id.String(),
		WorkspaceID:   workspaceID,
		UserID:        userID,
		Kind:          kind,
		Bytes:         bytes,
		EncodeSeconds: encodeSeconds,
		JobID:         jobID,
		At:            time.Now().UTC(),
	}
	return e.pub.PublishJSON(ctx, events.SubjectMetering, ev)
}
