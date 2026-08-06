# packages/console/

The Aether Cloud **file-transcoder console** (FT-5): the production port of the
design prototype (`docs/design/ui_kits/aether-live/FileTranscoder.jsx`,
`Uploader.jsx`, `Parts.jsx`), wired to the real backend services. This replaces
the WO-001R scaffold README that named FT-5 as the deliverer of this directory.

## Stack

- Vite 5 + React 18 + TypeScript, npm (single committed `package-lock.json`).
- `lucide-react` (ISC) for icons; the Aether mark is inline SVG.
- Design tokens are imported verbatim from `docs/design/colors_and_type.css`
  (the source of record) via a CSS `@import` in `src/styles/base.css`, so the
  console can never drift from the token file. No colors are invented.

## Services it binds to

| Lane | Base path | What binds |
|---|---|---|
| FT-6a tenancy | `/api/tenancy` | OIDC login/refresh/logout, `/v1/me`, `/v1/workspaces`, `/v1/usage`, `/v1/apikeys`. Backs the SaaS chrome: UserMenu, workspace switcher, plan/usage meter. |
| FT-2 upload | `/api/upload` | The real resumable chunked upload state machine: `POST /v1/uploads`, `PUT .../chunks/{n}`, `POST .../complete`, `DELETE`. 64 MiB chunks, up to 8 parallel, server chunk map as truth, sha256 per chunk, retry, resume-from-map, 429 + Retry-After backpressure. |
| FT-3 jobs | `/api/jobs` | Job queue, retry, and the preset editor: `GET/POST /v1/jobs`, `POST /v1/jobs/{id}/retry`, `DELETE`, `GET/POST/PATCH /v1/presets`. |
| FT-4 telemetry | `/api/telemetry` | The three SSE streams `GET /v1/streams/{hardware,jobs,logs}`. Every live readout binds here. |

The API base is configurable via `VITE_API_BASE` (default the relative `/api`).
Auth uses the FT-6a bearer access token (kept in memory) plus the httpOnly
refresh cookie the tenancy OIDC flow sets; a hard 401 routes to login.

## Honesty rules that shaped the port (R10)

There are **zero simulated values**. The prototype's tick-loop job simulation,
throughput random walk, sample-title fallback, Drop-link fault injector, and
index-based worker assignment are all removed and replaced by live API/SSE data.
Where a backend field or route does not exist yet, the UI shows the em dash
(U+2014) for the absent value or a real loading/empty/error state, and never a
fabricated number. Specifically:

- **GPU/host telemetry**: rendered from the FT-4 hardware stream. When the
  sticky `status` event reports `gpu:"unavailable"`, every GPU readout shows the
  em dash (the honest-absence rule). `cpuUtilPct` is always shown.
- **Source mediainfo, per-job duration, poster frame**: no FT-3 probe/poster
  HTTP route exists, so those rows render the em dash and the poster is a
  user-fillable slot with local persistence, rather than the prototype's `SRC`
  constants.
- **EVE mode (AM-11)**: the toggle is live, the output-profile pane locks with
  derived rows, and delivery formats stay multi-select. Because no EVE adapter
  exists (U3), EVE mode is an explicit **eve-pending** state, never a silent
  fallback to the manual profile (C1).
- **Delivery (AM-10)**: media library, archive, and CDN package are the v1
  target types; the webhook target is rendered explicitly disabled.

See `docs/design/panel-map-file.md` for the full readout-to-source map.

## Develop

```
npm ci
npm run dev        # Vite dev server; proxies /api/* to the services (see vite.config.ts)
npm run build      # tsc --noEmit then vite build
npm run typecheck
npm run lint
```

Point the dev proxy at running services with `VITE_DEV_TENANCY_TARGET`,
`VITE_DEV_UPLOAD_TARGET`, `VITE_DEV_JOBS_TARGET`, `VITE_DEV_TELEMETRY_TARGET`.
