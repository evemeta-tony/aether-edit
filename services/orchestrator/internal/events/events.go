// services/orchestrator/internal/events/events.go
//
// Wire payloads and subjects for the frozen cross-WO contracts v0 (Janus
// V-4). The JSON shapes and subjects below are the contract; the Go struct
// names are local to this service. Validation is strict at the boundary
// (S1): unknown or malformed events are rejected, never coerced.
package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Contract subjects.
const (
	// SubjectUploadLanded is contract 1 (producer FT-2, consumer FT-3).
	SubjectUploadLanded = "aether.ft.upload.landed.v1"
	// SubjectMetering is contract 2 (producers FT-2 and FT-3, consumer FT-6).
	SubjectMetering = "aether.ft.metering.v1"
	// SubjectJobProgress carries job state transitions and live progress for
	// FT-4. The subject name is an FT-3 choice (not in the frozen set); the
	// field names match the frozen contract 4 SSE shape for /v1/streams/jobs.
	SubjectJobProgress = "aether.ft.jobs.progress.v1"
)

// ObjectKeyPattern is the frozen landed-object key shape.
var ObjectKeyPattern = regexp.MustCompile(`^assets/([A-Za-z0-9_-]{1,64})/sha256/([0-9a-f]{64})$`)

// UploadLanded is the contract 1 payload.
type UploadLanded struct {
	UploadID    string    `json:"uploadId"`
	WorkspaceID string    `json:"workspaceId"`
	UserID      string    `json:"userId"`
	ObjectKey   string    `json:"objectKey"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"sizeBytes"`
	Mime        string    `json:"mime"`
	LandedAt    time.Time `json:"landedAt"`
}

// ParseUploadLanded strictly decodes and validates a landed-object event.
func ParseUploadLanded(data []byte) (UploadLanded, error) {
	var e UploadLanded
	if err := strictUnmarshal(data, &e); err != nil {
		return e, fmt.Errorf("landed event decode: %w", err)
	}
	if _, err := uuid.Parse(e.UploadID); err != nil {
		return e, fmt.Errorf("landed event: uploadId is not a uuid")
	}
	if e.WorkspaceID == "" || len(e.WorkspaceID) > 64 {
		return e, fmt.Errorf("landed event: workspaceId must be 1..64 characters")
	}
	if e.UserID == "" || len(e.UserID) > 128 {
		return e, fmt.Errorf("landed event: userId must be 1..128 characters")
	}
	m := ObjectKeyPattern.FindStringSubmatch(e.ObjectKey)
	if m == nil {
		return e, fmt.Errorf("landed event: objectKey does not match assets/<workspaceId>/sha256/<hex64>")
	}
	if m[1] != e.WorkspaceID {
		return e, fmt.Errorf("landed event: objectKey workspace does not match workspaceId")
	}
	if m[2] != e.SHA256 {
		return e, fmt.Errorf("landed event: objectKey sha256 does not match sha256 field")
	}
	if e.SizeBytes <= 0 {
		return e, fmt.Errorf("landed event: sizeBytes must be positive")
	}
	if e.Mime == "" || len(e.Mime) > 255 {
		return e, fmt.Errorf("landed event: mime must be 1..255 characters")
	}
	if e.LandedAt.IsZero() {
		return e, fmt.Errorf("landed event: landedAt is required")
	}
	return e, nil
}

// MeteringKind enumerates the contract 2 kinds FT-3 emits. FT-2's upload
// kinds (upload_session_created, upload_completed) are deliberately not
// defined here: this service must never emit them, and duplicating another
// lane's kinds locally invites contract drift. The full kind set lives with
// the frozen contract (Argus PR#4 finding 19).
type MeteringKind string

const (
	MeterJobQueued    MeteringKind = "job_queued"
	MeterJobStarted   MeteringKind = "job_started"
	MeterJobCompleted MeteringKind = "job_completed"
	MeterJobFailed    MeteringKind = "job_failed"
)

// Metering is the contract 2 payload.
type Metering struct {
	EventID       string       `json:"eventId"`
	WorkspaceID   string       `json:"workspaceId"`
	UserID        string       `json:"userId"`
	Kind          MeteringKind `json:"kind"`
	Bytes         *int64       `json:"bytes,omitempty"`
	EncodeSeconds *float64     `json:"encodeSeconds,omitempty"`
	JobID         string       `json:"jobId,omitempty"`
	At            time.Time    `json:"at"`
}

// JobProgress is the payload published on SubjectJobProgress. Field names
// follow frozen contract 4 (/v1/streams/jobs event shape).
type JobProgress struct {
	JobID       string  `json:"jobId"`
	State       string  `json:"state"`
	FPS         float64 `json:"fps"`
	SpeedX      float64 `json:"speedX"`
	ETASeconds  float64 `json:"etaSeconds"`
	ProgressPct float64 `json:"progressPct"`
}

// strictUnmarshal decodes JSON rejecting unknown fields and trailing data.
func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after JSON value")
	}
	return nil
}
