// packages/console/src/components/panels/JobQueue.tsx
//
// Job queue: in-flight uploads (FT-2) shown above transcode jobs (FT-3), with
// live per-row progress/speed/eta from the FT-4 jobs stream. Filters are a
// client filter over the job list. Every simulated value from the prototype is
// removed (R10(b)): job progress rides the stream, queued rows show the em dash
// for progress/speed/eta (R1), the subtitle uses the real failure reason, and
// there is no index-based worker assignment.

import { Meter, Dot, type Tone } from "../Parts";
import { Icon } from "../Icons";
import { EM_DASH, fmtEta, fmtGB } from "../../lib/format";
import type { Job, JobState, Preset } from "../../api/jobs";
import type { JobStreamEvent } from "../../api/telemetry";
import type { UploadView } from "../../upload/engine";

const GRID = "1fr 96px 70px 124px 52px 62px 72px";
type Filter = "all" | "running" | "queued" | "done" | "failed";

const STATE_LABEL: Record<JobState, [Tone, string]> = {
  completed: ["ok", "Complete"],
  running: ["onair", "Encoding"],
  queued: ["idle", "Queued"],
  failed: ["err", "Failed"],
};

const FILTER_TO_STATE: Record<Exclude<Filter, "all">, JobState> = {
  running: "running",
  queued: "queued",
  done: "completed",
  failed: "failed",
};

