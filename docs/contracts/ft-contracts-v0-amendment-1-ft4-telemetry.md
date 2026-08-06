<!-- docs/contracts/ft-contracts-v0-amendment-1-ft4-telemetry.md -->

# Amendment 1 to cross-WO contracts v0: FT-4 telemetry shared types and NATS input subjects

STATUS: PROPOSED. Per the V-4 freeze rule, contract 4 (FT-4 SSE streams
/v1/streams/{hardware,jobs,logs}) is frozen; this amendment extends it and
therefore requires confirmation on the transcoders-first thread by the FT-2
and FT-3 owners before the FT-4 PR (#2) merges. Until confirmed, everything
below is a proposal authored by the FT-4 lane, not frozen contract text.

CONSUMING WOs: FT-4 (consumer of the subjects, exporter of the streams),
FT-2 (producer of aether.ft.log.v1 transfer lines; owner of the
services/contracts module that must export the shared Go types), FT-3
(producer of aether.ft.job.state.v1, aether.ft.job.progress.v1, and
aether.ft.log.v1 job lines), FT-5 (console consumer of the stream shapes).

## Part A: shared Go types in services/contracts

The services/contracts module (owned by the FT-2 lane, module path
github.com/evemeta-tony/aether-edit/services/contracts) must export exactly:

```go
type HardwareSample struct {
    GPUUtilPct      *float64 `json:"gpuUtilPct,omitempty"`
    VRAMUsedMB      *float64 `json:"vramUsedMB,omitempty"`
    VRAMTotalMB     *float64 `json:"vramTotalMB,omitempty"`
    JunctionC       *float64 `json:"junctionC,omitempty"`
    PowerW          *float64 `json:"powerW,omitempty"`
    EncoderSessions *int64   `json:"encoderSessions,omitempty"`
    CPUUtilPct      *float64 `json:"cpuUtilPct,omitempty"`
}

type JobStreamEvent struct {
    JobID       string  `json:"jobId"`
    State       string  `json:"state"`
    FPS         float64 `json:"fps"`
    SpeedX      float64 `json:"speedX"`
    EtaSeconds  float64 `json:"etaSeconds"`
    ProgressPct float64 `json:"progressPct"`
}

type LogStreamEvent struct {
    Line  string    `json:"line"`
    Tag   string    `json:"tag"`
    Level string    `json:"level"`
    At    time.Time `json:"at"`
}
```

The pointer-optional fields on HardwareSample are load bearing: they carry
the honest-absence guarantee (a GPU-less host omits all six GPU keys rather
than emitting zeros). A non-pointer shape with omitempty on value types
would silently emit zeros and break FT-4 acceptance; any change to these
shapes is a further amendment on the thread.

## Part B: JSON Schemas (wire shapes)

Schemas are JSON Schema draft 2020-12, hand-authored per V-4 (A3's
generated-types rule covers the render spec only). additionalProperties is
false throughout: consumers reject unknown fields, never coerce (S1).

### B.1 Hardware sample (SSE event "sample" on /v1/streams/hardware)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "aether.ft.telemetry.hardware-sample.v0",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "gpuUtilPct": {"type": "number"},
    "vramUsedMB": {"type": "number"},
    "vramTotalMB": {"type": "number"},
    "junctionC": {"type": "number"},
    "powerW": {"type": "number"},
    "encoderSessions": {"type": "integer"},
    "cpuUtilPct": {"type": "number"}
  },
  "dependentRequired": {
    "gpuUtilPct": ["vramUsedMB", "vramTotalMB", "junctionC", "powerW", "encoderSessions"]
  },
  "description": "All GPU keys are present together (GPU ok) or absent together (GPU unavailable or error). cpuUtilPct is present whenever a /proc/stat delta exists. Zeros are never fabricated for absent hardware."
}
```

### B.2 Job stream event (SSE event "job" on /v1/streams/jobs)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "aether.ft.telemetry.job-stream-event.v0",
  "type": "object",
  "additionalProperties": false,
  "required": ["jobId", "state", "fps", "speedX", "etaSeconds", "progressPct"],
  "properties": {
    "jobId": {"type": "string", "minLength": 1},
    "state": {"enum": ["queued", "running", "completed", "failed"]},
    "fps": {"type": "number", "minimum": 0},
    "speedX": {"type": "number", "minimum": 0},
    "etaSeconds": {"type": "number", "minimum": 0},
    "progressPct": {"type": "number", "minimum": 0, "maximum": 100}
  }
}
```

