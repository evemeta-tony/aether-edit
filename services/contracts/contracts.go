// services/contracts/contracts.go

// Package contracts carries the frozen cross work order contract types
// for the file transcoder track (Janus V-4). The canonical prose lives
// in docs/contracts/ft-contracts-v0.md. Do not change shapes here
// without a contract revision.
package contracts

import (
	"context"
	"time"
)

// NATS JetStream subjects for the frozen v1 events.
const (
	// SubjectUploadLanded carries LandedObjectEvent payloads.
	// Producer: upload service (FT2). Consumer: job service (FT3).
	SubjectUploadLanded = "aether.ft.upload.landed.v1"

	// SubjectMetering carries MeteringEvent payloads.
	// Producers: FT2 and FT3. Consumer: FT6.
	SubjectMetering = "aether.ft.metering.v1"
)

// LandedObjectEvent is contract 1: emitted once an uploaded object has
// been assembled, hashed, and written to object storage.
type LandedObjectEvent struct {
	// UploadID is a uuidv7 string.
	UploadID    string `json:"uploadId"`
	WorkspaceID string `json:"workspaceId"`
	UserID      string `json:"userId"`
	// ObjectKey is "assets/<workspaceId>/sha256/<hex64>".
	ObjectKey string `json:"objectKey"`
	// SHA256 is the lowercase hex64 digest of the whole object.
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
	Mime      string `json:"mime"`
	// LandedAt serializes as RFC3339.
	LandedAt time.Time `json:"landedAt"`
}

// MeteringKind enumerates the allowed metering event kinds.
type MeteringKind string

const (
	MeteringUploadSessionCreated MeteringKind = "upload_session_created"
	MeteringUploadCompleted      MeteringKind = "upload_completed"
	MeteringJobQueued            MeteringKind = "job_queued"
	MeteringJobStarted           MeteringKind = "job_started"
	MeteringJobCompleted         MeteringKind = "job_completed"
	MeteringJobFailed            MeteringKind = "job_failed"
)

// MeteringEvent is contract 2: usage metering emitted by FT2 and FT3,
// consumed later by FT6. Bytes, EncodeSeconds, and JobID are optional
// and omitted when not applicable.
type MeteringEvent struct {
	// EventID is a uuidv7 string.
	EventID       string       `json:"eventId"`
	WorkspaceID   string       `json:"workspaceId"`
	UserID        string       `json:"userId"`
	Kind          MeteringKind `json:"kind"`
	Bytes         *int64       `json:"bytes,omitempty"`
	EncodeSeconds *float64     `json:"encodeSeconds,omitempty"`
	JobID         string       `json:"jobId,omitempty"`
	// At serializes as RFC3339.
	At time.Time `json:"at"`
}

// QuotaDecision is contract 3 (Janus V-5): the result of a quota check.
type QuotaDecision struct {
	Allowed bool
	Reason  string
}

// QuotaChecker is contract 3 (Janus V-5): the quota hook implemented by
// ConfigQuota now and by the metered FT6 implementation later.
type QuotaChecker interface {
	CheckUploadSession(ctx context.Context, workspaceID string, sizeHintBytes int64) (QuotaDecision, error)
	CheckJobAdmission(ctx context.Context, workspaceID string) (QuotaDecision, error)
}

// HardwareSample is contract 4: one event on GET /v1/streams/hardware
// (SSE, 1 Hz) from the telemetry service (FT4).
type HardwareSample struct {
	// Fields are nullable pointers with omitempty so an absent GPU
	// serializes as null (not a fabricated zero), which the console
	// renders as an em-dash per the honest-absence requirement (R12).
	GPUUtilPct      *float64 `json:"gpuUtilPct,omitempty"`
	VRAMUsedMB      *float64 `json:"vramUsedMB,omitempty"`
	VRAMTotalMB     *float64 `json:"vramTotalMB,omitempty"`
	JunctionC       *float64 `json:"junctionC,omitempty"`
	PowerW          *float64 `json:"powerW,omitempty"`
	EncoderSessions *int64   `json:"encoderSessions,omitempty"`
	CPUUtilPct      *float64 `json:"cpuUtilPct,omitempty"`
}

// JobStreamEvent is contract 4: one event on GET /v1/streams/jobs
// (job state transitions plus progress) from the telemetry service.
type JobStreamEvent struct {
	JobID       string  `json:"jobId"`
	State       string  `json:"state"`
	FPS         float64 `json:"fps"`
	SpeedX      float64 `json:"speedX"`
	ETASeconds  float64 `json:"etaSeconds"`
	ProgressPct float64 `json:"progressPct"`
}

// LogStreamEvent is contract 4: one event on GET /v1/streams/logs from
// the telemetry service.
type LogStreamEvent struct {
	Line  string    `json:"line"`
	Tag   string    `json:"tag"`
	Level string    `json:"level"`
	At    time.Time `json:"at"`
}
