# Aether Cloud transcoders — implementation handoff

Two console UIs, built as working prototypes rather than comps. Every control listed
here is wired: state changes, the data model behind it, and the numbers on screen all
move. Nothing talks to a backend — the tick loops are faithful simulations of the
state machines the real services need to expose.

Read this alongside the source. The prototypes ARE the spec: if a state is rendered
here, the transport or service needs to expose it.

---

## Files

```
ui_kits/aether-live/
  Live_transcoder.html    live console page
  File_transcoder.html    file/VOD console page
  Parts.jsx               shared primitives (both pages)
  Transcoder.jsx          live console
  FileTranscoder.jsx      file console
  Uploader.jsx            resumable chunked upload state machine + Transfer panel
  image-slot.js           user-fillable image placeholder (poster frame)
../aether-edit/Icons.jsx  Lucide wrapper + Aether mark
../../colors_and_type.css design tokens
```

Load order matters (classic scripts, no modules): `Icons.jsx` → `Parts.jsx` →
`Uploader.jsx` → console. Each file publishes to `window` at the end; nothing is
bundled.

### Parts.jsx — shared primitives

| Export | Purpose |
| --- | --- |
| `Panel` | titled surface, `right` slot for header controls |
| `Read` | mono numeral + tracked unit caption — the house readout |
| `Meter` | thin progress bar, colour-coded by state |
| `Dot` | status dot: `ok / warn / err / idle / onair`, optional pulse |
| `TChip` | uppercase toggle chip |
| `DragSlider` | pointer-drag numeric control |
| `Row` | label/value spec row |
| `Graph` | 60-sample sparkline with fill |
| `ConsoleCrumb` | wordmark + breadcrumb trail |
| `UserMenu` | account menu (identity, workspace switch, plan, actions) |
| `stamp / jit / mb` | timestamp, jitter, 2-dp format |

All styling is CSS custom properties from `colors_and_type.css`. No hard-coded
brand colours — 96 tokens, zero unresolved references.

---

## Shared SaaS chrome

Both consoles carry the same top bar: mark + breadcrumb on the left, run-state
pill and primary action on the right, then `UserMenu`.

`UserMenu` renders identity, a workspace switcher (active workspace checked),
plan tier with an encode-hours-against-quota meter, and account / API keys /
billing / sign out. Props: `name`, `email`, `org`, `role`. The workspace list and
usage figures are placeholders — wire to the tenancy and metering APIs.

---

## Live transcoder (`Transcoder.jsx`)

A single bonded ingest fanned out to an ABR ladder. One 500 ms tick drives ingest
rates, per-rendition measured bitrate, GPU load, the aggregate graph and the log.

**Ingest (left).** Bonded source: input rate and link RTT as readouts, then one
row per WAN link (modem, wifi, ethernet) with live bitrate and meter. Below, a
spec block: source format, input codec, decode path, loss recovered. When stopped,
every live figure falls to zero or em-dash — that fallback is deliberate and should
be preserved.

**Encoder hardware (left).** GPU/CPU/VRAM utilisation meters plus junction
temperature, power draw and session count. GPU load is *derived*, not faked:
`Σ (w × h × fps)` across enabled renditions, normalised. Enable a rung and GPU
climbs; disable one and it drops. Sessions read `enabledCount/8`.

**Program monitor (centre).** 16:9 preview with an on-air badge and burnt-in
timecode. Shows "No program" when stopped.

**Aggregate output (centre).** Egress, glass-to-glass latency, frames encoded and
dropped, over a 60-second throughput graph. Egress is the sum of enabled rung
targets plus the audio track; the graph max scales to it.

**Transcode ladder (centre).** The core table. Per rung: enable toggle, name,
codec, profile, target bitrate, measured bitrate with meter, GPU share, state.
Toggling a rung recomputes aggregate egress, GPU load and session count
immediately. Audio is a fixed final row. ABR ladder / Passthrough chips in the
header.

**Rendition (right).** Parameters for the selected rung, and they write back to
the ladder: rate control (CBR/VBR/CQ), target bitrate, GOP length, B-frames,
quality preset p1–p7. Below, derived spec rows — note keyframe interval is
computed as `gop × fps`, so it tracks the slider.

**Destinations (right).** Per-destination name, protocol, host and which rendition
it takes, with health dots that idle when stopped.

**Pipeline log (centre bottom).** Timestamped, tagged, auto-scrolling.

**Start/stop.** The top-bar button gates every live figure in one place.

---

## File transcoder (`FileTranscoder.jsx`)

Batch VOD. Same shell, different shape: a farm working a queue of files.

**Source file (left).** Poster frame (an `<image-slot>` — drag any still onto it
and it persists), then mediainfo for the selected job: container, input codec,
resolution, chroma, source rate, duration. Below, the stream inventory: video,
audio track count, subtitle track count. All of it follows the table selection.

**Transcode farm (left).** Worker nodes with GPU type, region and utilisation.
Nodes are claimed as jobs go running and released as they finish.

**Batch progress (centre).** Completed / in flight / queued / failed counts,
farm throughput in fps, and aggregate realtime multiple, over a throughput graph.

**Job queue (centre).** Filterable by state. Per job: filename with a subtitle
line (assigned worker, container, size — or the error reason in red), profile,
duration, progress meter, speed as a realtime multiple, ETA, state. Failed jobs
render a Retry button that returns them to the queue.

