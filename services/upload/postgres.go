// services/upload/postgres.go

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the production Store backed by a shared Postgres
// cluster via pgx. Schema lives in services/upload/migrations and is
// applied with golang-migrate before the service starts.
type PostgresStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PostgresStore)(nil)

// NewPostgresStore connects a pool to databaseURL and pings it.
func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// Close releases the pool.
func (p *PostgresStore) Close() { p.pool.Close() }

// CreateSession inserts the session row and its full PENDING chunk map
// in one transaction.
func (p *PostgresStore) CreateSession(ctx context.Context, s *Session) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO upload_sessions
			(id, workspace_id, user_id, filename, size_bytes, mime,
			 chunk_size_bytes, chunk_count, state, s3_upload_id, staging_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		s.ID, s.WorkspaceID, s.UserID, s.Filename, s.SizeBytes, s.Mime,
		s.ChunkSizeBytes, s.ChunkCount, s.State, s.S3UploadID, s.StagingKey)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	rows := make([][]any, 0, s.ChunkCount)
	for i := 0; i < s.ChunkCount; i++ {
		rows = append(rows, []any{s.ID, i, ChunkPending})
	}
	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"upload_chunks"},
		[]string{"session_id", "chunk_index", "state"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("insert chunks: %w", err)
	}
	return tx.Commit(ctx)
}

// GetSession loads one session by id.
func (p *PostgresStore) GetSession(ctx context.Context, id uuid.UUID) (*Session, error) {
	s := &Session{}
	err := p.pool.QueryRow(ctx, `
		SELECT id, workspace_id, user_id, filename, size_bytes, mime,
		       chunk_size_bytes, chunk_count, state, s3_upload_id,
		       staging_key, sha256, object_key, created_at, updated_at
		FROM upload_sessions WHERE id = $1`, id).Scan(
		&s.ID, &s.WorkspaceID, &s.UserID, &s.Filename, &s.SizeBytes, &s.Mime,
		&s.ChunkSizeBytes, &s.ChunkCount, &s.State, &s.S3UploadID,
		&s.StagingKey, &s.SHA256, &s.ObjectKey, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return s, nil
}

// GetChunks returns the chunk map ordered by index.
func (p *PostgresStore) GetChunks(ctx context.Context, id uuid.UUID) ([]Chunk, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT chunk_index, state, sha256, etag, size_bytes
		FROM upload_chunks WHERE session_id = $1 ORDER BY chunk_index`, id)
	if err != nil {
		return nil, fmt.Errorf("get chunks: %w", err)
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.Index, &c.State, &c.SHA256, &c.ETag, &c.SizeBytes); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClaimChunk moves a non DONE chunk to INFLIGHT atomically.
func (p *PostgresStore) ClaimChunk(ctx context.Context, id uuid.UUID, index int) (ClaimResult, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE upload_chunks SET state = $3, updated_at = now()
		WHERE session_id = $1 AND chunk_index = $2 AND state <> $4`,
		id, index, ChunkInflight, ChunkDone)
	if err != nil {
		return 0, fmt.Errorf("claim chunk: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return ClaimAcquired, nil
	}
	// Nothing updated: either the chunk is DONE or the row is missing.
	var state string
	err = p.pool.QueryRow(ctx, `
		SELECT state FROM upload_chunks WHERE session_id = $1 AND chunk_index = $2`,
		id, index).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("claim chunk state: %w", err)
	}
	if state == ChunkDone {
		return ClaimAlreadyDone, nil
	}
	return 0, fmt.Errorf("claim chunk: unexpected state %q", state)
}

// MarkChunkDone records the verified part write.
func (p *PostgresStore) MarkChunkDone(ctx context.Context, id uuid.UUID, index int, sha256Hex, etag string, sizeBytes int64) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE upload_chunks
		SET state = $3, sha256 = $4, etag = $5, size_bytes = $6, updated_at = now()
		WHERE session_id = $1 AND chunk_index = $2`,
		id, index, ChunkDone, sha256Hex, etag, sizeBytes)
	if err != nil {
		return fmt.Errorf("mark chunk done: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// MarkChunkRetry flags the chunk for re claim.
func (p *PostgresStore) MarkChunkRetry(ctx context.Context, id uuid.UUID, index int) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE upload_chunks SET state = $3, updated_at = now()
		WHERE session_id = $1 AND chunk_index = $2 AND state <> $4`,
		id, index, ChunkRetry, ChunkDone)
	if err != nil {
		return fmt.Errorf("mark chunk retry: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// SetSessionAssembled records the minted hash and object key.
func (p *PostgresStore) SetSessionAssembled(ctx context.Context, id uuid.UUID, sha256Hex, objectKey string) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE upload_sessions
		SET state = $2, sha256 = $3, object_key = $4, updated_at = now()
		WHERE id = $1`,
		id, SessionAssembled, sha256Hex, objectKey)
	if err != nil {
		return fmt.Errorf("set session assembled: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// SetSessionState moves the session to state.
func (p *PostgresStore) SetSessionState(ctx context.Context, id uuid.UUID, state string) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE upload_sessions SET state = $2, updated_at = now() WHERE id = $1`,
		id, state)
	if err != nil {
		return fmt.Errorf("set session state: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
