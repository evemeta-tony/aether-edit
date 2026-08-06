// packages/console/src/api/upload.ts
//
// FT-2 upload client: the raw session HTTP surface the resumable transfer
// engine drives. Shapes mirror services/upload/server.go exactly:
//   POST   /v1/uploads                      -> {uploadId, chunkSizeBytes, chunkCount}
//   GET    /v1/uploads/{id}                 -> session state incl. server chunk map
//   PUT    /v1/uploads/{id}/chunks/{n}      -> chunk write (exact Content-Length +
//                                              X-Chunk-Sha256; 429 + Retry-After on
//                                              backpressure)
//   POST   /v1/uploads/{id}/complete        -> assembly + landed-object publish
//   DELETE /v1/uploads/{id}                 -> cancel
//
// The server does not advertise maxParallelStreams; the engine sets its own
// parallelism (8, matching the prototype). chunkSizeBytes and chunkCount come
// from the create response and are the copy source for the empty-state text
// (never hardcoded client side, per panel map section 5).

import { servicePaths } from "./config";
import { ApiError, request } from "./http";

export type ChunkState = "PENDING" | "INFLIGHT" | "DONE" | "RETRY";
export type SessionState = "ACTIVE" | "ASSEMBLED" | "COMPLETED" | "CANCELLED";

export interface CreateUploadResponse {
  uploadId: string;
  chunkSizeBytes: number;
  chunkCount: number;
}

export interface ChunkStatus {
  index: number;
  state: ChunkState;
}

export interface UploadSession {
  uploadId: string;
  filename: string;
  sizeBytes: number;
  mime: string;
  chunkSizeBytes: number;
  chunkCount: number;
  state: SessionState;
  doneChunks: number;
  chunks: ChunkStatus[];
  sha256?: string;
  objectKey?: string;
}

export interface CompleteResponse {
  uploadId: string;
  objectKey: string;
  sha256: string;
  sizeBytes: number;
  state: SessionState;
}

const u = servicePaths.upload;

export function createUpload(
  filename: string,
  sizeBytes: number,
  mime: string,
): Promise<CreateUploadResponse> {
  return request<CreateUploadResponse>(`${u}/v1/uploads`, {
    method: "POST",
    json: { filename, sizeBytes, mime },
  });
}

export function getUpload(id: string, signal?: AbortSignal): Promise<UploadSession> {
  return request<UploadSession>(`${u}/v1/uploads/${encodeURIComponent(id)}`, { signal });
}

// putChunk writes one chunk. The body is the raw bytes; sha256Hex is the
// lowercase hex sha256 of exactly those bytes (the server recomputes and
// rejects a mismatch). Returns { retryAfterMs } on a 429 backpressure signal
// so the engine can honor Retry-After instead of hammering.
export interface PutChunkResult {
  ok: boolean;
  saturated: boolean;
  retryAfterMs: number | null;
}

export async function putChunk(
  id: string,
  index: number,
  body: ArrayBuffer,
  sha256Hex: string,
  signal?: AbortSignal,
): Promise<PutChunkResult> {
  try {
    await request<unknown>(`${u}/v1/uploads/${encodeURIComponent(id)}/chunks/${index}`, {
      method: "PUT",
      body,
      headers: {
        "Content-Type": "application/octet-stream",
        "Content-Length": String(body.byteLength),
        "X-Chunk-Sha256": sha256Hex,
      },
      signal,
    });
    return { ok: true, saturated: false, retryAfterMs: null };
  } catch (err) {
    if (err instanceof ApiError && err.status === 429) {
      return { ok: false, saturated: true, retryAfterMs: err.retryAfterMs };
    }
    throw err;
  }
}

export function completeUpload(id: string): Promise<CompleteResponse> {
  return request<CompleteResponse>(`${u}/v1/uploads/${encodeURIComponent(id)}/complete`, {
    method: "POST",
  });
}

export function cancelUpload(id: string): Promise<unknown> {
  return request<unknown>(`${u}/v1/uploads/${encodeURIComponent(id)}`, { method: "DELETE" });
}
