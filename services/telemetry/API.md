<!-- services/telemetry/API.md -->

# aether-edit telemetry service (FT-4)

The telemetry service feeds every file-console readout. It implements
contract 4 of the frozen cross-WO contracts v0 (Janus V-4): three
authenticated SSE endpoints, a 1 Hz hardware sampler, a job progress
aggregator, and a structured log stream.

## Configuration (environment)

| Variable | Default | Meaning |
|---|---|---|
| `TELEMETRY_LISTEN_ADDR` | `127.0.0.1:8094` | HTTP bind address (`host:port`) |
| `TELEMETRY_AUTH_HS256_KEY` | required | base64url (no padding) HS256 signing key; decodes to at least 32 bytes |
| `TELEMETRY_NATS_URL` | `nats://127.0.0.1:4222` | NATS server (`nats://` or `tls://`) |
| `TELEMETRY_STREAM_BUFFER` | `256` | per-connection SSE buffer, 16 to 4096 events |

All values are validated at startup; invalid values are rejected, never
coerced. The service exits with an error rather than guessing.

## Authentication

Every `/v1/streams/*` endpoint requires `Authorization: Bearer <JWT>`, where
the JWT is an HS256 token minted by the tenancy signer (FT-6a) and signed with
the same key configured here. The token is verified for the HS256 algorithm
only, a valid signature, a present and unexpired `exp`, an enforced `nbf` when
present, and both required claims: `sub` (the user id) and `workspaceId`. Any
missing, malformed, wrongly signed, expired, not-yet-valid, or claim-incomplete
token returns `401 {"error":"unauthorized"}`. `GET /healthz` is unauthenticated
and returns liveness plus `gpu` and `nats` status strings.

## SSE conventions (all three streams)

- `Content-Type: text/event-stream`; the stream opens with a `: connected`
  comment.
- Heartbeat comment `: hb` every 15 seconds keeps intermediaries alive.
- Per-connection backpressure is drop-oldest over a bounded buffer. Loss is
  never silent: before the next delivery after any drop, the stream emits
  `event: dropped` with `data: {"dropped":N}`.
- Query parameters other than those documented are rejected with 400.

## GET /v1/streams/hardware

1 Hz hardware samples (contract 4).

- `event: sample`, data:
  `{"gpuUtilPct":n,"vramUsedMB":n,"vramTotalMB":n,"junctionC":n,"powerW":n,"encoderSessions":n,"cpuUtilPct":n}`
- `event: status` (sticky; replayed to every new subscriber and republished
  on change), data: `{"stream":"hardware","gpu":"ok"|"unavailable"|"error","reason":string?}`

GPU metrics come from NVML (NVIDIA go-nvml bindings, device index 0):
utilization, memory info, `TEMPERATURE_GPU` sensor for `junctionC`, power
usage, and encoder session count. `cpuUtilPct` is the whole-machine
utilization derived from successive `/proc/stat` reads.

Honest absence: on a host with no usable GPU (no NVML library, no devices,
or init failure) the service still starts, the `status` event carries
`"gpu":"unavailable"` with a reason, and `sample` events omit all six GPU
keys entirely. Zeros are never fabricated; the console renders its
placeholder state from exactly this status event.

## GET /v1/streams/jobs

Job state transitions and progress (contract 4).

- `event: job`, data:
  `{"jobId":s,"state":"queued"|"running"|"completed"|"failed","fps":n,"speedX":n,"etaSeconds":n,"progressPct":n}`
- `event: aggregate` (sticky), data:
  `{"queued":n,"inFlight":n,"completed":n,"failed":n,"farmFps":n,"aggregateSpeedX":n}`

`farmFps` and `aggregateSpeedX` are sums over currently running jobs.
`completed` and `failed` are monotonic counters for the service lifetime.

## GET /v1/streams/logs

Tagged structured log lines (contract 4). Optional `?tag=<tag>` filters to
one tab (for example `tag=job` or `tag=transfer`); tags must match
`^[a-z][a-z0-9_-]{0,31}$`.

- `event: log`, data: `{"line":s,"tag":s,"level":"debug"|"info"|"warn"|"error","at":RFC3339}`

## NATS input subjects (proposed amendment 1 to contracts v0)

The telemetry service consumes three plain NATS subjects. Producers are the
FT-2 (transfer) and FT-3 (job) services. These subjects and the shared Go
types below are NOT part of the frozen contract 4 text; they are pinned as
`docs/contracts/ft-contracts-v0-amendment-1-ft4-telemetry.md` (JSON Schemas
included) and per the V-4 freeze rule require FT-2 and FT-3 owner
confirmation on the transcoders-first thread before this service merges.
Payloads failing validation are rejected and logged, never coerced; unknown
JSON fields are rejected.

### aether.ft.job.state.v1 (producer: FT-3)

```json
{"jobId":"...","workspaceId":"...","state":"queued|running|completed|failed","at":"RFC3339"}
```

One message per state transition. Terminal states remove the job from the
active set and increment the monotonic counters.

### aether.ft.job.progress.v1 (producer: FT-3)

```json
{"jobId":"...","workspaceId":"...","fps":120.5,"speedX":2.01,"etaSeconds":300,"progressPct":10,"at":"RFC3339"}
```

`fps`, `speedX`, `etaSeconds` must be nonnegative; `progressPct` in [0,100].
Progress for an unknown job implies the job is running.

### aether.ft.log.v1 (producers: FT-2 and FT-3)

```json
{"line":"probe done","tag":"job","level":"info","at":"RFC3339"}
```

`line` nonempty, at most 8192 bytes. `tag` per the tag pattern above
(`job` for encode-farm lines, `transfer` for upload lines). `level` one of
`debug|info|warn|error`.

## Dependencies and build notes

- `github.com/NVIDIA/go-nvml` (Apache-2.0): dlopens `libnvidia-ml.so` at
  runtime, so the binary builds and runs without NVIDIA drivers (cgo is
  required at build time).
- `github.com/nats-io/nats.go` (Apache-2.0).
- `github.com/evemeta-tony/aether-edit/services/contracts` via the in-repo
  `replace ../contracts` directive. The contracts module is owned by the
  FT-2 lane; this service compiles once that branch lands. The exported
  types this service uses are `HardwareSample`, `JobStreamEvent`, and
  `LogStreamEvent`, shaped exactly per
  `docs/contracts/ft-contracts-v0-amendment-1-ft4-telemetry.md` (Part A).

Acceptance of reported values against `nvidia-smi` ground truth on the OVH
box is a deployment-time check per R10 and is not claimed by the unit tests.
