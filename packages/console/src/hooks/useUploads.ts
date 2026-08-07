// packages/console/src/hooks/useUploads.ts
//
// React binding over the real upload engine. Replaces the prototype's
// useUploads simulation: add(files) starts real FT-2 sessions, and the hook
// re-renders on every engine emit. onLanded fires once per session when
// POST .../complete has returned 200 (the landed-object event is published).
// That event registers the SOURCE in FT-3; it does not create a job. The
// caller therefore does not fabricate a queued job -- it refetches the real
// job list and leaves the transfer in the honest "landed / source registered"
// state until an explicit job is created.

import { useCallback, useEffect, useRef, useState } from "react";
import { UploadTask, type UploadView } from "../upload/engine";

export interface UseUploads {
  uploads: UploadView[];
  add: (files: File[]) => void;
  pause: (id: string) => void;
  resume: (id: string) => void;
  cancel: (id: string) => void;
}

export function useUploads(onLanded: (view: UploadView) => void): UseUploads {
  const [uploads, setUploads] = useState<UploadView[]>([]);
  const tasks = useRef<Map<string, UploadTask>>(new Map());
  const landedRef = useRef(onLanded);
  landedRef.current = onLanded;

  // views mirrors the current per-task view; kept in a ref so the emit
  // callback can rebuild the array without stale closures.
  const views = useRef<Map<string, UploadView>>(new Map());

  const publish = useCallback(() => {
    setUploads(Array.from(views.current.values()));
  }, []);

  const add = useCallback(
    (files: File[]) => {
      for (const file of files) {
        const task = new UploadTask(
          file,
          (view) => {
            views.current.set(view.id, view);
            publish();
          },
          (view) => {
            landedRef.current(view);
          },
        );
        tasks.current.set(task.id, task);
        views.current.set(task.id, task.view());
        void task.start();
      }
      publish();
    },
    [publish],
  );

  const pause = useCallback((id: string) => {
    tasks.current.get(id)?.pause();
  }, []);

  const resume = useCallback((id: string) => {
    void tasks.current.get(id)?.resume();
  }, []);

  const cancel = useCallback(
    (id: string) => {
      const task = tasks.current.get(id);
      if (!task) return;
      void task.cancel();
      tasks.current.delete(id);
      views.current.delete(id);
      publish();
    },
    [publish],
  );

  useEffect(() => {
    const map = tasks.current;
    return () => {
      for (const t of map.values()) t.dispose();
    };
  }, []);

  return { uploads, add, pause, resume, cancel };
}
