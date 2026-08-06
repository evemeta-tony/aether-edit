-- services/tenancy/migrations/000001_init.up.sql
-- FT-6a tenancy schema: users, workspaces, memberships, refresh
-- tokens, login states, API keys, metering events, usage rollups.


CREATE TABLE tenancy_users (
    id                  text PRIMARY KEY,
    google_sub          text NOT NULL UNIQUE,
    email               text NOT NULL,
    name                text NOT NULL,
    active_workspace_id text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL,
    last_login_at       timestamptz NOT NULL
);

CREATE UNIQUE INDEX tenancy_users_email_idx ON tenancy_users (lower(email));

CREATE TABLE tenancy_workspaces (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    plan_tier  text NOT NULL,
    created_by text NOT NULL REFERENCES tenancy_users (id),
    created_at timestamptz NOT NULL
);

CREATE TABLE tenancy_memberships (
    workspace_id text NOT NULL REFERENCES tenancy_workspaces (id),
    user_id      text NOT NULL REFERENCES tenancy_users (id),
    role         text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX tenancy_memberships_user_idx ON tenancy_memberships (user_id);

CREATE TABLE tenancy_refresh_tokens (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES tenancy_users (id),
    family_id  text NOT NULL,
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz
);

CREATE INDEX tenancy_refresh_tokens_family_idx ON tenancy_refresh_tokens (family_id);

CREATE TABLE tenancy_login_states (
    state         text PRIMARY KEY,
    nonce         text NOT NULL,
    pkce_verifier text NOT NULL,
    created_at    timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL
);

CREATE TABLE tenancy_api_keys (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES tenancy_workspaces (id),
    name         text NOT NULL,
    secret_hash  text NOT NULL,
    created_by   text NOT NULL REFERENCES tenancy_users (id),
    created_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at   timestamptz
);

CREATE INDEX tenancy_api_keys_workspace_idx ON tenancy_api_keys (workspace_id);

CREATE TABLE tenancy_metering_events (
    event_id       text PRIMARY KEY,
    workspace_id   text NOT NULL,
    user_id        text NOT NULL DEFAULT '',
    kind           text NOT NULL,
    bytes          bigint,
    encode_seconds double precision,
    job_id         text NOT NULL DEFAULT '',
    at             timestamptz NOT NULL,
    received_at    timestamptz NOT NULL
);

CREATE INDEX tenancy_metering_events_ws_idx ON tenancy_metering_events (workspace_id, at);

CREATE TABLE tenancy_usage_rollups (
    workspace_id      text NOT NULL,
    month             text NOT NULL,
    bytes_uploaded    bigint NOT NULL DEFAULT 0,
    encode_seconds    double precision NOT NULL DEFAULT 0,
    upload_sessions   bigint NOT NULL DEFAULT 0,
    uploads_completed bigint NOT NULL DEFAULT 0,
    jobs_queued       bigint NOT NULL DEFAULT 0,
    jobs_started      bigint NOT NULL DEFAULT 0,
    jobs_completed    bigint NOT NULL DEFAULT 0,
    jobs_failed       bigint NOT NULL DEFAULT 0,
    updated_at        timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, month)
);

