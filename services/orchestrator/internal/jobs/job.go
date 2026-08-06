// services/orchestrator/internal/jobs/job.go
//
// Job domain model for the transcode job service: states, error taxonomy,
// and the state machine. States are frozen by the FT contracts: exactly
// queued, running, completed, failed, with retry returning failed to queued.
package jobs

import (
	"errors"
	"fmt"
	"time"
)

// State is a job lifecycle state.
type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
)

// ValidState reports whether s is one of the four frozen states.
func ValidState(s State) bool {
	switch s {
	case StateQueued, StateRunning, StateCompleted, StateFailed:
		return true
	}
	return false
}

// ErrorClass is the frozen error taxonomy.
type ErrorClass string

const (
	ErrorValidation ErrorClass = "validation"
	ErrorAsset      ErrorClass = "asset"
	ErrorDecode     ErrorClass = "decode"
	ErrorEncode     ErrorClass = "encode"
	ErrorInternal   ErrorClass = "internal"
)

// ValidErrorClass reports whether c is a member of the taxonomy.
func ValidErrorClass(c ErrorClass) bool {
	switch c {
	case ErrorValidation, ErrorAsset, ErrorDecode, ErrorEncode, ErrorInternal:
		return true
	}
	return false
}

// ErrInvalidTransition is returned when a state change is not allowed.
var ErrInvalidTransition = errors.New("invalid job state transition")

// CanTransition reports whether from -> to is a legal transition.
// Legal transitions:
//
//	queued  -> running            (scheduler claims a slot)
//	queued  -> failed             (cancel while queued, or admission revoked)
//	running -> completed          (all outputs written)
//	running -> failed             (engine error or cancel)
//	failed  -> queued             (explicit retry)
func CanTransition(from, to State) bool {
	switch from {
	case StateQueued:
		return to == StateRunning || to == StateFailed
	case StateRunning:
		return to == StateCompleted || to == StateFailed
	case StateFailed:
		return to == StateQueued
	case StateCompleted:
		return false
	}
	return false
}

// Transition validates from -> to and returns ErrInvalidTransition with
// context when the move is illegal.
func Transition(from, to State) error {
	if !ValidState(from) || !ValidState(to) {
		return fmt.Errorf("%w: %s -> %s (unknown state)", ErrInvalidTransition, from, to)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}

// CanRetry reports whether a job in state s may be retried.
// Retry is legal only from failed and returns the job to queued.
func CanRetry(s State) bool { return s == StateFailed }

// CanCancel reports whether a job in state s may be canceled.
// The state set is frozen to four states, so cancel resolves to failed with
// error class internal and message "canceled by user" (documented in the
// service README).
func CanCancel(s State) bool { return s == StateQueued || s == StateRunning }

// OutputProgress is per-output (ladder rung) progress persisted with the job.
type OutputProgress struct {
	Name        string  `json:"name"`
	ObjectKey   string  `json:"objectKey,omitempty"`
	State       string  `json:"state"` // pending | running | completed | failed
	ProgressPct float64 `json:"progressPct"`
}

// Job is a transcode job row.
type Job struct {
	ID              string           `json:"id"`
	WorkspaceID     string           `json:"workspaceId"`
	UserID          string           `json:"userId"`
	PresetID        string           `json:"presetId"`
	SourceObjectKey string           `json:"sourceObjectKey"`
	SourceSHA256    string           `json:"sourceSha256"`
	State           State            `json:"state"`
	ErrorClass      ErrorClass       `json:"errorClass,omitempty"`
	ErrorMessage    string           `json:"errorMessage,omitempty"`
	Attempts        int              `json:"attempts"`
	ProgressPct     float64          `json:"progressPct"`
	FPS             float64          `json:"fps"`
	SpeedX          float64          `json:"speedX"`
	ETASeconds      float64          `json:"etaSeconds"`
	Outputs         []OutputProgress `json:"outputs"`
	CreatedAt       time.Time        `json:"createdAt"`
	QueuedAt        time.Time        `json:"queuedAt"`
	StartedAt       *time.Time       `json:"startedAt,omitempty"`
	FinishedAt      *time.Time       `json:"finishedAt,omitempty"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}
