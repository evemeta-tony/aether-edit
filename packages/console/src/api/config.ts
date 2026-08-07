// packages/console/src/api/config.ts
//
// API base configuration. Every service lives under a single configurable
// base (VITE_API_BASE, default the relative "/api") with a per-service path
// prefix, matching the panel map's named sources:
//   FT-6a tenancy   -> /api/tenancy
//   FT-2  upload     -> /api/upload
//   FT-3  jobs       -> /api/jobs
//   FT-4  telemetry  -> /api/telemetry
// Keeping the base relative by default lets the console be served from the
// same origin as an edge that fans out to the four services.

const rawBase = (import.meta.env.VITE_API_BASE as string | undefined) ?? "/api";

// Normalize away any trailing slash so joins are unambiguous.
const base = rawBase.replace(/\/+$/, "");

export const API_BASE = base;

export const servicePaths = {
  tenancy: `${base}/tenancy`,
  upload: `${base}/upload`,
  jobs: `${base}/jobs`,
  telemetry: `${base}/telemetry`,
} as const;

export type ServiceName = keyof typeof servicePaths;
