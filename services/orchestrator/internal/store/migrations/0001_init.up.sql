-- services/orchestrator/internal/store/migrations/0001_init.up.sql
-- Initial schema for the transcode job service (FT-3).

CREATE TABLE presets (
    id               uuid PRIMARY KEY,
    workspace_id     text        NOT NULL,
    name             text        NOT NULL,
    container        text        NOT NULL CHECK (container IN ('mp4', 'mov', 'hls', 'dash', 'webm')),
    video_codec      text        NOT NULL CHECK (video_codec IN ('h264', 'hevc', 'av1')),
    rate_control     text        NOT NULL CHECK (rate_control IN ('crf', 'vbr', 'cbr')),
    crf              integer     NOT NULL DEFAULT 0,
    bitrate_kbps     integer     NOT NULL DEFAULT 0,
    max_bitrate_kbps integer     NOT NULL DEFAULT 0,
    gop_length       integer     NOT NULL,
    speed_preset     text        NOT NULL CHECK (speed_preset IN ('p1', 'p2', 'p3', 'p4', 'p5', 'p6', 'p7')),
    ladder           jsonb       NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE sources (
    object_key   text PRIMARY KEY,
    workspace_id text        NOT NULL,
    sha256       text        NOT NULL,
    size_bytes   bigint      NOT NULL,
    mime         text        NOT NULL,
    media_info   jsonb       NOT NULL,
    probed_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sources_workspace_idx ON sources (workspace_id);

CREATE TABLE jobs (
    id                uuid PRIMARY KEY,
    workspace_id      text        NOT NULL,
    user_id           text        NOT NULL,
    preset_id         uuid        NOT NULL REFERENCES presets (id),
    source_object_key text        NOT NULL,
    source_sha256     text        NOT NULL,
    state             text        NOT NULL CHECK (state IN ('queued', 'running', 'completed', 'failed')),
    error_class       text        CHECK (error_class IN ('validation', 'asset', 'decode', 'encode', 'internal')),
    error_message     text        NOT NULL DEFAULT '',
    attempts          integer     NOT NULL DEFAULT 0,
    progress_pct      double precision NOT NULL DEFAULT 0,
    fps               double precision NOT NULL DEFAULT 0,
    speed_x           double precision NOT NULL DEFAULT 0,
    eta_seconds       double precision NOT NULL DEFAULT 0,
    outputs           jsonb       NOT NULL DEFAULT '[]',
    created_at        timestamptz NOT NULL DEFAULT now(),
    queued_at         timestamptz NOT NULL DEFAULT now(),
    started_at        timestamptz,
    finished_at       timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX jobs_state_queued_idx ON jobs (queued_at) WHERE state = 'queued';
CREATE INDEX jobs_workspace_state_idx ON jobs (workspace_id, state);
