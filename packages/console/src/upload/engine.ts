// packages/console/src/upload/engine.ts
//
// The real resumable, chunked, multi-threaded upload engine (FT-2). This is
// the production counterpart to the prototype's Uploader.jsx simulation, and
// the simulation is gone: there is no tick loop, no random chunk resolution,
// no fabricated throughput. The engine drives the actual FT-2 session API and
// mirrors the SERVER chunk map as the source of truth.
//
// State machine, per the panel map and Uploader.jsx:
//   1. POST /v1/uploads -> {uploadId, chunkSizeBytes, chunkCount}
//   2. Up to MAX_PARALLEL workers each claim a PENDING chunk, slice those bytes
//      from the File, sha256 them, and PUT them. The server verifies the sha256
//      and records DONE; a failed/corrupt write is retried (bounded).
//   3. Backpressure: a 429 with Retry-After parks the whole engine for the
//      hinted delay (honors the service's inflight-byte ceiling).
//   4. Pause reverts in-flight chunks to pending locally (no server call; the
//      server map is authoritative for DONE cells only). Resume re-fetches the
//      server chunk map (GET /v1/uploads/{id}) and continues, which is what
//      makes a reconnect resume from the map rather than byte zero.
//   5. When every chunk is DONE, POST .../complete verifies and publishes the
//      landed-object event; the session moves to "landed" (COMPLETED).
//
// Throughput is honest (R6): measured from bytes actually acknowledged on the
// wire over a short trailing window, never inferred from UI paint cadence.

import {
  cancelUpload,
  completeUpload,
  createUpload,
  getUpload,
  putChunk,
  type ChunkState,
} from "../api/upload";
import { ApiError } from "../api/http";
import { sha256Hex } from "./sha256";

export const MAX_PARALLEL = 8; // parallel transfer streams (matches the prototype)
const MAX_CHUNK_ATTEMPTS = 5; // per-chunk retry ceiling before the session errors
const RATE_WINDOW_MS = 3000; // trailing window for measured throughput

// EngineState mirrors the prototype's U_STATE set, mapped to real lifecycle:
//   uploading  -> actively moving chunks
//   paused     -> operator paused; in-flight chunks reverted to pending
//   error      -> transport error; auto-resume from the server map is armed
//   verifying  -> all chunks DONE; POST complete in flight (assembly + verify)
//   landed     -> COMPLETED; landed-object event published
//   canceled   -> DELETEd
export type EngineState = "uploading" | "paused" | "error" | "verifying" | "landed" | "canceled";

// PerStream is one transfer worker's live view for the UI (which chunk it is
// moving, or null when idle).
export interface UploadView {
  id: string; // local id (stable across the session lifetime)
  uploadId: string | null; // server session id, once created
  file: string;
  sizeBytes: number;
  state: EngineState;
  chunkSizeBytes: number;
  chunkStates: ChunkState[]; // full server-truth map merged with local in-flight
  doneChunks: number;
  chunkCount: number;
  streams: (number | null)[]; // per-worker current chunk index (MAX_PARALLEL slots)
  liveStreams: number;
  bytesMoved: number;
  throughputBytesPerSec: number; // measured wire rate (R6)
  retries: number;
  etaSeconds: number | null; // derived from remaining bytes / measured rate
  errorMessage: string | null;
  objectKey: string | null;
  sha256: string | null;
}

type Listener = (view: UploadView) => void;
type LandedListener = (view: UploadView) => void;

interface RateSample {
  at: number;
  bytes: number;
}

// UploadTask owns one file's transfer.
export class UploadTask {
  readonly id: string;
  readonly file: File;
  private uploadId: string | null = null;
  private chunkSizeBytes = 0;
  private chunkCount = 0;
  private chunkStates: ChunkState[] = [];
  private streams: (number | null)[] = Array(MAX_PARALLEL).fill(null);
  private state: EngineState = "uploading";
  private retries = 0;
  private errorMessage: string | null = null;
  private objectKey: string | null = null;
  private sha256: string | null = null;
  private rateSamples: RateSample[] = [];
  private ackedBytes = 0;
  private parkedUntil = 0; // backpressure: do not claim before this timestamp
  private attempts = new Map<number, number>();
  private abort = new AbortController();
  private listener: Listener;
  private onLanded: LandedListener;
  private disposed = false;

  constructor(file: File, listener: Listener, onLanded: LandedListener) {
    this.id = `u_${Math.random().toString(36).slice(2, 10)}`;
    this.file = file;
    this.listener = listener;
    this.onLanded = onLanded;
  }

