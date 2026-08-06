// services/upload/memstore_test.go

package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// MemStore is an in memory Store double for tests. The production
// store is PostgresStore; this double mirrors its transition rules so
// the state machine tests are meaningful.
type MemStore struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*Session
	chunks   map[uuid.UUID][]Chunk
}

var _ Store = (*MemStore)(nil)

func NewMemStore() *MemStore {
	return &MemStore{
		sessions: make(map[uuid.UUID]*Session),
		chunks:   make(map[uuid.UUID][]Chunk),
	}
}

func (m *MemStore) CreateSession(ctx context.Context, s *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[s.ID]; ok {
		return fmt.Errorf("duplicate session %s", s.ID)
	}
	cp := *s
	m.sessions[s.ID] = &cp
	chunks := make([]Chunk, s.ChunkCount)
	for i := range chunks {
		chunks[i] = Chunk{Index: i, State: ChunkPending}
	}
	m.chunks[s.ID] = chunks
	return nil
}

func (m *MemStore) GetSession(ctx context.Context, id uuid.UUID) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *MemStore) GetChunks(ctx context.Context, id uuid.UUID) ([]Chunk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	chunks, ok := m.chunks[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]Chunk, len(chunks))
	copy(out, chunks)
	return out, nil
}

func (m *MemStore) ClaimChunk(ctx context.Context, id uuid.UUID, index int) (ClaimResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	chunks, ok := m.chunks[id]
	if !ok || index < 0 || index >= len(chunks) {
		return 0, ErrNotFound
	}
	if chunks[index].State == ChunkDone {
		return ClaimAlreadyDone, nil
	}
	chunks[index].State = ChunkInflight
	return ClaimAcquired, nil
}

func (m *MemStore) MarkChunkDone(ctx context.Context, id uuid.UUID, index int, sha256Hex, etag string, sizeBytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	chunks, ok := m.chunks[id]
	if !ok || index < 0 || index >= len(chunks) {
		return ErrNotFound
	}
	chunks[index] = Chunk{Index: index, State: ChunkDone, SHA256: sha256Hex, ETag: etag, SizeBytes: sizeBytes}
	return nil
}

func (m *MemStore) MarkChunkRetry(ctx context.Context, id uuid.UUID, index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	chunks, ok := m.chunks[id]
	if !ok || index < 0 || index >= len(chunks) {
		return ErrNotFound
	}
	if chunks[index].State == ChunkDone {
		return ErrNotFound
	}
	chunks[index].State = ChunkRetry
	return nil
}

func (m *MemStore) SetSessionAssembled(ctx context.Context, id uuid.UUID, sha256Hex, objectKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.State = SessionAssembled
	s.SHA256 = sha256Hex
	s.ObjectKey = objectKey
	return nil
}

func (m *MemStore) SetSessionState(ctx context.Context, id uuid.UUID, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.State = state
	return nil
}
