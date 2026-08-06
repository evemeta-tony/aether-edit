-- services/upload/migrations/000001_create_upload_tables.up.sql

CREATE TABLE upload_sessions (
    id               UUID PRIMARY KEY,
    workspace_id     TEXT NOT NULL,
    user_id          TEXT NOT NULL,
    filename         TEXT NOT NULL,
    size_bytes       BIGINT NOT NULL CHECK (size_bytes > 0),
    mime             TEXT NOT NULL,
    chunk_size_bytes BIGINT NOT NULL CHECK (chunk_size_bytes > 0),
    chunk_count      INTEGER NOT NULL CHECK (chunk_count > 0),
    state            TEXT NOT NULL CHECK (state IN ('ACTIVE', 'ASSEMBLED', 'COMPLETED', 'CANCELLED')),
    s3_upload_id     TEXT NOT NULL,
    staging_key      TEXT NOT NULL,
    sha256           TEXT NOT NULL DEFAULT '',
    object_key       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX upload_sessions_workspace_idx ON upload_sessions (workspace_id, created_at);

CREATE TABLE upload_chunks (
    session_id  UUID NOT NULL REFERENCES upload_sessions (id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0),
    state       TEXT NOT NULL DEFAULT 'PENDING'
                CHECK (state IN ('PENDING', 'INFLIGHT', 'DONE', 'RETRY')),
    sha256      TEXT NOT NULL DEFAULT '',
    etag        TEXT NOT NULL DEFAULT '',
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, chunk_index)
);
