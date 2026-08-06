// packages/console/src/components/TransferPanel.tsx
//
// Transfer panel and chunk map, ported from Uploader.jsx but bound to the real
// upload engine (UploadView). The prototype's "Drop link" fault injector is
// removed (R10(b)); the recovery path it demonstrated (auto-resume from the
// server chunk map on transport error) is kept in the engine. Throughput is the
// engine's measured wire rate (R6); ETA, bytes-moved, and the per-cell span are
// derived (R2/R3); absent values render the em dash (R1). The chunk map is
// clamped to 180 cells with a per-cell span caption (R2).

import { Icon } from "./Icons";
import { Dot, Eb, Meter, Read, TChip, type Tone } from "./Parts";
import { EM_DASH } from "../lib/format";
import { MAX_PARALLEL, type EngineState, type UploadView } from "../upload/engine";
import type { ChunkState } from "../api/upload";

const MB = 1024 * 1024;
const GB = 1024 * MB;
const MAX_CELLS = 180; // R2 clamp

const U_STATE: Record<EngineState, [Tone, string]> = {
  uploading: ["ok", "Transferring"],
  paused: ["idle", "Paused"],
  error: ["err", "Link lost · resuming"],
  verifying: ["warn", "Verifying checksum"],
  landed: ["ok", "Landed"],
  canceled: ["idle", "Canceled"],
};

function cellColor(c: ChunkState): string {
  switch (c) {
    case "DONE":
      return "var(--blue-500)";
    case "INFLIGHT":
      return "var(--blue-300)";
    case "RETRY":
      return "var(--warn)";
    default:
      return "rgba(255,255,255,.07)";
  }
}

// clampCells reduces the chunk map to at most MAX_CELLS cells, where each cell
// stands for a span of chunks. A cell is DONE only if every chunk it covers is
// DONE; otherwise it reflects the "hottest" state present (retry > inflight >
// pending) so progress reads honestly.
function clampCells(chunks: ChunkState[]): ChunkState[] {
  if (chunks.length <= MAX_CELLS) return chunks;
  const per = Math.ceil(chunks.length / MAX_CELLS);
  const cells: ChunkState[] = [];
  for (let i = 0; i < chunks.length; i += per) {
    const span = chunks.slice(i, i + per);
    if (span.every((c) => c === "DONE")) cells.push("DONE");
    else if (span.some((c) => c === "RETRY")) cells.push("RETRY");
    else if (span.some((c) => c === "INFLIGHT")) cells.push("INFLIGHT");
    else cells.push("PENDING");
  }
  return cells;
}

function ChunkMap({ chunks }: { chunks: ChunkState[] }) {
  const cells = clampCells(chunks);
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 2, alignContent: "flex-start" }}>
      {cells.map((c, i) => (
        <span key={i} style={{ width: 8, height: 8, borderRadius: 1, background: cellColor(c), flex: "none" }} />
      ))}
    </div>
  );
}

function UBtn({
  icon,
  children,
  onClick,
  danger,
}: {
  icon?: string;
  children: React.ReactNode;
  onClick: () => void;
  danger?: boolean;
}) {
  return (
    <button
      className="ae-b"
      onClick={onClick}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        font: "600 10px var(--font-sans)",
        letterSpacing: ".09em",
        textTransform: "uppercase",
        padding: "6px 9px",
        borderRadius: "var(--r-xs)",
        cursor: "pointer",
        background: "transparent",
        border: "1px solid var(--line-strong)",
        color: danger ? "var(--err)" : "var(--fg1)",
        whiteSpace: "nowrap",
      }}
    >
      {icon && <Icon name={icon} size={11} />}
      {children}
    </button>
  );
}

