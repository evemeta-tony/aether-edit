// packages/console/src/api/telemetry.ts
//
// FT-4 telemetry client. Three bearer-authenticated SSE streams feed every
// live readout. Payload shapes are taken verbatim from services/telemetry
// (API.md). The honest-absence rule is load-bearing here: HardwareSample GPU
// fields are OMITTED (not zero) when there is no usable GPU, and the sticky
// "status" event carries gpu:"unavailable". The UI renders an em-dash for any
// omitted field; it never fabricates a zero.

import { servicePaths } from "./config";
import { connectSse, type SseConnection, type SseStatus } from "./sse";

const base = `${servicePaths.telemetry}/v1/streams`;

// HardwareSample: every GPU field is nullable because it may be absent on a
// GPU-less host. cpuUtilPct is always present (derived from /proc/stat).
export interface HardwareSample {
  gpuUtilPct: number | null;
  vramUsedMB: number | null;
  vramTotalMB: number | null;
  junctionC: number | null;
  powerW: number | null;
  encoderSessions: number | null;
  cpuUtilPct: number | null;
}

export interface HardwareStatus {
  stream: "hardware";
  gpu: "ok" | "unavailable" | "error";
  reason?: string;
}

export interface JobStreamEvent {
  jobId: string;
  state: "queued" | "running" | "completed" | "failed";
  fps: number;
  speedX: number;
  etaSeconds: number;
  progressPct: number;
}

export interface JobAggregate {
  queued: number;
  inFlight: number;
  completed: number;
  failed: number;
  farmFps: number;
  aggregateSpeedX: number;
}

export interface LogEvent {
  line: string;
  tag: string;
  level: "debug" | "info" | "warn" | "error";
  at: string;
}

function num(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

// A HardwareSample where an omitted key becomes null (the honest-absence rule).
function readHardwareSample(raw: Record<string, unknown>): HardwareSample {
  return {
    gpuUtilPct: num(raw.gpuUtilPct),
    vramUsedMB: num(raw.vramUsedMB),
    vramTotalMB: num(raw.vramTotalMB),
    junctionC: num(raw.junctionC),
    powerW: num(raw.powerW),
    encoderSessions: num(raw.encoderSessions),
    cpuUtilPct: num(raw.cpuUtilPct),
  };
}

export interface HardwareHandlers {
  onSample?: (s: HardwareSample) => void;
  onStatus?: (s: HardwareStatus) => void;
  onDropped?: (n: number) => void;
  onConnState?: (s: SseStatus) => void;
}

export function streamHardware(h: HardwareHandlers): SseConnection {
  return connectSse(`${base}/hardware`, {
    onStatus: h.onConnState,
    onEvent: (ev) => {
      const data = safeParse(ev.data);
      if (!data) return;
      switch (ev.event) {
        case "sample":
          h.onSample?.(readHardwareSample(data));
          break;
        case "status":
          h.onStatus?.(data as unknown as HardwareStatus);
          break;
        case "dropped":
          h.onDropped?.(num(data.dropped) ?? 0);
          break;
      }
    },
  });
}

export interface JobsHandlers {
  onJob?: (j: JobStreamEvent) => void;
  onAggregate?: (a: JobAggregate) => void;
  onDropped?: (n: number) => void;
  onConnState?: (s: SseStatus) => void;
}

export function streamJobs(h: JobsHandlers): SseConnection {
  return connectSse(`${base}/jobs`, {
    onStatus: h.onConnState,
    onEvent: (ev) => {
      const data = safeParse(ev.data);
      if (!data) return;
      switch (ev.event) {
        case "job":
          h.onJob?.(data as unknown as JobStreamEvent);
          break;
        case "aggregate":
          h.onAggregate?.(data as unknown as JobAggregate);
          break;
        case "dropped":
          h.onDropped?.(num(data.dropped) ?? 0);
          break;
      }
    },
  });
}

export interface LogsHandlers {
  onLog?: (l: LogEvent) => void;
  onDropped?: (n: number) => void;
  onConnState?: (s: SseStatus) => void;
}

// streamLogs optionally filters to one tag (job | transfer) server side.
export function streamLogs(h: LogsHandlers, tag?: string): SseConnection {
  const url = tag ? `${base}/logs?tag=${encodeURIComponent(tag)}` : `${base}/logs`;
  return connectSse(url, {
    onStatus: h.onConnState,
    onEvent: (ev) => {
      const data = safeParse(ev.data);
      if (!data) return;
      if (ev.event === "log") h.onLog?.(data as unknown as LogEvent);
      else if (ev.event === "dropped") h.onDropped?.(num(data.dropped) ?? 0);
    },
  });
}

function safeParse(s: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(s);
    return v && typeof v === "object" ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}
