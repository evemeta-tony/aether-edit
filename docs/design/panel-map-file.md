<!-- docs/design/panel-map-file.md -->
# File console panel map (FT-1)

Maps every rendered readout, state, and control in the file transcoder
prototype (docs/design/ui_kits/aether-live/FileTranscoder.jsx and Uploader.jsx,
plus the shared Parts.jsx primitives they use) to a named real source. The
prototypes are the spec of record: if a state is rendered there, a service in
this table must expose it.

Named sources referenced below:

- Frozen contracts v0 (Janus V-4): landed-object event on NATS subject
  aether.ft.upload.landed.v1; metering events on aether.ft.metering.v1; quota
  hook package services/contracts (QuotaChecker, ConfigQuota); FT-4 SSE streams
  GET /v1/streams/hardware, GET /v1/streams/jobs, GET /v1/streams/logs.
  Freeze-state note (Argus PR#5 pass 1, S1): for FT-4 only the three stream
  PATHS above are frozen. Every stream payload field named in this map
  (gpuUtilPct, progressPct, speedX, etaSeconds, line, tag, level, at) is
  proposed for build-ahead, to be ratified when FT-4's stream schema PR merges
  under the V-4 freeze rule. Do not treat these field names as settled.
- FT-2 upload-session API (expected routes, named here for build-ahead):
  POST /v1/uploads (create session; body carries fileName, sizeBytes, mime;
  response carries uploadId, chunkSizeBytes, maxParallelStreams, chunk map),
  GET /v1/uploads/{uploadId} (session state incl. server-held chunk map),
  PUT /v1/uploads/{uploadId}/chunks/{index} (chunk write, chunk checksum),
  POST /v1/uploads/{uploadId}/complete (assembly + whole-object checksum),
  DELETE /v1/uploads/{uploadId} (cancel).
- FT-3 job-service API (expected routes): GET /v1/jobs, GET /v1/jobs/{id},
  POST /v1/jobs, POST /v1/jobs/{id}/retry, GET /v1/jobs/{id}/probe,
  GET /v1/jobs/{id}/poster, GET /v1/presets, PATCH /v1/presets/{id},
  GET /v1/workers, GET /v1/queue, POST /v1/queue/pause, POST /v1/queue/resume,
  GET /v1/delivery-targets, POST /v1/delivery-targets.

"Derived (client)" means the UI computes the figure from other mapped state at
render time; per the display conventions below, derived figures stay derived.

## 1. Top bar (FileTranscoder.jsx)

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Breadcrumb trail: Workspaces / aether-media / File transcoder (ConsoleCrumb) | hardcoded trail prop | workspace name from tenancy context (see Unmapped, item U1); page label static |
| Queue running / Queue paused pill (Dot + label) | running state | FT-3 GET /v1/queue (queue run state), live updates via FT-4 /v1/streams/jobs |
| Active/concurrency readout "{active}/{MAX_CONCURRENT} . {queued} waiting" | derived from jobs array | Derived (client) from FT-3 GET /v1/jobs states; concurrency limit from GET /v1/queue |
| Pause queue / Resume queue button | setRunning toggle | FT-3 POST /v1/queue/pause and POST /v1/queue/resume |
| Add media button | add(NEXT_FILES...) | opens file picker; each file starts FT-2 POST /v1/uploads (quota-gated server side via QuotaChecker.CheckUploadSession) |
| UserMenu button and popover (Parts.jsx) | hardcoded props | see section 6 |

## 2. Left column

### 2.1 Source file panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Header right: file size "{job.size} GB" | job.size | FT-3 GET /v1/jobs/{id} (sizeBytes, originating from landed event sizeBytes) |
| Poster frame (image-slot) | user-dropped still, sidecar persisted | FT-3 GET /v1/jobs/{id}/poster (frame extracted at probe time) |
| "Poster . 00:00:04" badge | static | poster timestamp returned by FT-3 probe/poster metadata |
| Duration badge fmtDur(job.dur) | job.dur | FT-3 GET /v1/jobs/{id}/probe durationSeconds |
| Filename line | job.file | FT-3 GET /v1/jobs/{id} fileName (from upload session metadata; objectKey in the landed event is content-addressed, so display name travels in the job record) |
| Row Container | SRC[job.src].container | FT-3 probe container/format |
| Row Codec in | SRC.codec | FT-3 probe video codec |
| Row Resolution "{w}x{h}p{fps}" | SRC.w/h/fps | FT-3 probe width, height, fps |
| Row Chroma | SRC.chroma | FT-3 probe pixel format / bit depth |
| Row Source rate "{rate} Mb/s" | SRC.rate | FT-3 probe overall bitrate |
| Row Duration | fmtDur(job.dur) | FT-3 probe durationSeconds (derived formatting, client) |
| Streams inventory: video track, audio track count + stereo/discrete, subtitle track count | SRC.codec, SRC.audio, SRC.subs | FT-3 probe stream list (per-stream type, codec, channel layout) |
| Panel follows job-queue selection | sel state | client selection state; data per selected jobId from FT-3 |

### 2.2 Transcode farm panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Header right: "{active}/{workers} claimed" | derived index math | Derived (client) from FT-3 GET /v1/workers (claim state per worker) |
| Per worker: name, gpu type, region | WORKERS constant | FT-3 GET /v1/workers |
| Per worker claimed Dot (ok/idle) | index vs active count | FT-3 worker claim state (real scheduler assignment, jobId per worker); the prototype's index mapping is a known simplification and must NOT be reproduced |
| Per worker utilisation % + Meter | fake 58 + i*11 | FT-4 GET /v1/streams/hardware gpuUtilPct per worker (stream is per host; worker id keys the fan-out) |

## 3. Center column

### 3.1 Batch progress panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Header right: "{jobs.length} jobs . this batch" | jobs array | Derived (client) from FT-3 GET /v1/jobs (batch scope) |
| Read Completed "{done}/{total}" | derived | Derived (client) from FT-3 job states; transitions via FT-4 /v1/streams/jobs |
| Read In flight | derived | same |
| Read Queued | derived | same |
| Read Failed (err tone when nonzero) | derived | same |
| Read Farm throughput fps | sum of speed*25 | Derived (client): sum of fps over running jobs from FT-4 /v1/streams/jobs |
| Read Realtime multiple "x" | sum of speeds | Derived (client): sum of speedX from FT-4 /v1/streams/jobs |
| Graph (60-sample throughput sparkline) | hist array, random walk | client-held 60-sample ring buffer of the derived farm throughput, fed by FT-4 /v1/streams/jobs at stream cadence |

### 3.2 Job queue panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Filter chips All / Running / Queued / Done / Failed | filter state | client filter over FT-3 GET /v1/jobs (optionally GET /v1/jobs?state=...) |
| Column headers Source / Profile / Duration / Progress / Speed / Eta / State | static | static UI |
| Upload rows (in-flight transfers shown in the queue): filename | uploads from useUploads | FT-2 session (client transfer engine state, session created by POST /v1/uploads) |
| Upload row subtitle "uploading . {done}/{n} chunks . {size} GB" or "link lost . resuming from chunk map" | chunk map sim | FT-2 chunk map state (client engine mirrors server map from GET /v1/uploads/{uploadId}) |
| Upload row Profile and Duration cells: em dash placeholder | literal | display convention R1: value does not exist yet |
| Upload row progress % + Meter (color by state) | chunk map | Derived (client) from FT-2 chunk map (done cells / total) |
| Upload row Speed (MB/s figure) or em dash | sim rate | client transfer engine measured throughput (sum over live streams) |
| Upload row Eta cell: em dash | literal | display convention R1 (no ETA rendered in the queue row) |
| Upload row State (Uploading / Paused / Resuming / Verifying + Dot, pulse while uploading) | u.state | FT-2 session state machine: uploading, paused, error(resuming), verifying, done |
| Job rows: filename | j.file | FT-3 GET /v1/jobs |
| Job row subtitle: error reason in red, else "{worker} . {container} . {size} GB" | j.err / worker index map | FT-3 job record: failure reason string, assigned workerId (real scheduler), probe container, sizeBytes |
| Job row Profile name | profiles[j.preset].name | FT-3 GET /v1/presets (preset referenced by job) |
| Job row Duration | fmtDur(j.dur) | FT-3 probe durationSeconds |
| Job row progress % (em dash while queued) + Meter (err/ok/blue/idle by state) | j.pct | FT-4 /v1/streams/jobs progressPct; em dash while queued per R1 |
| Job row Speed "{n}x" or em dash | j.speed | FT-4 /v1/streams/jobs speedX |
| Job row Eta or em dash | derived from pct/speed | FT-4 /v1/streams/jobs etaSeconds (service computes; client formats) |
| Job row State label + Dot (Complete / Encoding / Queued / Failed; Paused when queue paused) | STATE map + running | FT-3 job state + queue run state; transitions via FT-4 /v1/streams/jobs |
| Retry button on failed rows | retry(id) resets state | FT-3 POST /v1/jobs/{id}/retry (re-queues; admission gated by QuotaChecker.CheckJobAdmission) |
| Row selection highlight (blue tint + inset bar) | sel/xfer state | client selection state |
| Drag-over DropVeil + drop to upload | onDrop handler | client drag and drop; each dropped file starts FT-2 POST /v1/uploads; real DataTransfer names and sizes only (the prototype's sample-title fallback is a preview affordance, not product behavior) |

### 3.3 Transfer / Job log panel (bottom)

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Tab chips: "Transfer . {n}" (active transfer count) / "Job log" | uploads filter | Derived (client) from FT-2 active sessions |
| Job log lines: timestamp, tag, message (warn tag amber) | FT_LOG pool + stamp() | FT-4 GET /v1/streams/logs events (line, tag, level, at); at renders as the timestamp, level=warn renders amber |
| Log autoscroll | logRef effect | client behavior, keep |
| Transfer tab content | TransferPanel | see section 5 |

## 4. Right column

### 4.1 EVE panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| EVE on/off toggle | eve state | FT-3 PATCH /v1/presets/{id} (mode: "eve" or "manual" on the active preset/profile) |
| "Encoding . Verified . Efficient" caption + Automated / Manual profile tag | eve state | Derived (client) from preset mode |
| EVE steps list (Source analysis, Per-title ladder, Quality target, Packaging) with ok Dots | EVE_STEPS constant | FT-3 job pipeline stage status for the selected job when EVE mode is on (probe/analysis, ladder plan, quality target, packaging), surfaced on GET /v1/jobs/{id}; static description text stays static |
| Delivery formats multi-select chips (HLS, DASH, MP4, CMAF) | formats state | FT-3 PATCH /v1/presets/{id} (packaging formats array) |
| Off-state copy ("Jobs use the output profile below...") | static | static UI |

### 4.2 Output profile panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Header right: preset name or "EVE . per title" | p.name / eve | FT-3 GET /v1/presets (name of the selected job's preset) |
| Lock badge + dimmed pane when EVE on | eve state | Derived (client) from preset mode |
| Container chips (MP4 / MOV / HLS / DASH / WebM) | patchProfile('container') | FT-3 PATCH /v1/presets/{id} container |
| Rate control chips (CRF / VBR / CBR) | patchProfile('rc') | FT-3 PATCH /v1/presets/{id} rateControl |
| Quality CRF slider (14..32) or Target bitrate slider (0.5..40 Mb/s), swapping on rc | patchProfile('crf'/'target') | FT-3 PATCH /v1/presets/{id} crf / targetMbps; which slider shows is Derived (client) from rateControl |
| GOP length slider (1..8 s) | patchProfile('gop') | FT-3 PATCH /v1/presets/{id} gopSeconds |
| Encoder speed chips (fast / medium / slow) + Cheapest/Smallest caption | patchProfile('speed') | FT-3 PATCH /v1/presets/{id} encoderSpeed |
| Row Codec | p.codec or EVE literal | preset codec from FT-3; EVE mode shows the EVE plan values from the job record |
| Row Resolution | p.w x p.h | preset from FT-3 (EVE: "Per-title ladder" from plan) |
| Row Frame rate | p.fps | preset from FT-3 (EVE: source cadence from probe) |
| Row Keyframe "{gop*fps} frames" | derived | Derived (client) from preset gopSeconds and fps, per R3 (EVE: scene-aligned, from plan) |
| Row Audio | p.audio | preset from FT-3 (EVE: loudness normalised, from plan) |
| Row Formats out | container or formats join | Derived (client) from preset (manual: container; EVE: packaging formats, "none" when empty) |
| Row Est. output "{n} GB" | duration x rate formula | Derived (client) from preset rate parameters and the selected job's probe duration, per R3; the estimator formula is owned by FT-3 docs and mirrored client side |
| Preset edit scope: edits apply to every job on that preset | setProfiles keyed by preset | FT-3 PATCH /v1/presets/{id} semantics (server-side shared preset) |

### 4.3 Delivery panel

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Plus icon (add target) | static | FT-3 POST /v1/delivery-targets |
| Per target: name, protocol tag, host, note | hardcoded list | FT-3 GET /v1/delivery-targets; the prototype's Notify webhook target (proto HOOK, hooks.aether.cloud/jobs) is excluded from v1 per AM-10, see U5 |
| Per target health Dot (ok/warn, idle when queue paused) | tone + running | FT-3 delivery target health on GET /v1/delivery-targets, idled by queue run state |

## 5. Transfer panel and upload primitives (Uploader.jsx)

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Empty state: prompt + "{CHUNK_MB} MB chunks . {THREADS} parallel streams . resumes on break" | constants | FT-2 POST /v1/uploads response (chunkSizeBytes, maxParallelStreams); copy derives from session config, never hardcoded client side |
| Upload selector chips (when >1 transfer) | uploads map | client, over FT-2 sessions |
| Filename + state Dot (pulse while uploading) + state label (Transferring / Paused / Link lost . resuming / Verifying checksum / Landed) | U_STATE | FT-2 session state machine; "Landed" corresponds to the landed-object event aether.ft.upload.landed.v1 having been published |
| Pause button | pause(id) | client transfer engine stops claiming chunks; in-flight cells revert to pending in the local map; no server call required (server map is authoritative for done cells only) |
| Resume button (paused or error state) | resume(id) | client engine re-fetches FT-2 GET /v1/uploads/{uploadId} chunk map and resumes claiming; survives process restart because the map is server-held |
| Drop link button | breakLink(id) | prototype-only fault injection demonstrating resume; production keeps the recovery path (auto-resume from server chunk map on transport error) but does NOT ship a deliberate link-drop control |
| Cancel button | cancel(id) | FT-2 DELETE /v1/uploads/{uploadId} |
| Overall progress % + Meter (err/warn/blue by state) | chunk map | Derived (client) from FT-2 chunk map |
| Bytes moved "{moved} / {total} GB" | per-cell span math | Derived (client) from done cells x per-cell span, per R2 |
| Read Throughput MB/s | sum of live stream rates | client transfer engine measured per-stream throughput, summed; must be measured wire rate, not paint rate (R6) |
| Read Eta (em dash when absent) | remaining / rate | Derived (client) from remaining bytes and measured throughput; em dash when rate is 0, per R1 |
| Read Chunk retries (warn tone past threshold) | retries counter | client engine retry counter, reconciled with FT-2 per-chunk write failures; amber past threshold |
| Read Chunks "{done}/{total}" | chunk map | FT-2 chunk map |
| Streams caption "Streams . {live}/{THREADS}" + 8 per-thread meters | streams array | client engine per-stream state (thread count from session maxParallelStreams; a varying count needs no redesign per the handoff) |
| Chunk map caption "Chunk map . {cellLabel} per cell" | perCell math | Derived (client), per R2: caption reports the per-cell span |
| Chunk map cells (pending / in-flight / retry / done colors) | chunks array | FT-2 chunk map (done, authoritative) merged with client engine (in-flight, retry) |
| DropVeil overlay: "Drop media to upload" + chunk/stream caption | over state | client drag state; caption from session config defaults |
| onLanded handoff: landed transfer becomes a queued job + log line "landed . checksum verified . queued" | onLanded callback | contract 1: FT-2 publishes aether.ft.upload.landed.v1 after verify; FT-3 consumes it, may auto-probe, and the job appears in GET /v1/jobs; the log line arrives via FT-4 /v1/streams/logs. Metering: FT-2 emits upload_session_created and upload_completed, FT-3 emits job_queued (contract 2) |

## 6. Shared chrome (Parts.jsx primitives used by the file console)

| Rendered item | Prototype source | Real source |
| --- | --- | --- |
| Panel, Eb, Row, Read, Meter, Dot, TChip, DragSlider, Graph, ConsoleCrumb | presentational primitives | presentational; carry the display conventions in section 7. Data bindings are listed per panel above |
| stamp() timestamps | client clock | replaced by event "at" fields (RFC3339) from the producing service, rendered as HH:MM:SS |
| jit() jittered figures | simulation helper | simulation only; every production figure comes from a stream or API above. jit must not survive into product code |
| UserMenu identity (name, email, org, role) | hardcoded props | tenancy/auth session (Unmapped U1) |
| UserMenu workspace switcher (active checked) | hardcoded list | tenancy workspace list (Unmapped U1) |
| UserMenu Plan row ("Scale . annual") | hardcoded | billing/plan API (Unmapped U2) |
| UserMenu Encode hours "812 / 1,500" + quota Meter | hardcoded | FT-6 metering rollup over aether.ft.metering.v1 (encodeSeconds aggregation) against the workspace limit from the ConfigQuota config file (contracts 2 and 3); until FT-6 lands, the limit side is readable from the same config ConfigQuota enforces |
| UserMenu actions: Account settings, API keys, Billing and usage, Sign out | close-only buttons | account service routes (Unmapped U2) |

## 7. Display conventions (requirements, from HANDOFF.md "Conventions worth preserving")

- R1 Absent means em dash: a value that does not currently exist renders the em
  dash character (U+2014), never 0. Applies to queued progress, absent ETA,
  absent speed, upload rows' profile and duration cells, and stopped readouts.
- R2 Clamped chunk map: the chunk map renders at most 180 cells; on large files
  one cell stands for a span of the file. Bytes moved and ETA derive from the
  per-cell span, and the caption always reports the cell size.
- R3 Derived over hardcoded: keyframe interval, estimated output size, farm
  throughput, realtime multiple, ETA, and all count readouts are computed from
  other visible state, never stored as their own figures.
- R4 Mono tabular numerals: every changing figure uses var(--font-mono) with
  font-variant-numeric: tabular-nums so nothing jitters.
- R5 One signal blue: blue means live, selected, or active. Amber is caution,
  red is failure or on-air, grey is idle. No decorative color.
- R6 Honest throughput: transfer throughput is what the streams sustain on the
  wire, not a figure inferred from UI paint cadence.
- R7 No shadows or gradients outside the program monitor; labels at natural
  width carry white-space: nowrap.

## 8. Unmapped (explicit, with proposed owners)

| Id | Rendered item | Why unmapped | Proposed owner |
| --- | --- | --- | --- |
| U1 | UserMenu identity, workspace switcher, breadcrumb workspace name | no auth/tenancy service exists in the frozen contracts or the FT-1..FT-6 lane set | new WO: auth/tenancy service (session, workspace list, workspace switch); interim: single hardcoded workspace from deployment config |
| U2 | UserMenu Plan tier, Account settings, API keys, Billing and usage, Sign out targets | billing/account surface is out of scope for FT-2/FT-3/FT-4/FT-6 | new WO: account/billing service; Sign out belongs to the U1 auth WO |
| U3 | EVE per-title analysis engine (the decision logic behind the EVE steps: content-adaptive ladder, VMAF target) | contracts name no analysis service; FT-3 can carry the stage status fields (mapped in 4.1) but nothing computes them yet | new WO: EVE analysis service, feeding FT-3 job records |
| U4 | Batch scoping ("this batch" grouping of jobs) | no batch concept in the expected FT-3 routes; current map treats the visible job list as one batch | FT-3, as a batchId on POST /v1/jobs plus GET /v1/jobs?batch= |
| U5 | Notify webhook delivery target (proto HOOK, hooks.aether.cloud/jobs) rendered in the delivery panel | webhook delivery is excluded from v1 per AM-10; the rendered target is prototype content, not product scope | deferred Co-Chair decision (v1.x webhook WO, if ever); the delivery panel ships without a HOOK target type |

Coverage: every rendered readout, state, and control in FileTranscoder.jsx,
Uploader.jsx, and the Parts.jsx primitives they use appears in sections 1
through 6, or in section 8 with a proposed owner. Nothing was silently dropped.