### B.3 Aggregate (sticky SSE event "aggregate" on /v1/streams/jobs)

The jobs stream emits two event types: per-job "job" events (B.2) and the
sticky "aggregate" event consumed by the FT-5 batch readouts (queue counts,
farm fps, aggregate realtime multiple). farmFps and aggregateSpeedX are sums
over currently running jobs; completed and failed are monotonic counters for
the service lifetime.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "aether.ft.telemetry.aggregate.v0",
  "type": "object",
  "additionalProperties": false,
  "required": ["queued", "inFlight", "completed", "failed", "farmFps", "aggregateSpeedX"],
  "properties": {
    "queued": {"type": "integer", "minimum": 0},
    "inFlight": {"type": "integer", "minimum": 0},
    "completed": {"type": "integer", "minimum": 0},
    "failed": {"type": "integer", "minimum": 0},
    "farmFps": {"type": "number", "minimum": 0},
    "aggregateSpeedX": {"type": "number", "minimum": 0}
  }
}
```

### B.4 Log stream event (SSE event "log" on /v1/streams/logs)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "aether.ft.telemetry.log-stream-event.v0",
  "type": "object",
  "additionalProperties": false,
  "required": ["line", "tag", "level", "at"],
  "properties": {
    "line": {"type": "string", "minLength": 1, "maxLength": 8192},
    "tag": {"type": "string", "pattern": "^[a-z][a-z0-9_-]{0,31}$"},
    "level": {"enum": ["debug", "info", "warn", "error"]},
    "at": {"type": "string", "format": "date-time"}
  }
}
```

## Part C: NATS input subjects

Three plain NATS subjects consumed by the telemetry service. Producers are
named per subject. Payloads failing validation are rejected and logged,
never coerced; unknown JSON fields and trailing data are rejected.

### C.1 aether.ft.job.state.v1 (producer: FT-3)

One message per job state transition.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "aether.ft.job.state.v1",
  "type": "object",
  "additionalProperties": false,
  "required": ["jobId", "workspaceId", "state", "at"],
  "properties": {
    "jobId": {"type": "string", "minLength": 1},
    "workspaceId": {"type": "string", "minLength": 1},
    "state": {"enum": ["queued", "running", "completed", "failed"]},
    "at": {"type": "string", "format": "date-time"}
  }
}
```

### C.2 aether.ft.job.progress.v1 (producer: FT-3)

Progress for an unknown job implies the job is running.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "aether.ft.job.progress.v1",
  "type": "object",
  "additionalProperties": false,
  "required": ["jobId", "workspaceId", "fps", "speedX", "etaSeconds", "progressPct", "at"],
  "properties": {
    "jobId": {"type": "string", "minLength": 1},
    "workspaceId": {"type": "string", "minLength": 1},
    "fps": {"type": "number", "minimum": 0},
    "speedX": {"type": "number", "minimum": 0},
    "etaSeconds": {"type": "number", "minimum": 0},
    "progressPct": {"type": "number", "minimum": 0, "maximum": 100},
    "at": {"type": "string", "format": "date-time"}
  }
}
```

### C.3 aether.ft.log.v1 (producers: FT-2 and FT-3)

Tag convention: "job" for encode-farm lines, "transfer" for upload lines.
Wire shape is exactly B.4 (the log stream is a validated passthrough; the
service normalizes "at" to UTC millisecond precision on the way out).

## Change control

This file follows the V-4 freeze rule. It freezes when the PR carrying it
merges with the consuming WOs named in the header, which itself requires
the FT-2 and FT-3 owner confirmations recorded on the transcoders-first
thread. Post-freeze changes require a further amendment on that thread.
