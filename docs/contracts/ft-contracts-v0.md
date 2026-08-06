<!-- docs/contracts/ft-contracts-v0.md -->
# Frozen Cross WO Contracts v0 (Janus V-4)

These four contracts are frozen. Every file transcoder work order builds
exactly to these shapes. The Go types for all four live in
`services/contracts` (module
`github.com/evemeta-tony/aether-edit/services/contracts`).

## 1. Landed object event

NATS JetStream subject: `aether.ft.upload.landed.v1`

JSON payload:

```json
{
  "uploadId": "uuidv7",
  "workspaceId": "string",
  "userId": "string",
  "objectKey": "assets/<workspaceId>/sha256/<hex64>",
  "sha256": "hex64",
  "sizeBytes": 0,
  "mime": "string",
  "landedAt": "RFC3339"
}
```

`sizeBytes` is an int64. Producer: the upload service (FT2). Consumer: the
job service (FT3), which may auto probe on receipt.

## 2. Metering events

NATS JetStream subject: `aether.ft.metering.v1`

JSON payload:

```json
{
  "eventId": "uuidv7",
  "workspaceId": "string",
  "userId": "string",
  "kind": "upload_session_created | upload_completed | job_queued | job_started | job_completed | job_failed",
  "bytes": 0,
  "encodeSeconds": 0,
  "jobId": "string",
  "at": "RFC3339"
}
```

`bytes` (int64), `encodeSeconds` (number), and `jobId` (string) are
optional and omitted when not applicable. Producers: FT2 and FT3.
Consumer: FT6 (later). Producers emit faithfully now.

## 3. Quota hook (Janus V-5)

Go package `services/contracts` (its own `go.mod`, module
`github.com/evemeta-tony/aether-edit/services/contracts`):

```go
type QuotaDecision struct {
    Allowed bool
    Reason  string
}

type QuotaChecker interface {
    CheckUploadSession(ctx context.Context, workspaceID string, sizeHintBytes int64) (QuotaDecision, error)
    CheckJobAdmission(ctx context.Context, workspaceID string) (QuotaDecision, error)
}
```

Ships now with `ConfigQuota`: a real implementation that reads per
workspace limits from a mounted YAML or JSON config file and enforces
them (it denies over the limit with a typed reason). It is a real
enforcement path, not a noop. FT6 later swaps in the metered
implementation and owns end to end verification.

## 4. Telemetry streams (FT4 owns)

SSE endpoints on the telemetry service. Event names and shapes are part
of this contract.

`GET /v1/streams/hardware` at 1 Hz, JSON fields:
`gpuUtilPct`, `vramUsedMB`, `vramTotalMB`, `junctionC`, `powerW`,
`encoderSessions`, `cpuUtilPct`

`GET /v1/streams/jobs` for job state transitions plus progress, JSON
fields: `jobId`, `state`, `fps`, `speedX`, `etaSeconds`, `progressPct`

`GET /v1/streams/logs` JSON fields: `line`, `tag`, `level`, `at`
