// packages/console/src/hooks/useTelemetry.ts
//
// React bindings over the three FT-4 SSE streams. Each hook owns one
// connection for the component tree's lifetime and exposes the latest typed
// state plus a connection status so panels can show honest loading/empty/error
// states (R10) instead of fabricated numbers on a dead stream.

import { useEffect, useState } from "react";
import {
  streamHardware,
  streamJobs,
  streamLogs,
  type HardwareSample,
  type HardwareStatus,
  type JobAggregate,
  type JobStreamEvent,
  type LogEvent,
} from "../api/telemetry";
import type { SseStatus } from "../api/sse";

export interface HardwareState {
  sample: HardwareSample | null;
  status: HardwareStatus | null;
  conn: SseStatus;
}

export function useHardwareStream(): HardwareState {
  const [state, setState] = useState<HardwareState>({ sample: null, status: null, conn: "connecting" });
  useEffect(() => {
    const conn = streamHardware({
      onSample: (sample) => setState((s) => ({ ...s, sample })),
      onStatus: (status) => setState((s) => ({ ...s, status })),
      onConnState: (c) => setState((s) => ({ ...s, conn: c })),
    });
    return () => conn.close();
  }, []);
  return state;
}

export interface JobsStreamState {
  // Per-job latest progress keyed by jobId (running-job telemetry).
  progress: Map<string, JobStreamEvent>;
  aggregate: JobAggregate | null;
  conn: SseStatus;
  // Monotonic transition tick: bumped on every job event so callers can
  // trigger a jobs list refetch on state changes.
  transitionTick: number;
}

export function useJobsStream(): JobsStreamState {
  const [aggregate, setAggregate] = useState<JobAggregate | null>(null);
  const [conn, setConn] = useState<SseStatus>("connecting");
  const [transitionTick, setTick] = useState(0);
  // progress is held in state and replaced with a FRESH Map on every update
  // (never mutated in place). Emitting a new identity keeps it safe to memoize
  // a consumer on `progress`: a stable-identity mutated Map would silently
  // freeze per-row progress under React referential-equality checks.
  const [progress, setProgress] = useState<Map<string, JobStreamEvent>>(new Map());

  useEffect(() => {
    const c = streamJobs({
      onJob: (j) => {
        setProgress((prev) => {
          const next = new Map(prev);
          // A terminal state removes the job from the active progress set.
          if (j.state === "completed" || j.state === "failed") next.delete(j.jobId);
          else next.set(j.jobId, j);
          return next;
        });
        if (j.state === "completed" || j.state === "failed") setTick((t) => t + 1);
      },
      onAggregate: setAggregate,
      onConnState: setConn,
    });
    return () => c.close();
  }, []);

  return { progress, aggregate, conn, transitionTick };
}

export interface LogsState {
  lines: LogEvent[];
  conn: SseStatus;
}

// useLogsStream keeps a bounded tail of log lines for the given tag (undefined
// = all tags). The tag is a dependency so switching tabs re-subscribes with the
// server-side filter.
export function useLogsStream(tag: string | undefined, cap = 200): LogsState {
  const [lines, setLines] = useState<LogEvent[]>([]);
  const [conn, setConn] = useState<SseStatus>("connecting");
  useEffect(() => {
    setLines([]);
    const c = streamLogs(
      {
        onLog: (l) => setLines((prev) => (prev.length >= cap ? [...prev.slice(1), l] : [...prev, l])),
        onConnState: setConn,
      },
      tag,
    );
    return () => c.close();
  }, [tag, cap]);
  return { lines, conn };
}
