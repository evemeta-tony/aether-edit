// packages/console/src/components/FileTranscoder.tsx
//
// The production file-transcoder console, ported from
// docs/design/ui_kits/aether-live/FileTranscoder.jsx and wired to the real
// FT-2/FT-3/FT-4/FT-6a services. Every simulated value from the prototype is
// gone (R10(b)): the 500ms job tick, the random throughput walk, the sample
// title fallback, the Drop-link fault injector, and the index-based worker
// assignment are all replaced by live API/SSE data, and where a backend is not
// reachable the UI shows honest loading/empty/error states.
//
// Layout, spacing, and the display conventions (mono tabular numerals, em dash
// for absent values, derived-over-hardcoded, one signal blue, no shadows) are
// preserved from the prototype.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { SessionState } from "../hooks/useSession";
import { useUploads } from "../hooks/useUploads";
import { useHardwareStream, useJobsStream, useLogsStream } from "../hooks/useTelemetry";
import { useJobsData } from "../hooks/useJobs";
import { ConsoleCrumb, Dot, Graph, Panel, Read } from "./Parts";
import { Icon } from "./Icons";
import { UserMenu } from "./UserMenu";
import { TransferPanel, DropVeil } from "./TransferPanel";
import { SourcePanel } from "./panels/SourcePanel";
import { HardwarePanel } from "./panels/HardwarePanel";
import { JobQueue } from "./panels/JobQueue";
import { EvePanel } from "./panels/EvePanel";
import { OutputProfilePanel } from "./panels/OutputProfilePanel";
import { DeliveryPanel } from "./panels/DeliveryPanel";
import { retryJob as apiRetryJob } from "../api/jobs";
import { EM_DASH, fmtInt, fmtNum, fmtTimeOfDay } from "../lib/format";
import type { EveState } from "./panels/EvePanel";

const HIST_LEN = 60; // throughput sparkline ring buffer length