export function JobQueue({
  jobs,
  presets,
  uploads,
  progress,
  filter,
  onFilter,
  sel,
  onSelJob,
  xfer,
  onSelXfer,
  onRetry,
  loading,
  errorMessage,
}: {
  jobs: Job[];
  presets: Preset[];
  uploads: UploadView[];
  progress: Map<string, JobStreamEvent>;
  filter: Filter;
  onFilter: (f: Filter) => void;
  sel: string | null;
  onSelJob: (id: string) => void;
  xfer: string | null;
  onSelXfer: (id: string) => void;
  onRetry: (id: string) => void;
  loading: boolean;
  errorMessage: string | null;
}) {
  const presetById = new Map(presets.map((p) => [p.id, p]));
  const shown = filter === "all" ? jobs : jobs.filter((j) => j.state === FILTER_TO_STATE[filter]);

  return (
    <div
      style={{
        flex: 1,
        minWidth: 0,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        background: "var(--bg-panel)",
        border: "1px solid var(--line)",
        borderRadius: "var(--r-md)",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "10px 13px", borderBottom: "1px solid var(--line)", flex: "none" }}>
        <span style={{ font: "var(--t-eyebrow)", letterSpacing: "var(--ls-eyebrow)", textTransform: "uppercase", color: "var(--fg3)" }}>
          Job queue
        </span>
        <div style={{ flex: 1 }} />
        <div style={{ display: "flex", gap: 6 }}>
          {([
            ["all", "All"],
            ["running", "Running"],
            ["queued", "Queued"],
            ["done", "Done"],
            ["failed", "Failed"],
          ] as [Filter, string][]).map(([k, l]) => (
            <button
              key={k}
              className="ae-b"
              onClick={() => onFilter(k)}
              style={{
                font: "600 10px var(--font-sans)",
                letterSpacing: ".09em",
                textTransform: "uppercase",
                padding: "6px 10px",
                borderRadius: "var(--r-xs)",
                cursor: "pointer",
                whiteSpace: "nowrap",
                background: filter === k ? "var(--blue-tint)" : "transparent",
                border: `1px solid ${filter === k ? "var(--blue-500)" : "var(--line-strong)"}`,
                color: filter === k ? "var(--blue-300)" : "var(--fg2)",
              }}
            >
              {l}
            </button>
          ))}
        </div>
      </div>

      <div style={{ flex: 1, overflowY: "auto", minHeight: 0 }}>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: GRID,
            alignItems: "center",
            gap: 10,
            padding: "0 13px",
            height: 32,
            borderBottom: "1px solid var(--line)",
            font: "500 9px var(--font-sans)",
            letterSpacing: ".13em",
            textTransform: "uppercase",
            color: "var(--fg4)",
            position: "sticky",
            top: 0,
            background: "var(--bg-panel)",
            zIndex: 2,
          }}
        >
          <span>Source</span>
          <span>Profile</span>
          <span>Duration</span>
          <span>Progress</span>
          <span>Speed</span>
          <span>Eta</span>
          <span>State</span>
        </div>

        {/* Upload rows (only in the All filter, matching the prototype). */}
        {filter === "all" &&
          uploads.map((u) => {
            const upct = u.chunkCount > 0 ? (u.doneChunks / u.chunkCount) * 100 : 0;
            const rateMBs = u.throughputBytesPerSec / (1024 * 1024);
            return (
              <div
                key={u.id}
                onClick={() => onSelXfer(u.id)}
                style={{
                  display: "grid",
                  gridTemplateColumns: GRID,
                  alignItems: "center",
                  gap: 10,
                  padding: "0 13px",
                  minHeight: 46,
                  cursor: "pointer",
                  borderBottom: "1px solid var(--line)",
                  background: xfer === u.id ? "var(--blue-tint)" : "transparent",
                  boxShadow: xfer === u.id ? "inset 2px 0 0 var(--blue-500)" : "none",
                }}
              >
                <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                  <span style={{ font: "400 12px var(--font-mono)", color: "#fff", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {u.file}
                  </span>
                  <span style={{ font: "var(--t-micro)", color: u.state === "error" ? "var(--err)" : "var(--fg4)", whiteSpace: "nowrap" }}>
                    {u.state === "error"
                      ? "link lost · resuming from chunk map"
                      : `uploading · ${u.doneChunks}/${u.chunkCount} chunks · ${fmtGB(u.sizeBytes)}`}
                  </span>
                </div>
                <span style={{ font: "var(--t-body-sm)", color: "var(--fg4)" }}>{EM_DASH}</span>
                <span style={{ font: "400 12px var(--font-mono)", color: "var(--fg4)" }}>{EM_DASH}</span>
                <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                  <span style={{ font: "400 12px var(--font-mono)", color: "#fff", width: 34, fontVariantNumeric: "tabular-nums" }}>
                    {upct.toFixed(0)}%
                  </span>
                  <div style={{ flex: 1 }}>
                    <Meter pct={upct} color={u.state === "error" ? "var(--err)" : u.state === "verifying" ? "var(--warn)" : "var(--viz-2)"} />
                  </div>
                </div>
                <span style={{ font: "400 12px var(--font-mono)", color: "var(--fg2)", fontVariantNumeric: "tabular-nums" }}>
                  {rateMBs > 0 ? rateMBs.toFixed(0) : EM_DASH}
                </span>
                <span style={{ font: "400 12px var(--font-mono)", color: "var(--fg4)" }}>{EM_DASH}</span>
                <span style={{ display: "flex", alignItems: "center", gap: 7, font: "var(--t-micro)", color: "var(--fg2)" }}>
                  <Dot
                    tone={u.state === "error" ? "err" : u.state === "verifying" ? "warn" : u.state === "paused" ? "idle" : "ok"}
                    pulse={u.state === "uploading"}
                  />
                  {u.state === "verifying" ? "Verifying" : u.state === "paused" ? "Paused" : u.state === "error" ? "Resuming" : "Uploading"}
                </span>
              </div>
            );
          })}

        {/* Job rows. */}
        {shown.map((j) => {
          const [tone, label] = STATE_LABEL[j.state];
          const live = progress.get(j.id);
          const pct = j.state === "running" && live ? live.progressPct : j.progressPct;
          const speed = j.state === "running" && live ? live.speedX : j.speedX;
          const eta = j.state === "running" && live ? live.etaSeconds : j.etaSeconds;
          const preset = presetById.get(j.presetId);
          const showPct = j.state !== "queued";
          const showSpeed = j.state === "running" && speed > 0;
          const showEta = j.state === "running" && eta > 0;
          return (
            <div
              key={j.id}
              onClick={() => onSelJob(j.id)}
              style={{
                display: "grid",
                gridTemplateColumns: GRID,
                alignItems: "center",
                gap: 10,
                padding: "0 13px",
                minHeight: 46,
                cursor: "pointer",
                borderBottom: "1px solid var(--line)",
                background: sel === j.id ? "var(--blue-tint)" : "transparent",
                boxShadow: sel === j.id ? "inset 2px 0 0 var(--blue-500)" : "none",
                opacity: j.state === "completed" ? 0.62 : 1,
              }}
            >
              <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                <span style={{ font: "400 12px var(--font-mono)", color: "#fff", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {j.sourceObjectKey.split("/").pop() ?? j.sourceObjectKey}
                </span>
                <span
                  style={{
                    font: "var(--t-micro)",
                    color: j.errorMessage ? "var(--err)" : "var(--fg4)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {j.errorMessage
                    ? `${j.errorClass ? j.errorClass + " · " : ""}${j.errorMessage}`
                    : `attempt ${j.attempts} · ${preset ? preset.container.toUpperCase() : EM_DASH}`}
                </span>
              </div>
              <span style={{ font: "var(--t-body-sm)", color: "var(--fg2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {preset ? preset.name : EM_DASH}
              </span>
              <span style={{ font: "400 12px var(--font-mono)", color: "var(--fg3)", fontVariantNumeric: "tabular-nums" }}>
                {/* Duration is probe truth (FT-3), not on the job record: em dash. */}
                {EM_DASH}
              </span>
              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                <span
                  style={{
                    font: "400 12px var(--font-mono)",
                    color: showPct ? "#fff" : "var(--fg4)",
                    width: 34,
                    fontVariantNumeric: "tabular-nums",
                  }}
                >
                  {showPct ? `${Math.round(pct)}%` : EM_DASH}
                </span>
                <div style={{ flex: 1 }}>
                  <Meter
                    pct={pct}
                    color={
                      j.state === "failed"
                        ? "var(--err)"
                        : j.state === "completed"
                          ? "var(--ok)"
                          : j.state === "running"
                            ? "var(--blue-500)"
                            : "var(--idle)"
                    }
                  />
                </div>
              </div>
              <span style={{ font: "400 12px var(--font-mono)", color: "var(--fg2)", fontVariantNumeric: "tabular-nums" }}>
                {showSpeed ? `${speed.toFixed(1)}×` : EM_DASH}
              </span>
              <span style={{ font: "400 12px var(--font-mono)", color: "var(--fg2)", fontVariantNumeric: "tabular-nums" }}>
                {showEta ? fmtEta(eta) : EM_DASH}
              </span>
              {j.state === "failed" ? (
                <button
                  className="ae-b"
                  onClick={(e) => {
                    e.stopPropagation();
                    onRetry(j.id);
                  }}
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 6,
                    font: "600 10px var(--font-sans)",
                    letterSpacing: ".09em",
                    textTransform: "uppercase",
                    padding: "5px 8px",
                    borderRadius: "var(--r-xs)",
                    cursor: "pointer",
                    background: "transparent",
                    border: "1px solid var(--line-strong)",
                    color: "var(--fg1)",
                  }}
                >
                  <Icon name="rotate-ccw" size={11} />
                  Retry
                </button>
              ) : (
                <span style={{ display: "flex", alignItems: "center", gap: 7, font: "var(--t-micro)", color: "var(--fg2)" }}>
                  <Dot tone={tone} pulse={j.state === "running"} />
                  {label}
                </span>
              )}
            </div>
          );
        })}

        {/* Honest empty / loading / error states (R10). */}
        {shown.length === 0 && uploads.length === 0 && (
          <div style={{ padding: 24, textAlign: "center", color: "var(--fg4)", font: "var(--t-micro)" }}>
            {loading ? "Loading jobs..." : errorMessage ? `Jobs unavailable: ${errorMessage}` : "No jobs in this batch. Add media to start."}
          </div>
        )}
      </div>
    </div>
  );
}
