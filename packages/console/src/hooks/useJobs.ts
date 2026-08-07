// packages/console/src/hooks/useJobs.ts
//
// Loads the FT-3 job list and presets and keeps them fresh. The job list is
// refetched when the FT-4 jobs stream reports a state transition (transitionTick
// from useJobsStream), so state changes are authoritative from FT-3 while live
// progress (fps/speed/eta/pct) rides the stream. Per-job live progress is merged
// in at render time by the panels, not stored back onto the job rows.

import { useCallback, useEffect, useState } from "react";
import { listJobs, listPresets, type Job, type Preset } from "../api/jobs";

export interface JobsData {
  jobs: Job[];
  presets: Preset[];
  loading: boolean;
  errorMessage: string | null;
  reload: () => void;
}

export function useJobsData(transitionTick: number): JobsData {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMessage, setError] = useState<string | null>(null);
  const [manualTick, setManual] = useState(0);

  const reload = useCallback(() => setManual((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;
    const ctrl = new AbortController();
    (async () => {
      try {
        const [jobsRes, presetsRes] = await Promise.all([
          listJobs(undefined, ctrl.signal),
          listPresets(ctrl.signal),
        ]);
        if (cancelled) return;
        setJobs(jobsRes.jobs);
        setPresets(presetsRes.presets);
        setError(null);
      } catch (err) {
        if (cancelled) return;
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : "job load failed");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
      ctrl.abort();
    };
  }, [transitionTick, manualTick]);

  return { jobs, presets, loading, errorMessage, reload };
}
