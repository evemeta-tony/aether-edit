// services/upload/store.go

package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Session states.
const (
	SessionActive    = "ACTIVE"
	SessionAssembled = "ASSEMBLED"
	SessionCompleted = "COMPLETED"
	SessionCancelled = "CANCELLED"
)

// Chunk states.
const (
	ChunkPending  = "PENDING"
	ChunkInflight = "INFLIGHT"
	ChunkDone     = "DONE"
	ChunkRetry    = "RETRY"
)

// ErrNotFound is returned by stores for missing sessions.
var ErrNotFound = errors.New("not found")

// Session is a persisted upload session. The chunk map lives in Chunk
// rows keyed by (session id, index) so that a service restart rebuilds
// the full resume state from the store.
type Session struct {
	ID             uuid.UUID
	WorkspaceID    string
	UserID         string
	Filename       string
	SizeBytes      int64
	Mime           string
	ChunkSizeBytes int64
	ChunkCount     int
	State          string
	S3UploadID     string
	StagingKey     string
	SHA256         string
	ObjectKey      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Chunk is one entry of the persisted chunk map.
type Chunk struct {
	Index     int
	State     string
	SHA256    string
	ETag      string
	SizeBytes int64
}

// ClaimResult reports the outcome of a chunk claim.
type ClaimResult int

const (
	// ClaimAcquired means the chunk moved to INFLIGHT and the caller
	// owns the write.
	ClaimAcquired ClaimResult = iota
	// ClaimAlreadyDone means the chunk is DONE; re upload is a no op.
	ClaimAlreadyDone
)

// Store persists sessions and chunk maps. The Postgres implementation
// (PostgresStore) is the production store; tests may substitute an in
// memory double.
type Store interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, id uuid.UUID) (*Session, error)
	GetChunks(ctx context.Context, id uuid.UUID) ([]Chunk, error)

	// ClaimChunk transitions a chunk from PENDING, RETRY, or a stale
	// INFLIGHT to INFLIGHT. A DONE chunk returns ClaimAlreadyDone.
	ClaimChunk(ctx context.Context, id uuid.UUID, index int) (ClaimResult, error)

	// MarkChunkDone records a verified part write.
	MarkChunkDone(ctx context.Context, id uuid.UUID, index int, sha256Hex, etag string, sizeBytes int64) error

	// MarkChunkRetry flags a failed or corrupted write for re claim.
	MarkChunkRetry(ctx context.Context, id uuid.UUID, index int) error

	// SetSessionAssembled records the minted hash and final object key
	// after server side assembly, before event publication.
	SetSessionAssembled(ctx context.Context, id uuid.UUID, sha256Hex, objectKey string) error

	// SetSessionState moves the session to state (COMPLETED, CANCELLED).
	SetSessionState(ctx context.Context, id uuid.UUID, state string) error
}