  view(): UploadView {
    const done = this.chunkStates.filter((c) => c === "DONE").length;
    const bytesMoved = this.doneBytes();
    const rate = this.measuredRate();
    const remaining = this.sizeBytes() - bytesMoved;
    const eta = rate > 0 && remaining > 0 ? remaining / rate : null;
    return {
      id: this.id,
      uploadId: this.uploadId,
      file: this.file.name,
      sizeBytes: this.sizeBytes(),
      state: this.state,
      chunkSizeBytes: this.chunkSizeBytes,
      chunkStates: this.chunkStates.slice(),
      doneChunks: done,
      chunkCount: this.chunkCount,
      streams: this.streams.slice(),
      liveStreams: this.streams.filter((s) => s !== null).length,
      bytesMoved,
      throughputBytesPerSec: rate,
      retries: this.retries,
      etaSeconds: eta,
      errorMessage: this.errorMessage,
      objectKey: this.objectKey,
      sha256: this.sha256,
    };
  }

  private sizeBytes(): number {
    return this.file.size;
  }

  private chunkLen(index: number): number {
    if (index === this.chunkCount - 1) {
      return this.sizeBytes() - this.chunkSizeBytes * (this.chunkCount - 1);
    }
    return this.chunkSizeBytes;
  }

  private doneBytes(): number {
    let total = 0;
    for (let i = 0; i < this.chunkStates.length; i++) {
      if (this.chunkStates[i] === "DONE") total += this.chunkLen(i);
    }
    return total;
  }

  private measuredRate(): number {
    const now = Date.now();
    const cutoff = now - RATE_WINDOW_MS;
    this.rateSamples = this.rateSamples.filter((s) => s.at >= cutoff);
    if (this.rateSamples.length === 0) return 0;
    const bytes = this.rateSamples.reduce((n, s) => n + s.bytes, 0);
    const span = Math.max(1, now - Math.min(...this.rateSamples.map((s) => s.at)));
    return (bytes / span) * 1000;
  }

  private emit() {
    if (!this.disposed) this.listener(this.view());
  }

  // start creates the server session and begins moving chunks.
  async start(): Promise<void> {
    try {
      const mime = this.file.type || "application/octet-stream";
      const created = await createUpload(this.file.name, this.file.size, mime);
      // Validate the create response at the boundary (S1): chunkSizeBytes and
      // chunkCount are coerced straight into slice/offset arithmetic, so a
      // zero/negative chunk size or a chunkCount inconsistent with the file
      // size would make chunkLen() go negative and produce empty/backwards
      // slices that hash to the empty buffer and loop until the retry ceiling.
      // Reject an incoherent session up front rather than silently corrupting.
      if (
        !Number.isInteger(created.chunkSizeBytes) ||
        created.chunkSizeBytes <= 0 ||
        !Number.isInteger(created.chunkCount) ||
        created.chunkCount <= 0 ||
        Math.ceil(this.file.size / created.chunkSizeBytes) !== created.chunkCount
      ) {
        this.fail(
          `upload session returned an inconsistent chunk map ` +
            `(size=${this.file.size}, chunkSizeBytes=${created.chunkSizeBytes}, ` +
            `chunkCount=${created.chunkCount})`,
        );
        return;
      }
      this.uploadId = created.uploadId;
      this.chunkSizeBytes = created.chunkSizeBytes;
      this.chunkCount = created.chunkCount;
      this.chunkStates = Array(this.chunkCount).fill("PENDING");
      this.emit();
      this.pump();
    } catch (err) {
      this.fail(errMessage(err));
    }
  }

  // pump keeps MAX_PARALLEL workers busy until the session is done, paused,
  // errored, or parked for backpressure.
  private pump(): void {
    if (this.state !== "uploading" || this.disposed) return;
    if (Date.now() < this.parkedUntil) {
      const wait = this.parkedUntil - Date.now();
      setTimeout(() => this.pump(), wait);
      return;
    }
    for (let s = 0; s < MAX_PARALLEL; s++) {
      if (this.streams[s] !== null) continue;
      const idx = this.claimNext();
      if (idx < 0) break;
      this.streams[s] = idx;
      this.chunkStates[idx] = "INFLIGHT";
      void this.moveChunk(s, idx);
    }
    this.emit();
    if (this.streams.every((x) => x === null) && this.allDone()) {
      void this.finish();
    }
  }

  private claimNext(): number {
    for (let i = 0; i < this.chunkStates.length; i++) {
      if (this.chunkStates[i] === "PENDING" || this.chunkStates[i] === "RETRY") return i;
    }
    return -1;
  }

  private allDone(): boolean {
    return this.chunkCount > 0 && this.chunkStates.every((c) => c === "DONE");
  }

  private async moveChunk(slot: number, index: number): Promise<void> {
    const start = index * this.chunkSizeBytes;
    const end = start + this.chunkLen(index);
    try {
      const buf = await this.file.slice(start, end).arrayBuffer();
      const hash = await sha256Hex(buf);
      const res = await putChunk(this.uploadId as string, index, buf, hash, this.abort.signal);
      if (res.saturated) {
        // Backpressure: park the whole engine and requeue this chunk.
        this.streams[slot] = null;
        this.chunkStates[index] = "PENDING";
        this.parkedUntil = Date.now() + (res.retryAfterMs ?? 1000);
        this.pump();
        return;
      }
      this.chunkStates[index] = "DONE";
      this.streams[slot] = null;
      this.ackedBytes += end - start;
      this.rateSamples.push({ at: Date.now(), bytes: end - start });
      this.pump();
    } catch (err) {
      if (this.abort.signal.aborted) return;
      this.streams[slot] = null;
      const n = (this.attempts.get(index) ?? 0) + 1;
      this.attempts.set(index, n);
      this.retries += 1;
      if (n >= MAX_CHUNK_ATTEMPTS) {
        // A repeatedly failing chunk on a live transport is a hard error; a
        // transport drop (network error, not an HTTP status) arms auto-resume.
        if (isTransportError(err)) {
          this.enterErrorAndAutoResume(errMessage(err));
        } else {
          this.fail(`chunk ${index}: ${errMessage(err)}`);
        }
        return;
      }
      this.chunkStates[index] = "RETRY";
      this.pump();
    }
  }

