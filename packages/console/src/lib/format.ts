// packages/console/src/lib/format.ts
//
// Display helpers carrying the panel-map conventions. The absent-value glyph
// (R1) is the em dash U+2014, produced here from a Unicode escape so no literal
// em dash appears in authored source (convention C4). Every consumer renders
// EM_DASH for a value that does not currently exist, never a fabricated zero.

// U+2014 em dash, built from an escape so no literal dash appears in source.
export const EM_DASH = "\u2014";

// fmtDur formats seconds as HH:MM:SS (zero-padded), matching the prototype.
export function fmtDur(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const hh = String(Math.floor(s / 3600)).padStart(2, "0");
  const mm = String(Math.floor(s / 60) % 60).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return `${hh}:${mm}:${ss}`;
}

// fmtEta formats a positive seconds figure into a compact h/m/s label, or the
// em dash when the figure is absent (null) or non-positive-and-unknown.
export function fmtEta(seconds: number | null): string {
  if (seconds === null || !Number.isFinite(seconds) || seconds < 0) return EM_DASH;
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds / 60) % 60}m`;
  if (seconds >= 60) return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
  return `${Math.max(0, Math.floor(seconds))}s`;
}

const GIB = 1024 * 1024 * 1024;

// bytesToGB converts a byte count to GiB with one decimal.
export function gib(bytes: number): number {
  return bytes / GIB;
}

export function fmtGB(bytes: number, digits = 1): string {
  return `${gib(bytes).toFixed(digits)} GB`;
}

// fmtInt renders an integer with thousands separators, or the em dash for a
// null (absent) value.
export function fmtInt(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return EM_DASH;
  return Math.round(v).toLocaleString();
}

// fmtNum renders a fixed-decimal number, or the em dash for a null value.
export function fmtNum(v: number | null, digits = 1): string {
  if (v === null || !Number.isFinite(v)) return EM_DASH;
  return v.toFixed(digits);
}

// fmtPct renders a whole-percent, or the em dash for a null value.
export function fmtPct(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return EM_DASH;
  return `${Math.round(v)}%`;
}

// fmtTimeOfDay renders an RFC3339 timestamp as HH:MM:SS local time. Falls back
// to the em dash if the timestamp does not parse.
export function fmtTimeOfDay(rfc3339: string): string {
  const d = new Date(rfc3339);
  if (Number.isNaN(d.getTime())) return EM_DASH;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}
