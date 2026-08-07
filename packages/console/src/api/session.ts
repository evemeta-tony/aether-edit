// packages/console/src/api/session.ts
//
// In-memory access-token holder for the FT-6a bearer contract. The tenancy
// service issues a short-lived access token (JWT) plus an httpOnly refresh
// cookie (aether_refresh). We keep the access token in memory only (never in
// localStorage, so an XSS cannot exfiltrate a long-lived credential); the
// refresh cookie survives reloads and is exchanged at POST /v1/auth/refresh.
//
// A single unauthorized handler is registered by the app so that any 401 from
// any service routes the operator to login rather than rendering a broken
// console with a stale token.

let accessToken: string | null = null;
let expiresAtMs = 0;

// onUnauthorized is invoked when a request comes back 401 after a refresh
// attempt has already failed. The app wires this to its login redirect.
let onUnauthorized: (() => void) | null = null;

export function setAccessToken(token: string, expiresInSeconds: number): void {
  accessToken = token;
  // Refresh a little early so a request never rides an already-expired token.
  expiresAtMs = Date.now() + Math.max(0, expiresInSeconds - 30) * 1000;
}

export function clearAccessToken(): void {
  accessToken = null;
  expiresAtMs = 0;
}

export function getAccessToken(): string | null {
  return accessToken;
}

export function accessTokenExpired(): boolean {
  return accessToken === null || Date.now() >= expiresAtMs;
}

export function registerUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn;
}

export function fireUnauthorized(): void {
  clearAccessToken();
  if (onUnauthorized) onUnauthorized();
}
