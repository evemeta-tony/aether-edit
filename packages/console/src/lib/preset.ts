// packages/console/src/lib/preset.ts
//
// Bridges the real FT-3 preset model (services/orchestrator) to the display
// vocabulary the output-profile panel uses, and computes the derived rows
// (R3): keyframe interval, resolution, formats-out, and the estimated output
// size. The prototype's illustrative constants (crf x 0.24, 5.6 EVE) are NOT
// ported; the estimate is computed honestly from the preset's real rate
// parameters and the selected source's probe duration, and is absent (em dash)
// whenever an input it needs is unknown.

import type { Container, Preset, RateControl, VideoCodec } from "../api/jobs";
import { EM_DASH } from "./format";

// Container display labels, upper-cased for the chips (server stores lowercase).
export const CONTAINERS: { id: Container; label: string }[] = [
  { id: "mp4", label: "MP4" },
  { id: "mov", label: "MOV" },
  { id: "hls", label: "HLS" },
  { id: "dash", label: "DASH" },
  { id: "webm", label: "WebM" },
];

export const RATE_CONTROLS: { id: RateControl; label: string }[] = [
  { id: "crf", label: "CRF" },
  { id: "vbr", label: "VBR" },
  { id: "cbr", label: "CBR" },
];

// Encoder speed: the prototype shows fast/medium/slow; the real model is
// p1..p7 (p1 slowest/best, p7 fastest). Map the three visible chips onto three
// representative points so the control stays faithful while writing valid
// values to the service.
export const SPEED_CHIPS: { id: "p6" | "p4" | "p2"; label: string }[] = [
  { id: "p6", label: "fast" },
  { id: "p4", label: "medium" },
  { id: "p2", label: "slow" },
];

export function speedChipFor(speed: string): "p6" | "p4" | "p2" {
  if (speed === "p7" || speed === "p6" || speed === "p5") return "p6";
  if (speed === "p4") return "p4";
  return "p2";
}

// codecLabel renders the codec-neutral name the way the panel reads it.
export function codecLabel(codec: VideoCodec): string {
  switch (codec) {
    case "h264":
      return "H.264";
    case "hevc":
      return "HEVC";
    case "av1":
      return "AV1";
  }
}

// resolutionLabel reads the top rung of the ladder (the largest output), or the
// em dash when the ladder is empty.
export function resolutionLabel(preset: Preset): string {
  if (preset.ladder.length === 0) return EM_DASH;
  const top = preset.ladder.reduce((a, b) => (a.width * a.height >= b.width * b.height ? a : b));
  return `${top.width}x${top.height}`;
}

// keyframeFrames is the keyframe interval in frames. The service stores
// gopLength directly in FRAMES (unlike the prototype's gop-seconds x fps), so
// the derived row reports gopLength verbatim.
export function keyframeLabel(preset: Preset): string {
  return `${preset.gopLength} frames`;
}

// targetKbps returns the effective target bitrate in kbps for size estimation,
// or null when the mode is CRF (quality-targeted, size not directly derivable
// from a bitrate). CRF output size cannot be honestly estimated without an
// encoder model, so it renders as the em dash rather than a fabricated figure.
export function targetKbps(preset: Preset): number | null {
  if (preset.rateControl === "crf") return null;
  return preset.bitrateKbps && preset.bitrateKbps > 0 ? preset.bitrateKbps : null;
}

// estOutputLabel derives the estimated output size (R3) from the preset's real
// bitrate and the source probe duration. Bytes = bitrate(bps) * seconds / 8,
// summed conceptually over the ladder is out of scope without per-rung
// bitrates, so this reports the top-rung target. Returns the em dash when the
// duration or a usable bitrate is unknown.
export function estOutputLabel(preset: Preset, durationSeconds: number | null): string {
  const kbps = targetKbps(preset);
  if (kbps === null || durationSeconds === null || durationSeconds <= 0) return EM_DASH;
  const bytes = (kbps * 1000 * durationSeconds) / 8;
  const gb = bytes / (1024 * 1024 * 1024);
  return `${gb.toFixed(2)} GB`;
}
