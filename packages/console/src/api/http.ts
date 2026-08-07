// packages/console/src/api/http.ts
//
// The shared HTTP client for all four services. Responsibilities:
//   - attach the FT-6a bearer access token (and fall back to the refresh
//     cookie the tenancy OIDC flow set)
//   - transparently refresh an expired access token once, then retry
//   - surface a typed ApiError carrying status, code, and message so callers
//     can render honest error states (R10: never fabricate on failure)
//   - route a hard 401 to the login handler
//
// The refresh endpoint uses the httpOnly cookie, so it is called with
// credentials but without a bearer header.

import { servicePaths } from "./config";
import {
  accessTokenExpired,
  clearAccessToken,
  fireUnauthorized,
  getAccessToken,
  setAccessToken,
} from "./session";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryAfterMs: number | null;
  readonly body: unknown;

  constructor(status: number, code: string, message: string, body: unknown, retryAfterMs: number | null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.body = body;
    this.retryAfterMs = retryAfterMs;
  }
}

// Both service families shape errors as {error:{code,message}} (tenancy,
// upload) or {error:string} (orchestrator). This reads both.
function readError(status: number, body: unknown, retryAfterMs: number | null): ApiError {
  if (body && typeof body === "object" && "error" in (body as Record<string, unknown>)) {
    const e = (body as Record<string, unknown>).error;
    if (typeof e === "string") return new ApiError(status, "error", e, body, retryAfterMs);
    if (e && typeof e === "object") {
      const code = String((e as Record<string, unknown>).code ?? "error");
      const message = String((e as Record<string, unknown>).message ?? code);
      return new ApiError(status, code, message, body, retryAfterMs);
    }
  }
  return new ApiError(status, "error", `HTTP ${status}`, body, retryAfterMs);
}

function retryAfterMsFrom(res: Response, body: unknown): number | null {
  const header = res.headers.get("Retry-After");
  if (header) {
    const secs = Number(header);
    if (Number.isFinite(secs)) return Math.max(0, secs) * 1000;
  }
  if (body && typeof body === "object") {
    const backoff = (body as Record<string, unknown>).backoff;
    if (backoff && typeof backoff === "object") {
      const ms = (backoff as Record<string, unknown>).retryAfterMs;
      if (typeof ms === "number") return ms;
    }
  }
  return null;
}

let refreshInFlight: Promise<boolean> | null = null;

// refreshAccessToken exchanges the httpOnly refresh cookie for a new access
// token. Concurrent callers share one in-flight refresh.
export async function refreshAccessToken(): Promise<boolean> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      const res = await fetch(`${servicePaths.tenancy}/v1/auth/refresh`, {
        method: "POST",
        credentials: "include",
      });
      if (!res.ok) {
        clearAccessToken();
        return false;
      }
      const data = (await res.json()) as { accessToken?: string; expiresIn?: number };
      if (!data.accessToken || typeof data.expiresIn !== "number") {
        clearAccessToken();
        return false;
      }
      setAccessToken(data.accessToken, data.expiresIn);
      return true;
    } catch {
      clearAccessToken();
      return false;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

export interface RequestOptions {
  method?: string;
  // JSON body; when set, Content-Type is application/json.
  json?: unknown;
  // Raw body (e.g. a chunk); Content-Type is left to headers.
  body?: BodyInit;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  // When true, a 401 does not fire the global unauthorized handler; the caller
  // handles auth itself (used by the login bootstrap).
  noAuthRedirect?: boolean;
}

async function parseBody(res: Response): Promise<unknown> {
  const ct = res.headers.get("Content-Type") ?? "";
  if (res.status === 204) return null;
  if (ct.includes("application/json")) {
    try {
      return await res.json();
    } catch {
      return null;
    }
  }
  return null;
}

// request performs an authenticated fetch against a fully-qualified path,
// refreshing the access token once on expiry or on a first 401.
export async function request<T>(url: string, opts: RequestOptions = {}): Promise<T> {
  if (accessTokenExpired()) {
    await refreshAccessToken();
  }
  const res = await doFetch(url, opts);
  if (res.status === 401 && !opts.noAuthRedirect) {
    const refreshed = await refreshAccessToken();
    if (refreshed) {
      const retry = await doFetch(url, opts);
      if (retry.status === 401) {
        fireUnauthorized();
        throw readError(401, await parseBody(retry), null);
      }
      return finish<T>(retry);
    }
    fireUnauthorized();
    throw readError(401, await parseBody(res), null);
  }
  return finish<T>(res);
}

async function doFetch(url: string, opts: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { ...(opts.headers ?? {}) };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  let body: BodyInit | undefined = opts.body;
  if (opts.json !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.json);
  }
  return fetch(url, {
    method: opts.method ?? "GET",
    headers,
    body,
    credentials: "include",
    signal: opts.signal,
  });
}

async function finish<T>(res: Response): Promise<T> {
  const body = await parseBody(res);
  if (!res.ok) {
    throw readError(res.status, body, retryAfterMsFrom(res, body));
  }
  return body as T;
}
