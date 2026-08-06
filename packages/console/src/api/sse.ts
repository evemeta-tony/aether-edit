// packages/console/src/api/sse.ts
//
// A bearer-authenticated Server-Sent Events reader. The browser EventSource
// cannot attach an Authorization header, and the FT-4 telemetry streams all
// require Bearer auth, so we read the text/event-stream body with fetch and
// parse SSE frames ourselves. This also lets us:
//   - refresh the FT-6a access token and reconnect on a 401
//   - reconnect with backoff on transport error
//   - surface open/error/dropped state to the UI for honest loading/empty
//     states (R10): the caller never sees fabricated data on a dead stream.
//
// SSE framing per the spec: records are separated by a blank line; within a
// record, lines starting "event:" set the event name (default "message"),
// lines starting "data:" accumulate the payload, and ": " lines are comments
// (the telemetry service uses ": connected" and ": hb" heartbeats).

import { getAccessToken } from "./session";
import { accessTokenExpired, fireUnauthorized } from "./session";
import { refreshAccessToken } from "./http";

export interface SseEvent {
  event: string;
  data: string;
}

export type SseStatus = "connecting" | "open" | "error" | "closed";

export interface SseHandlers {
  onEvent: (ev: SseEvent) => void;
  onStatus?: (status: SseStatus) => void;
}

export interface SseConnection {
  close: () => void;
}

// connectSse opens url and delivers parsed events until close() is called.
// It reconnects on error with capped exponential backoff. The server replays
// its sticky events (status/aggregate) on every reconnect, so the client does
// not need to persist across reconnects.
export function connectSse(url: string, handlers: SseHandlers): SseConnection {
  let closed = false;
  let controller: AbortController | null = null;
  let attempt = 0;

  const setStatus = (s: SseStatus) => {
    if (!closed && handlers.onStatus) handlers.onStatus(s);
  };

  const backoffMs = () => Math.min(15000, 500 * 2 ** Math.min(attempt, 5));

  const run = async () => {
    while (!closed) {
      controller = new AbortController();
      setStatus("connecting");
      try {
        if (accessTokenExpired()) {
          await refreshAccessToken();
        }
        const token = getAccessToken();
        const res = await fetch(url, {
          headers: {
            Accept: "text/event-stream",
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
          },
          credentials: "include",
          signal: controller.signal,
        });
        if (res.status === 401) {
          const refreshed = await refreshAccessToken();
          if (!refreshed) {
            // Terminal: refresh failed, so the session is unauthenticated and
            // the loop is dead. Emit the error, fire the global unauthorized
            // handler, then mark the connection closed so a later close() is a
            // correct no-op rather than one that flips an already-dead loop.
            fireUnauthorized();
            setStatus("error");
            closed = true;
            return;
          }
          attempt += 1;
          await sleep(backoffMs(), controller.signal);
          continue;
        }
        if (!res.ok || !res.body) {
          setStatus("error");
          attempt += 1;
          await sleep(backoffMs(), controller.signal);
          continue;
        }
        attempt = 0;
        setStatus("open");
        await readStream(res.body, handlers, () => closed);
        // The stream ended without an error (server closed); reconnect.
        if (!closed) {
          attempt += 1;
          await sleep(backoffMs(), controller.signal);
        }
      } catch (err) {
        if (closed || (err instanceof DOMException && err.name === "AbortError")) return;
        setStatus("error");
        attempt += 1;
        try {
          await sleep(backoffMs(), controller?.signal);
        } catch {
          return;
        }
      }
    }
  };

  void run();

  return {
    close: () => {
      closed = true;
      setStatus("closed");
      if (controller) controller.abort();
    },
  };
}

async function readStream(
  body: ReadableStream<Uint8Array>,
  handlers: SseHandlers,
  isClosed: () => boolean,
): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (!isClosed()) {
    const { value, done } = await reader.read();
    if (done) return;
    buffer += decoder.decode(value, { stream: true });
    // Records are separated by a blank line (\n\n). Handle CRLF defensively.
    let sep: number;
    while ((sep = indexOfRecordEnd(buffer)) >= 0) {
      const raw = buffer.slice(0, sep);
      buffer = buffer.slice(sep).replace(/^(\r?\n){1,2}/, "");
      const parsed = parseRecord(raw);
      if (parsed) handlers.onEvent(parsed);
    }
  }
}

function indexOfRecordEnd(buffer: string): number {
  const lf = buffer.indexOf("\n\n");
  const crlf = buffer.indexOf("\r\n\r\n");
  if (lf < 0) return crlf;
  if (crlf < 0) return lf;
  return Math.min(lf, crlf);
}

function parseRecord(raw: string): SseEvent | null {
  let event = "message";
  const dataLines: string[] = [];
  for (const lineRaw of raw.split("\n")) {
    const line = lineRaw.replace(/\r$/, "");
    if (line === "" || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let val = colon < 0 ? "" : line.slice(colon + 1);
    if (val.startsWith(" ")) val = val.slice(1);
    if (field === "event") event = val;
    else if (field === "data") dataLines.push(val);
  }
  if (dataLines.length === 0) return null;
  return { event, data: dataLines.join("\n") };
}

function sleep(ms: number, signal?: AbortSignal | null): Promise<void> {
  return new Promise((resolve, reject) => {
    const id = setTimeout(resolve, ms);
    if (signal) {
      signal.addEventListener(
        "abort",
        () => {
          clearTimeout(id);
          reject(new DOMException("aborted", "AbortError"));
        },
        { once: true },
      );
    }
  });
}
