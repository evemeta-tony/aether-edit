// packages/console/src/components/panels/HardwarePanel.tsx
//
// Transcode host telemetry. The prototype rendered a four-worker farm with
// index-based fake utilisation; that index mapping is a known simplification
// the panel map forbids reproducing, and there is no FT-3 /v1/workers route.
// So this panel binds to the one real source that exists: the FT-4 hardware
// stream (GET /v1/streams/hardware), which reports the host GPU at device index
// 0. Honest absence is load-bearing (R10): when the sticky status event says
// gpu:"unavailable", every GPU readout renders the em dash, never a fabricated
// zero. cpuUtilPct is always present and shown.

import { Panel, Read, Dot, Meter } from "../Parts";
import { EM_DASH, fmtInt, fmtNum } from "../../lib/format";
import type { HardwareState } from "../../hooks/useTelemetry";

export function HardwarePanel({ hardware, encodingJobs }: { hardware: HardwareState; encodingJobs: number }) {
  const s = hardware.sample;
  const gpuOk = hardware.status?.gpu === "ok";
  const gpuUnavailable = hardware.status?.gpu === "unavailable" || hardware.status?.gpu === "error";

  const util = s?.gpuUtilPct ?? null;
  const vramUsed = s?.vramUsedMB ?? null;
  const vramTotal = s?.vramTotalMB ?? null;
  const vramPct = vramUsed !== null && vramTotal ? (vramUsed / vramTotal) * 100 : 0;

  const statusLabel =
    hardware.conn !== "open"
      ? hardware.conn === "error"
        ? "stream offline"
        : "connecting"
      : gpuUnavailable
        ? hardware.status?.reason || "no gpu"
        : gpuOk
          ? "gpu online"
          : "waiting";

  return (
    <Panel
      title="Transcode host"
      style={{ flex: "none" }}
      right={
        <span style={{ display: "flex", alignItems: "center", gap: 6, font: "400 10px var(--font-mono)", color: "var(--fg3)", whiteSpace: "nowrap" }}>
          <Dot tone={gpuOk ? "ok" : gpuUnavailable ? "idle" : "warn"} pulse={gpuOk && util !== null && util > 0} />
          {statusLabel}
        </span>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <div style={{ display: "flex", gap: 18 }}>
          <Read value={fmtInt(util === null ? null : util)} unit="%" label="GPU load" size={19} />
          <Read value={fmtInt(s?.encoderSessions ?? null)} label="Enc sessions" size={19} />
          <Read value={String(encodingJobs)} label="Encoding" size={19} />
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
            <span style={{ font: "500 10px var(--font-sans)", letterSpacing: ".1em", textTransform: "uppercase", color: "var(--fg3)" }}>
              VRAM
            </span>
            <span style={{ font: "400 11px var(--font-mono)", color: "var(--fg2)", marginLeft: "auto", fontVariantNumeric: "tabular-nums" }}>
              {vramUsed === null || vramTotal === null
                ? EM_DASH
                : `${(vramUsed / 1024).toFixed(1)} / ${(vramTotal / 1024).toFixed(1)} GB`}
            </span>
          </div>
          <Meter pct={vramPct} color={gpuOk ? "var(--viz-2)" : "var(--idle)"} />
        </div>
        <div style={{ display: "flex", gap: 18 }}>
          <Read value={fmtNum(s?.junctionC ?? null, 0)} unit="°C" label="Junction" size={17} />
          <Read value={fmtNum(s?.powerW ?? null, 0)} unit="W" label="Power" size={17} />
          <Read value={fmtNum(s?.cpuUtilPct ?? null, 0)} unit="%" label="CPU" size={17} />
        </div>
      </div>
    </Panel>
  );
}