  // finish runs POST complete: verify + landed publish, then move to landed.
  private async finish(): Promise<void> {
    if (this.state === "verifying" || this.state === "landed") return;
    // INVARIANT (double-complete guard): the guard check above and this
    // assignment must stay in the same synchronous turn. On a single JS thread
    // no second finish() can slip between them, so finish() is single-entry.
    // Do NOT insert an `await` before this line -- doing so reopens a
    // double-complete race under overlapping worker returns (Argus S-F10).
    this.state = "verifying";
    this.emit();
    try {
      const done = await completeUpload(this.uploadId as string);
      this.objectKey = done.objectKey;
      this.sha256 = done.sha256;
      this.state = "landed";
      this.emit();
      this.onLanded(this.view());
    } catch (err) {
      // A completion conflict (missing chunks) means the server map disagrees;
      // re-sync from the server truth and keep moving rather than fabricating.
      if (err instanceof ApiError && err.status === 409) {
        await this.resyncFromServer();
        this.state = "uploading";
        this.emit();
        this.pump();
        return;
      }
      this.fail(errMessage(err));
    }
  }

  // enterErrorAndAutoResume mirrors the panel-map recovery path: on a transport
  // drop, the session shows "link lost, resuming" and auto-resumes from the
  // server chunk map. There is no deliberate link-drop control (R10(b)).
  private enterErrorAndAutoResume(message: string): void {
    this.state = "error";
    this.errorMessage = message;
    this.revertInflight();
    // Clear the trailing rate window so the parked engine does not keep showing
    // a stale non-zero throughput until the samples age out (matches pause();
    // Argus S-F11).
    this.rateSamples = [];
    this.emit();
    setTimeout(() => {
      if (this.state === "error" && !this.disposed) void this.resume();
    }, 1500);
  }

  private revertInflight(): void {
    this.streams = Array(MAX_PARALLEL).fill(null);
    this.chunkStates = this.chunkStates.map((c) => (c === "INFLIGHT" ? "PENDING" : c));
  }

  // resyncFromServer re-fetches the authoritative server chunk map and adopts
  // its DONE cells, so a resume (or a completion conflict) continues from the
  // map. Survives process restart because the map is server-held.
  private async resyncFromServer(): Promise<void> {
    if (!this.uploadId) return;
    const sess = await getUpload(this.uploadId, this.abort.signal);
    this.chunkSizeBytes = sess.chunkSizeBytes;
    this.chunkCount = sess.chunkCount;
    const next: ChunkState[] = Array(this.chunkCount).fill("PENDING");
    for (const c of sess.chunks) {
      // Trust the server only for DONE; PENDING/RETRY are reclaimable locally.
      next[c.index] = c.state === "DONE" ? "DONE" : "PENDING";
    }
    this.chunkStates = next;
    this.streams = Array(MAX_PARALLEL).fill(null);
    if (sess.objectKey) this.objectKey = sess.objectKey;
    if (sess.sha256) this.sha256 = sess.sha256;
  }

  private fail(message: string): void {
    this.state = "error";
    this.errorMessage = message;
    this.revertInflight();
    this.emit();
  }

  pause(): void {
    if (this.state !== "uploading") return;
    this.state = "paused";
    this.abort.abort();
    this.abort = new AbortController();
    this.revertInflight();
    this.rateSamples = [];
    this.emit();
  }

  async resume(): Promise<void> {
    if (this.state !== "paused" && this.state !== "error") return;
    this.errorMessage = null;
    try {
      await this.resyncFromServer();
      this.state = "uploading";
      this.emit();
      this.pump();
    } catch (err) {
      this.fail(errMessage(err));
    }
  }

  async cancel(): Promise<void> {
    this.abort.abort();
    this.state = "canceled";
    this.disposed = true;
    if (this.uploadId) {
      try {
        await cancelUpload(this.uploadId);
      } catch {
        // Best effort: the session may already be gone. The UI drops the row.
      }
    }
  }

  dispose(): void {
    this.disposed = true;
    this.abort.abort();
  }
}

function isTransportError(err: unknown): boolean {
  // An ApiError carries an HTTP status (the server answered); anything else
  // (TypeError from fetch, network failure) is a transport drop.
  return !(err instanceof ApiError);
}

function errMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "transfer failed";
}
