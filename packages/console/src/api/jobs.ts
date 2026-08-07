// packages/console/src/api/jobs.ts
//
// FT-3 orchestrator client: jobs, presets. Types mirror
// services/orchestrator/internal/jobs (Job, Preset, Rung) and the httpapi
// request bodies exactly. Note the real preset model differs from the
// prototype's illustrative one: container/videoCodec/rateControl are lowercase
// enums, gopLength is in FRAMES, speedPreset is p1..p7, and the ladder is a
// list of rungs. The console maps the prototype's controls onto these.

import { servicePaths } from "./config";
import { request } from "./http";

export type JobState = "queued" | "running" | "completed" | "failed";
export type ErrorClass = "validation" | "asset" | "decode" | "encode" | "internal";

export interface OutputProgress {
  name: string;
  objectKey?: string;
  state: string;
  progressPct: number;
}

export interface Job {
  id: string;
  workspaceId: string;
  userId: string;
  presetId: string;
  sourceObjectKey: string;
  sourceSha256: string;
  state: JobState;
  errorClass?: ErrorClass;
  errorMessage?: string;
  attempts: number;
  progressPct: number;
  fps: number;
  speedX: number;
  etaSeconds: number;
  outputs: OutputProgress[];
  createdAt: string;
  queuedAt: string;
  startedAt?: string;
  finishedAt?: string;
  updatedAt: string;
}

export type Container = "mp4" | "mov" | "hls" | "dash" | "webm";
export type VideoCodec = "h264" | "hevc" | "av1";
export type RateControl = "crf" | "vbr" | "cbr";
export type SpeedPreset = "p1" | "p2" | "p3" | "p4" | "p5" | "p6" | "p7";

export interface Rung {
  name: string;
  width: number;
  height: number;
}

export interface Preset {
  id: string;
  workspaceId: string;
  name: string;
  container: Container;
  videoCodec: VideoCodec;
  rateControl: RateControl;
  crf?: number;
  bitrateKbps?: number;
  maxBitrateKbps?: number;
  gopLength: number;
  speedPreset: SpeedPreset;
  ladder: Rung[];
  createdAt: string;
  updatedAt: string;
}

// PresetPatch carries only the fields being changed (PATCH semantics). The
// server validates the merged result, so a rateControl change must arrive with
// its consistent value fields.
export interface PresetPatch {
  name?: string;
  container?: Container;
  videoCodec?: VideoCodec;
  rateControl?: RateControl;
  crf?: number;
  bitrateKbps?: number;
  maxBitrateKbps?: number;
  gopLength?: number;
  speedPreset?: SpeedPreset;
  ladder?: Rung[];
}

export interface CreateJobRequest {
  objectKey: string;
  presetId: string;
}

const j = servicePaths.jobs;

export function listJobs(state?: JobState, signal?: AbortSignal): Promise<{ jobs: Job[] }> {
  const q = state ? `?state=${encodeURIComponent(state)}` : "";
  return request<{ jobs: Job[] }>(`${j}/v1/jobs${q}`, { signal });
}

export function getJob(id: string, signal?: AbortSignal): Promise<Job> {
  return request<Job>(`${j}/v1/jobs/${encodeURIComponent(id)}`, { signal });
}

export function createJob(req: CreateJobRequest): Promise<Job> {
  return request<Job>(`${j}/v1/jobs`, { method: "POST", json: req });
}

export function retryJob(id: string): Promise<Job> {
  return request<Job>(`${j}/v1/jobs/${encodeURIComponent(id)}/retry`, { method: "POST" });
}

export function cancelJob(id: string): Promise<unknown> {
  return request<unknown>(`${j}/v1/jobs/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function listPresets(signal?: AbortSignal): Promise<{ presets: Preset[] }> {
  return request<{ presets: Preset[] }>(`${j}/v1/presets`, { signal });
}

export function getPreset(id: string, signal?: AbortSignal): Promise<Preset> {
  return request<Preset>(`${j}/v1/presets/${encodeURIComponent(id)}`, { signal });
}

export function patchPreset(id: string, patch: PresetPatch): Promise<Preset> {
  return request<Preset>(`${j}/v1/presets/${encodeURIComponent(id)}`, {
    method: "PATCH",
    json: patch,
  });
}
