// services/upload/postgres_test.go

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// TestPostgresStore exercises the production store against a real
// scratch database. It runs when UPLOAD_TEST_DATABASE_URL is set (the
// migrations in ./migrations must already be applied); otherwise it is
// skipped so the suite stays runnable on hosts without Postgres.
func TestPostgresStore(t *testing.T) {
	dbURL := os.Getenv("UPLOAD_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("UPLOAD_TEST_DATABASE_URL not set; skipping Postgres store test")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dbURL)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		ID:             id,
		WorkspaceID:    "ws-pg-test",
		UserID:         "user-pg-test",
		Filename:       "pg.bin",
		SizeBytes:      3000,
		Mime:           "application/octet-stream",
		ChunkSizeBytes: 1024,
		ChunkCount:     3,
		State:          SessionActive,
		S3UploadID:     "mp-test",
		StagingKey:     "staging/" + id.String(),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.GetSession(ctx, id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.WorkspaceID != sess.WorkspaceID || got.ChunkCount != 3 || got.State != SessionActive {
		t.Fatalf("session = %+v", got)
	}

	chunks, err := store.GetChunks(ctx, id)
	if err != nil {
		t.Fatalf("GetChunks: %v", err)
	}
	if len(chunks) != 3 || chunks[0].State != ChunkPending {
		t.Fatalf("chunks = %+v", chunks)
	}

	claim, err := store.ClaimChunk(ctx, id, 0)
	if err != nil || claim != ClaimAcquired {
		t.Fatalf("ClaimChunk: %v %v", claim, err)
	}
	if err := store.MarkChunkDone(ctx, id, 0, "aa", `"etag"`, 1024); err != nil {
		t.Fatalf("MarkChunkDone: %v", err)
	}
	claim, err = store.ClaimChunk(ctx, id, 0)
	if err != nil || claim != ClaimAlreadyDone {
		t.Fatalf("ClaimChunk on DONE: %v %v", claim, err)
	}
	if _, err := store.ClaimChunk(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkChunkRetry(ctx, id, 1); err != nil {
		t.Fatalf("MarkChunkRetry: %v", err)
	}
	chunks, err = store.GetChunks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if chunks[0].State != ChunkDone || chunks[1].State != ChunkRetry || chunks[2].State != ChunkPending {
		t.Fatalf("chunk states = %+v", chunks)
	}

	if err := store.SetSessionAssembled(ctx, id, "beef", "assets/ws-pg-test/sha256/beef"); err != nil {
		t.Fatalf("SetSessionAssembled: %v", err)
	}
	if err := store.SetSessionState(ctx, id, SessionCompleted); err != nil {
		t.Fatalf("SetSessionState: %v", err)
	}
	got, err = store.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != SessionCompleted || got.SHA256 != "beef" {
		t.Fatalf("final session = %+v", got)
	}

	if _, err := store.GetSession(ctx, uuid.New()); err != ErrNotFound {
		t.Fatalf("missing session error = %v, want ErrNotFound", err)
	}
}

// TestMigrationFilesPresent keeps the golang-migrate layout honest.
func TestMigrationFilesPresent(t *testing.T) {
	for _, name := range []string{
		"000001_create_upload_tables.up.sql",
		"000001_create_upload_tables.down.sql",
	} {
		if _, err := os.Stat(filepath.Join("migrations", name)); err != nil {
			t.Fatalf("missing migration file %s: %v", name, err)
		}
	}
}
