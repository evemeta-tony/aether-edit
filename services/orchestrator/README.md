<!-- services/orchestrator/README.md -->

# aether-edit orchestrator (FT-3): transcode job service

Single-node ("farm of one", per T-7) transcode job service. It consumes
landed-object events from the upload service, probes sources with ffprobe,
manages presets and jobs in Postgres, schedules encodes onto a fixed number
of concurrent slots (console model, default 3), runs FFmpeg through the
TranscodeEngine adapter, writes outputs back to object storage, and emits
metering plus live progress events. A multi-node scheduler is explicitly
deferred to a later work order.

## Runtime configuration (environment)

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `ORCH_HTTP_ADDR` | no | `127.0.0.1:5203` | API listen address |
| `ORCH_DATABASE_URL` | yes | | Postgres URL |
| `ORCH_NATS_URL` | yes | | NATS server URL |
| `ORCH_OBJECT_STORE_ROOT` | yes | | Filesystem object store root shared with FT-2 on this node |
| `ORCH_STAGING_DIR` | no | `/var/tmp/aether-orchestrator` | Encode staging area |
| `ORCH_FFMPEG_PATH` | yes | | AM-5 built ffmpeg binary |
| `ORCH_FFPROBE_PATH` | yes | | AM-5 built ffprobe binary |
| `ORCH_SCHEDULER_SLOTS` | no | `3` | Concurrent encode slots |
| `ORCH_SCHEDULER_POLL_INTERVAL` | no | `2s` | Idle queue poll period |
| `ORCH_QUOTA_CONFIG` | yes | | Mounted quota limits JSON file |
| `ORCH_JWT_SECRET` | yes | | HS256 bearer token secret (env only, never in repo) |

Startup license gate: the service runs `ffmpeg -hide_banner -buildconf` and
parses the output. If the build advertises `--enable-gpl` or
`--enable-nonfree` the service logs the offending flag and exits nonzero.
This is a hard refusal, not a warning.

## Quota config file

JSON, strict (unknown fields rejected). `-1` means unlimited; defaults are
required.

```json
{
    "defaults": { "maxActiveJobs": 3, "maxUploadBytes": 5368709120 },
    "workspaces": { "ws-premium": { "maxActiveJobs": 10 } }
}
```

`maxActiveJobs` caps jobs in state queued plus running per workspace and is
enforced by `CheckJobAdmission` at POST /v1/jobs time (denials return HTTP
429 with a typed `quota_exceeded:...` reason). FT-6 later swaps in the
metered QuotaChecker behind the same frozen interface.

## Auth

Every route requires `Authorization: Bearer <JWT>` (HS256). Claims: `sub`
is the user id, `workspaceId` is the workspace. All reads and writes are
scoped to the token's workspace.

## HTTP API

All bodies are strict JSON: unknown fields, malformed values, and trailing
data are rejected with 400 and never coerced.

### Jobs

- `GET /v1/jobs?state=queued|running|completed|failed` lists jobs (newest
  first, capped at 200). The optional filter must be one of the four frozen
  states.
- `GET /v1/jobs/{id}` returns one job, including source object key and
  sha256, error class and message (taxonomy:
  `validation|asset|decode|encode|internal`), attempts, per-output progress
  (`outputs[]` with name, state, progressPct, objectKey), and live
  fps / speedX / etaSeconds / progressPct.
- `POST /v1/jobs` body `{"objectKey": "assets/<ws>/sha256/<hex64>",
  "presetId": "<uuid>"}`. The source must already be probed (a landed-object
  event was consumed for it), otherwise 422. Admission runs through the
  quota hook; denial is 429 with the reason. Success is 201 with the queued
  job and emits a `job_queued` metering event.
- `POST /v1/jobs/{id}/retry` retries a FAILED job only (returns it to
  queued, clearing error state); any other state is 409.
- `DELETE /v1/jobs/{id}` cancels. Queued jobs transition directly to failed
  with error class `internal` and message `canceled by user` (the state set
  is frozen to four states, so cancel resolves to failed; 200 with the final
  job). Running jobs get the cancel delivered to the in-process scheduler
  (202; the runner finalizes the same terminal shape). Completed and failed
  jobs return 409.

Job lifecycle: `queued -> running -> completed | failed`, plus
`failed -> queued` via retry. Nothing else.

### Presets

- `GET /v1/presets`, `GET /v1/presets/{id}`
- `POST /v1/presets` with the full definition:
  `name`, `container` (`mp4|mov|hls|dash|webm`), `videoCodec`
  (`h264|hevc|av1`; webm requires av1), `rateControl` (`crf|vbr|cbr`) with
  the matching value field (`crf` 0..51 for crf; `bitrateKbps` and optional
  `maxBitrateKbps` for vbr; `bitrateKbps` for cbr), `gopLength` (frames,
  1..600), `speedPreset` (`p1`..`p7`), and `ladder` (1..8 rungs of
  `{name, width, height}`, even dimensions).
- `PATCH /v1/presets/{id}` updates provided fields; cross-field constraints
  are validated on the merged result, so a rate control mode change must
  arrive with consistent value fields or the whole patch is rejected.

Preset edit semantic: an edit applies to jobs that START after the edit
commits. The scheduler snapshots the preset row when it claims a job, so
running jobs are never re-parameterized mid-encode; queued jobs pick up the
edited preset when they start. This is the documented API contract.

## Events

- Consumes `aether.ft.upload.landed.v1` (frozen contract 1) via a durable
  JetStream consumer and auto-probes each landed object with ffprobe
  (container, codecs, resolution, chroma subsampling, source bitrate,
  duration, and the video/audio/subtitle stream inventory), persisting the
  media info with the source. Malformed events are terminated; transient
  failures are NAKed with delay.
- Emits `aether.ft.metering.v1` (frozen contract 2): `job_queued`,
  `job_started`, `job_completed` (with `encodeSeconds`), `job_failed`.
- Publishes job state transitions and live progress on
  `aether.ft.jobs.progress.v1` (core NATS) with the field names frozen by
  contract 4 for the FT-4 `/v1/streams/jobs` SSE stream: `jobId`, `state`,
  `fps`, `speedX`, `etaSeconds`, `progressPct`. The subject name itself is
  an FT-3 choice, coordinated with FT-4.

## Outputs

Each ladder rung encodes with the AM-5 NVENC encoder mapped from the
codec-neutral preset (`h264 -> h264_nvenc`, `hevc -> hevc_nvenc`,
`av1 -> av1_nvenc`), staged locally, then stored under
`outputs/<workspaceId>/<jobId>/<rungName>/` in the object store. Progress is
parsed from the ffmpeg `-progress` pipe (stdout, never argv). HLS uses fmp4
segments; DASH emits a manifest plus segment files; webm audio is Opus, all
other containers use AAC.

## Migrations

Embedded SQL migrations run at startup through a small pgx-based runner
(versioned files, transaction per migration, advisory-lock serialized).
golang-migrate was deliberately not used: its module graph carries MPL-2.0
dependencies, outside the project license allowlist (rule S7).