export function TransferPanel({
  uploads,
  sel,
  onSel,
  pause,
  resume,
  cancel,
}: {
  uploads: UploadView[];
  sel: string | null;
  onSel: (id: string) => void;
  pause: (id: string) => void;
  resume: (id: string) => void;
  cancel: (id: string) => void;
}) {
  const u = uploads.find((x) => x.id === sel) || uploads.find((x) => x.state !== "landed") || uploads[0];
  if (!u) {
    return (
      <div
        style={{
          height: "100%",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 8,
          color: "var(--fg3)",
        }}
      >
        <Icon name="upload-cloud" size={20} color="var(--fg4)" />
        <span style={{ font: "var(--t-body-sm)" }}>Drop media onto the job queue to start a transfer</span>
        <span style={{ font: "var(--t-micro)", color: "var(--fg4)" }}>
          {/* Session config drives the copy; 64 MiB is the FT-2 fixed chunk. */}
          {64} MB chunks · up to {MAX_PARALLEL} parallel streams · resumes on break
        </span>
      </div>
    );
  }

  const [tone, label] = U_STATE[u.state];
  const done = u.doneChunks;
  const pct = u.chunkCount > 0 ? (done / u.chunkCount) * 100 : 0;
  const perCellBytes = u.chunkCount > 0 ? u.sizeBytes / Math.min(u.chunkCount, MAX_CELLS) : 0;
  const movedGB = u.bytesMoved / GB;
  const rateMBs = u.throughputBytesPerSec / MB;
  const eta = u.etaSeconds;
  const cellLabel = perCellBytes >= GB ? `${(perCellBytes / GB).toFixed(1)} GB` : `${Math.round(perCellBytes / MB)} MB`;

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", gap: 11, padding: "11px 13px", overflow: "hidden" }}>
      {uploads.length > 1 && (
        <div style={{ display: "flex", gap: 6, overflowX: "auto", flex: "none" }}>
          {uploads.map((x) => (
            <TChip key={x.id} active={x.id === u.id} onClick={() => onSel(x.id)}>
              {x.file.slice(0, 18)}
            </TChip>
          ))}
        </div>
      )}

      <div style={{ display: "flex", alignItems: "center", gap: 12, flex: "none" }}>
        <Dot tone={tone} pulse={u.state === "uploading"} />
        <span style={{ font: "400 12px var(--font-mono)", color: "#fff", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {u.file}
        </span>
        <span style={{ font: "var(--t-micro)", color: "var(--fg3)", whiteSpace: "nowrap" }}>{label}</span>
        <div style={{ flex: 1 }} />
        {u.state === "uploading" && <UBtn icon="pause" onClick={() => pause(u.id)}>Pause</UBtn>}
        {(u.state === "paused" || u.state === "error") && <UBtn icon="play" onClick={() => resume(u.id)}>Resume</UBtn>}
        <UBtn icon="x" danger onClick={() => cancel(u.id)}>Cancel</UBtn>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 12, flex: "none" }}>
        <span style={{ font: "400 12px var(--font-mono)", color: "#fff", width: 44, fontVariantNumeric: "tabular-nums" }}>
          {pct.toFixed(0)}%
        </span>
        <div style={{ flex: 1 }}>
          <Meter
            pct={pct}
            h={4}
            color={u.state === "error" ? "var(--err)" : u.state === "verifying" ? "var(--warn)" : "var(--blue-500)"}
          />
        </div>
        <span style={{ font: "400 11px var(--font-mono)", color: "var(--fg3)", whiteSpace: "nowrap", fontVariantNumeric: "tabular-nums" }}>
          {movedGB.toFixed(1)} / {(u.sizeBytes / GB).toFixed(1)} GB
        </span>
      </div>

      <div style={{ display: "flex", gap: 20, flex: 1, minHeight: 0 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 12, flex: "none", width: 150 }}>
          <div style={{ display: "flex", gap: 18 }}>
            <Read value={rateMBs > 0 ? rateMBs.toFixed(0) : "0"} unit="MB/s" label="Throughput" size={17} />
            <Read
              value={eta === null ? EM_DASH : eta > 60 ? `${Math.floor(eta / 60)}m` : `${Math.max(0, Math.floor(eta))}s`}
              label="Eta"
              size={17}
            />
          </div>
          <div style={{ display: "flex", gap: 18 }}>
            <Read value={String(u.retries)} label="Chunk retries" size={17} tone={u.retries > 6 ? "var(--warn)" : undefined} />
            <Read value={`${done}/${u.chunkCount}`} label="Chunks" size={17} />
          </div>
        </div>

        <div style={{ flex: "none", width: 176, display: "flex", flexDirection: "column", gap: 5, minHeight: 0 }}>
          <Eb>Streams · {u.liveStreams}/{MAX_PARALLEL}</Eb>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "5px 10px" }}>
            {u.streams.map((c, i) => (
              <div key={i} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                <span style={{ font: "400 9px var(--font-mono)", color: "var(--fg4)", width: 14 }}>
                  {String(i + 1).padStart(2, "0")}
                </span>
                <div style={{ flex: 1 }}>
                  {/* A live stream shows a full bar (it is actively moving a
                      chunk); an idle stream shows an empty bar. No paint-rate
                      animation is fabricated (R6). */}
                  <Meter pct={c === null ? 0 : 100} h={2} color={c === null ? "var(--idle)" : "var(--viz-2)"} />
                </div>
              </div>
            ))}
          </div>
        </div>

        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 5 }}>
          <Eb>Chunk map · {cellLabel} per cell</Eb>
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
            <ChunkMap chunks={u.chunkStates} />
          </div>
        </div>
      </div>
    </div>
  );
}

export function DropVeil({ over }: { over: boolean }) {
  if (!over) return null;
  return (
    <div
      style={{
        position: "absolute",
        inset: 0,
        zIndex: 5,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 10,
        pointerEvents: "none",
        background: "rgba(10,16,32,.86)",
        border: "1px dashed var(--blue-500)",
        borderRadius: "var(--r-md)",
      }}
    >
      <Icon name="upload-cloud" size={26} color="var(--blue-300)" />
      <span style={{ font: "var(--t-h3)", letterSpacing: "var(--ls-head)", color: "#fff" }}>Drop media to upload</span>
      <span style={{ font: "var(--t-micro)", color: "var(--fg3)" }}>{64} MB chunks · up to {MAX_PARALLEL} parallel streams · resumable</span>
    </div>
  );
}