The queue runs on a 500 ms tick with three concurrent slots. A job advances by
`(speed × tickSeconds) / duration`; on completion the scheduler promotes the next
queued job and assigns it a speed. Pause/resume from the top bar holds everything
without losing progress.

**EVE (right).** Toggle above the output profile.

- *Off* — the manual output profile below is live.
- *On* — EVE decides source analysis, per-title ladder, quality target and
  packaging upstream. The output profile pane locks (lock badge, dimmed) and its
  spec rows switch to derived values: per-title ladder, source cadence,
  scene-aligned keyframes, loudness-normalised audio. The one control that stays
  live is **delivery formats** — HLS, DASH, MP4, CMAF, multi-select.

**Output profile (right).** Container (MP4 / MOV / HLS / DASH / WebM), rate control
(CRF / VBR / CBR — the slider below swaps between quality and target bitrate
accordingly), GOP length, encoder speed. Then derived rows including keyframe
interval and estimated output size, which recompute from the controls and the
selected job's duration. Edits write to the *preset*, so every job on that preset
follows.

**Delivery (right).** Post-transcode targets: media library, CDN package, archive,
webhook.

**Transfer / Job log (centre bottom).** Tabbed. Log is timestamped and tagged;
warnings render in amber.

---

## Upload: resumable, chunked, multi-threaded (`Uploader.jsx`)

The most important part for implementation. Drop files anywhere on the job queue
(a dashed drop veil confirms the target) or use **Add media**. Real `DataTransfer`
files are read for name and size; a bare drop falls back to a sample title.

### The state machine

```
uploading ⇄ paused
uploading → error (link lost) → uploading   [resumes from the chunk map]
uploading → verifying → done → queued transcode job
```

`useUploads(onLanded)` owns it and returns
`{ uploads, add, pause, resume, breakLink, cancel }`. A 350 ms tick advances every
active transfer.

### How a transfer works

1. **Chunk map.** The file is split into 64 MB chunks; the map — not a byte offset
   — is the unit of truth. Each cell is `PENDING | INFLIGHT | DONE | RETRY`.
2. **Parallel claim.** Eight threads each claim a pending chunk. Aggregate
   throughput is the sum, so one slow TCP stream can't cap the transfer.
3. **Retry.** A chunk that fails goes back to the pool and is re-claimed by
   whichever thread frees up first. The retry counter is on screen and turns amber
   past a threshold.
4. **Resume.** On a break, in-flight chunks revert to pending and the map
   survives. Reconnecting resumes against the remainder — never from byte zero.
   The **Drop link** button demonstrates this; the queue row shows
   "link lost · resuming from chunk map".
5. **Verify.** When the map is complete the transfer enters `verifying` — a
   whole-object checksum against the assembled parts — and only then becomes a
   queued transcode job (`onLanded`), with a line written to the job log.

### The Transfer panel

Filename and state, controls (Pause / Resume / Drop link / Cancel), overall
progress with bytes moved against total, then throughput, ETA, chunk retries and
chunk count; eight per-thread meters; and the live chunk map.

Two display notes, both deliberate:

- The map is **clamped to 180 cells** for legibility, so on large files one cell
  stands for a span of the file rather than one 64 MB wire chunk. Bytes moved and
  ETA derive from that per-cell span, and the caption reports the cell size.
- **Throughput is what the threads sustain** (~80–130 MB/s per live stream), not a
  figure inferred from how fast the simulation paints. Eight threads on a 10G path
  land around 800 MB/s, which is the number that should appear in a spec.

Uploading files also appear as rows in the job queue itself, so there is one list
from ingest through delivery.

### What needs real engineering

- **Resumable session API** — create / query / complete an upload keyed to an
  upload ID, with the chunk map stored server-side so a resume survives a closed
  laptop, not just a dropped socket.
- **Chunk-level checksums** on write, whole-object checksum on assembly.
- **Adaptive concurrency** — thread count tuned to measured RTT and loss rather
  than fixed at eight. The UI already renders per-thread state, so a varying
  thread count needs no redesign.
- **Server-side assembly** and the handoff that turns a landed object into a
  queued transcode job.
- **Backpressure** — the UI assumes the client can be told to slow down; there is
  no state for "server rejected chunk, back off" yet. Add one if the transport
  needs it.

---

## Conventions worth preserving

- **Numerals are mono and tabular.** Every changing figure uses
  `var(--font-mono)` with `font-variant-numeric: tabular-nums` so nothing jitters.
- **Em-dash, not zero,** for a value that does not currently exist.
- **Derived over hard-coded.** GPU load, aggregate egress, keyframe interval,
  estimated output size and ETA are all computed from other visible state. Keep
  them derived — that is what makes the consoles read as instruments.
- **One signal blue.** Blue means live, selected or active. Amber is caution, red
  is failure or on-air, grey is idle. No decorative colour.
- **No shadows, no gradients** outside the program monitor.
- **Every label that sits at its natural width carries `white-space: nowrap`.**
  Console bars are dense; wrapping labels are the failure mode.

## Known simplifications

- Worker assignment maps running jobs to nodes by index, not by a scheduler.
- Log lines are drawn from a fixed pool.
- Ladder and job filter chips in a few panel headers (Warnings / Errors on the
  logs, Passthrough on the ladder) are presentational.
- Live console figures use jitter around a target rather than modelled network
  behaviour.