export function FileTranscoder({ session }: { session: SessionState }) {
  // ---- live streams (FT-4) ----
  const hardware = useHardwareStream();
  const jobsStream = useJobsStream();
  const [bottom, setBottom] = useState<"log" | "xfer">("log");
  const logs = useLogsStream(bottom === "xfer" ? "transfer" : "job");

  // ---- job + preset data (FT-3) ----
  const jobsData = useJobsData(jobsStream.transitionTick);

  // ---- selection + filter (client) ----
  const [sel, setSel] = useState<string | null>(null);
  const [filter, setFilter] = useState<"all" | "running" | "queued" | "done" | "failed">("all");
  const [xfer, setXfer] = useState<string | null>(null);
  const [over, setOver] = useState(false);

  // EVE toggle (AM-11): local until the active preset's mode round-trips.
  const [eve, setEve] = useState<EveState>({ on: false, formats: ["HLS", "DASH"], pending: false });

  // ---- uploads (FT-2) ----
  const onLanded = useCallback(() => {
    // The landed transfer becomes a queued job via FT-2 -> FT-3; refetch the
    // job list so the new row appears from the authoritative source (no
    // fabricated local job is inserted).
    jobsData.reload();
  }, [jobsData]);
  const { uploads, add, pause, resume, cancel } = useUploads(onLanded);

  // ---- selection defaults to the first job once loaded ----
  useEffect(() => {
    if (sel === null && jobsData.jobs.length > 0) setSel(jobsData.jobs[0].id);
  }, [sel, jobsData.jobs]);

  // ---- derived batch aggregates (R3), preferring the stream aggregate ----
  const agg = jobsStream.aggregate;
  const jobs = jobsData.jobs;
  const counts = useMemo(() => {
    const done = jobs.filter((j) => j.state === "completed").length;
    const running = jobs.filter((j) => j.state === "running").length;
    const queued = jobs.filter((j) => j.state === "queued").length;
    const failed = jobs.filter((j) => j.state === "failed").length;
    return { done, running, queued, failed };
  }, [jobs]);

  const farmFps = agg ? agg.farmFps : null;
  const realtimeX = agg ? agg.aggregateSpeedX : null;

  // ---- throughput sparkline ring buffer, fed at stream cadence ----
  const [hist, setHist] = useState<number[]>(() => Array(HIST_LEN).fill(0));
  const lastFpsRef = useRef(0);
  useEffect(() => {
    if (farmFps !== null) lastFpsRef.current = farmFps;
  }, [farmFps]);
  useEffect(() => {
    // Push a sample whenever the aggregate updates. This is the derived farm
    // throughput at the stream's own cadence (no random walk).
    setHist((h) => [...h.slice(1), lastFpsRef.current]);
  }, [agg]);

  const queueRunning = jobsStream.conn === "open";
  const maxConcurrent = agg ? Math.max(1, agg.inFlight + agg.queued) : counts.running + counts.queued || 1;

  // ---- retry ----
  const onRetry = useCallback(
    (id: string) => {
      (async () => {
        try {
          await apiRetryJob(id);
          jobsData.reload();
        } catch {
          // The queue reflects the authoritative state on the next refetch.
          jobsData.reload();
        }
      })();
    },
    [jobsData],
  );

  // ---- add media (real file picker; each file starts a real FT-2 session) ----
  const fileInput = useRef<HTMLInputElement | null>(null);
  const addMedia = () => fileInput.current?.click();
  const onFilesPicked = (files: FileList | null) => {
    const arr = files ? Array.from(files) : [];
    if (arr.length === 0) return;
    add(arr);
    setBottom("xfer");
    setXfer(null);
  };
  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setOver(false);
    const files = Array.from(e.dataTransfer.files ?? []);
    // Real DataTransfer names and sizes only; no sample-title fallback (R10(b)).
    if (files.length === 0) return;
    add(files);
    setBottom("xfer");
    setXfer(null);
  };

  const selectedJob = jobs.find((j) => j.id === sel) ?? null;
  const selectedPreset = selectedJob ? jobsData.presets.find((p) => p.id === selectedJob.presetId) ?? null : null;

  const wsName = session.me?.workspace?.name ?? EM_DASH;
  const activeUploads = uploads.filter((u) => u.state !== "landed" && u.state !== "canceled");

  return (
    <div style={{ position: "absolute", inset: 0, display: "flex", flexDirection: "column", background: "var(--bg-base)", color: "var(--fg1)" }}>
      <input ref={fileInput} type="file" multiple style={{ display: "none" }} onChange={(e) => onFilesPicked(e.target.files)} />

      {/* top bar */}
      <div style={{ height: 56, flex: "none", display: "flex", alignItems: "center", gap: 16, padding: "0 18px", background: "var(--bg-panel)", borderBottom: "1px solid var(--line)" }}>
        <ConsoleCrumb trail={[{ label: "Workspaces" }, { label: wsName, mono: true }, { label: "File transcoder" }]} />
        <div style={{ flex: 1 }} />
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            padding: "6px 11px",
            borderRadius: "var(--r-xs)",
            border: `1px solid ${queueRunning ? "var(--line-strong)" : "var(--line)"}`,
            background: queueRunning ? "var(--blue-tint)" : "transparent",
          }}
        >
          <Dot tone={queueRunning ? "ok" : "idle"} pulse={queueRunning} />
          <span
            style={{
              font: "600 11px var(--font-sans)",
              letterSpacing: ".14em",
              textTransform: "uppercase",
              color: queueRunning ? "#fff" : "var(--fg3)",
              whiteSpace: "nowrap",
            }}
          >
            {/* Queue run state reflects the live FT-4 jobs stream connection:
                "connected" is the honest signal that the farm is reporting. A
                pause control is out of scope (no FT-3 /v1/queue route exists). */}
            {queueRunning ? "Stream live" : "Stream offline"}
          </span>
        </div>
        <span style={{ font: "400 13px var(--font-mono)", color: "#fff", fontVariantNumeric: "tabular-nums" }}>
          {agg ? `${agg.inFlight}/${maxConcurrent}` : `${counts.running}/${maxConcurrent}`} · {agg ? agg.queued : counts.queued} waiting
        </span>
        <div style={{ width: 1, height: 22, background: "var(--line)" }} />
        <button
          className="ae-b"
          onClick={addMedia}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 8,
            minHeight: 38,
            padding: "10px 15px",
            borderRadius: "var(--r-sm)",
            cursor: "pointer",
            font: "var(--t-btn)",
            letterSpacing: "var(--ls-btn)",
            textTransform: "uppercase",
            color: "#fff",
            background: "var(--blue-500)",
            border: "1px solid transparent",
            whiteSpace: "nowrap",
          }}
        >
          <Icon name="upload" size={13} />
          Add media
        </button>
        <div style={{ width: 1, height: 22, background: "var(--line)" }} />
        <UserMenu session={session} />
      </div>

      {/* body */}
      <div style={{ flex: 1, display: "flex", gap: 12, padding: 12, minHeight: 0 }}>
        {/* left: source inspector + hardware */}
        <div style={{ width: 292, flex: "none", display: "flex", flexDirection: "column", gap: 12, minHeight: 0 }}>
          <SourcePanel job={selectedJob} />
          <HardwarePanel hardware={hardware} encodingJobs={counts.running} />
        </div>

        {/* center: batch progress + queue + log */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: 12, minWidth: 0 }}>
          <Panel
            title="Batch progress"
            style={{ flex: "none" }}
            right={<span style={{ font: "400 10px var(--font-mono)", color: "var(--fg3)" }}>{jobs.length} jobs · this batch</span>}
          >
            <div style={{ display: "flex", gap: 26, marginBottom: 11 }}>
              <Read value={String(agg ? agg.completed : counts.done)} unit={`/ ${jobs.length}`} label="Completed" size={22} />
              <Read value={String(agg ? agg.inFlight : counts.running)} label="In flight" size={22} />
              <Read value={String(agg ? agg.queued : counts.queued)} label="Queued" size={22} />
              <Read
                value={String(agg ? agg.failed : counts.failed)}
                label="Failed"
                size={22}
                tone={(agg ? agg.failed : counts.failed) ? "var(--err)" : undefined}
              />
              <Read value={fmtInt(farmFps)} unit="fps" label="Farm throughput" size={22} />
              <Read value={fmtNum(realtimeX, 1)} unit="×" label="Realtime" size={22} />
            </div>
            <Graph data={hist} max={Math.max(900, (farmFps ?? 0) * 1.25)} live={queueRunning} />
          </Panel>

          <div
            style={{ flex: 1, minHeight: 0, display: "flex", position: "relative" }}
            onDragOver={(e) => {
              e.preventDefault();
              setOver(true);
            }}
            onDragLeave={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false);
            }}
            onDrop={onDrop}
          >
            <JobQueue
              jobs={jobs}
              presets={jobsData.presets}
              uploads={activeUploads}
              progress={jobsStream.progress}
              filter={filter}
              onFilter={setFilter}
              sel={sel}
              onSelJob={setSel}
              xfer={xfer}
              onSelXfer={(id) => {
                setBottom("xfer");
                setXfer(id);
              }}
              onRetry={onRetry}
              loading={jobsData.loading}
              errorMessage={jobsData.errorMessage}
            />
            <DropVeil over={over} />
          </div>

          <Panel
            title={bottom === "xfer" ? "Transfer" : "Job log"}
            style={{ height: bottom === "xfer" ? 236 : 148, flex: "none" }}
            bodyStyle={{ padding: 0, flex: 1, minHeight: 0 }}
            right={
              <div style={{ display: "flex", gap: 6 }}>
                <TabChip active={bottom === "xfer"} onClick={() => setBottom("xfer")}>
                  Transfer{activeUploads.length ? ` · ${activeUploads.length}` : ""}
                </TabChip>
                <TabChip active={bottom === "log"} onClick={() => setBottom("log")}>
                  Job log
                </TabChip>
              </div>
            }
          >
            {bottom === "xfer" ? (
              <TransferPanel uploads={uploads} sel={xfer} onSel={setXfer} pause={pause} resume={resume} cancel={cancel} />
            ) : (
              <LogView lines={logs.lines} conn={logs.conn} />
            )}
          </Panel>
        </div>

        {/* right: EVE + output profile + delivery */}
        <div style={{ width: 312, flex: "none", display: "flex", flexDirection: "column", gap: 12, minHeight: 0 }}>
          <EvePanel state={eve} onChange={setEve} />
          <OutputProfilePanel
            preset={selectedPreset}
            eve={eve}
            job={selectedJob}
            onPatched={() => jobsData.reload()}
          />
          <DeliveryPanel live={queueRunning} />
        </div>
      </div>
    </div>
  );
}

function TabChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      className="ae-b"
      onClick={onClick}
      style={{
        font: "600 10px var(--font-sans)",
        letterSpacing: ".09em",
        textTransform: "uppercase",
        padding: "6px 10px",
        borderRadius: "var(--r-xs)",
        cursor: "pointer",
        whiteSpace: "nowrap",
        background: active ? "var(--blue-tint)" : "transparent",
        border: `1px solid ${active ? "var(--blue-500)" : "var(--line-strong)"}`,
        color: active ? "var(--blue-300)" : "var(--fg2)",
      }}
    >
      {children}
    </button>
  );
}

function LogView({ lines, conn }: { lines: { line: string; tag: string; level: string; at: string }[]; conn: string }) {
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);
  if (lines.length === 0) {
    return (
      <div style={{ height: "100%", display: "grid", placeItems: "center", color: "var(--fg4)", font: "var(--t-micro)" }}>
        {conn === "open" ? "No log lines yet" : conn === "error" ? "Log stream unavailable" : "Connecting to log stream..."}
      </div>
    );
  }
  return (
    <div ref={ref} style={{ height: "100%", overflowY: "auto", padding: "8px 13px", display: "flex", flexDirection: "column", gap: 3 }}>
      {lines.map((l, i) => (
        <div key={i} style={{ display: "flex", gap: 10, font: "400 11px var(--font-mono)", color: "var(--fg2)", whiteSpace: "nowrap" }}>
          <span style={{ color: "var(--fg4)" }}>{fmtTimeOfDay(l.at)}</span>
          <span style={{ color: l.level === "warn" || l.level === "error" ? "var(--warn)" : "var(--blue-400)", width: 30 }}>{l.tag}</span>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{l.line}</span>
        </div>
      ))}
    </div>
  );
}
