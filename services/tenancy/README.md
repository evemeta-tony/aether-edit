<!-- services/tenancy/README.md -->
# tenancy (FT-6a)

Tenancy, auth, quota, metering, and API key service for the Aether
Cloud File Transcoder. This is the S5 identity layer (FT-6a's first
deliverable per Janus V-2): it issues the access tokens every other FT
service middleware verifies, and it owns workspaces, plan tier quota
admission, metering rollups, and API keys. Billing is FT-6b and lives
elsewhere.

## Surfaces

Auth (S5, Google OIDC per AM-3):

| Route | What |
|---|---|
| `GET /v1/auth/login` | starts the authorization-code flow (state + nonce + PKCE S256), 302 to Google |
| `GET /v1/auth/callback` | single-use state check, code exchange, RS256 ID token verification against the issuer JWKS, first-login workspace bootstrap, session issue |
| `POST /v1/auth/refresh` | refresh rotation; reuse of a consumed token revokes the whole family (server-side revocation) |
| `POST /v1/auth/logout` | revokes the refresh family, clears the cookie |
| `POST /v1/auth/token` | exchanges a workspace API key for a short-lived contract JWT (usable against FT-2/FT-3) |

Access tokens are HS256 with the shared key, claims `sub`,
`workspaceId`, `exp`, `nbf`, `iat`, `iss`, `jti`: exactly the contract
the FT-2 and FT-3 middlewares verify. The `AccessSigner` interface is
JWKS-ready; the asymmetric upgrade is a recorded follow-up.

Workspaces (auth: bearer JWT; API keys are refused where a human user
is required):

| Route | What |
|---|---|
| `POST /v1/workspaces` | create (caller becomes owner, default tier) |
| `GET /v1/workspaces` | switcher list with role + active flag |
| `GET /v1/workspaces/active` | the token-scoped workspace |
| `POST /v1/workspaces/switch` | set active + mint a token scoped to it |
| `GET/POST /v1/workspaces/{id}/members` | membership list / add by email |
| `PATCH/DELETE /v1/workspaces/{id}/members/{userId}` | role change / removal |
| `GET /v1/me` | UserMenu identity block |
| `GET /v1/usage` | UserMenu plan/usage meter (rollups vs tier limits) |

Roles: `owner` > `admin` > `member`. Admins add/remove members;
granting or removing admin/owner takes owner; the last owner can
neither be demoted nor removed. Adding a member requires that user to
have signed in once already (invitations are out of scope for FT-6a).

API keys: `POST/GET /v1/apikeys`, `DELETE /v1/apikeys/{id}`. Format
`aek_<uuid>_<secret>`; argon2id (PHC string) at rest; the raw key is
shown exactly once at creation. The auth middleware accepts either a
bearer JWT or an API key (`Authorization: Bearer aek_...` or
`X-API-Key`). Revocation is immediate.

## Revocation semantics (stated exactly)

- API keys: revocation is immediate. The middleware and the exchange
  endpoint consult the store on every request.
- Refresh tokens: revocation is immediate and server-side (family
  revocation on logout or on rotation reuse).
- Access tokens are stateless JWTs and are NOT revocable before
  expiry. A member removed from a workspace, or a user who switched
  workspaces, holds any previously minted token as valid until its
  TTL (default 15 minutes) against services that only verify the
  claims contract (FT-2, FT-3, FT-4). Tenancy's own workspace
  endpoints re-check membership on each call and cut off sooner. The
  short TTL is the accepted bound for this posture; per-request
  membership callbacks or a token denylist would trade latency on
  every FT-2/FT-3 request and are not part of FT-6a.

Quota (frozen contract 3): `MeteredQuota` implements
`contracts.QuotaChecker` against the plan tier config plus the
metering rollups, with the typed reasons from ConfigQuota kept stable
and three added: `quota_storage_exceeded`,
`quota_encode_hours_exhausted`, `quota_tier_unknown`. It is served
over `POST /internal/v1/quota/check-upload-session` and
`/check-job-admission` (X-Internal-Token), and the `quotaclient`
package implements `contracts.QuotaChecker` over that API for FT-2 and
FT-3 to construct at deploy in place of `ConfigQuota` (which remains
the file-config fallback). Errors surface as errors: the V-5
fail-closed posture at the call sites denies on any failure.

Metering: a durable JetStream consumer on `aether.ft.metering.v1`
(frozen contract 2) builds per-workspace, per-UTC-month rollups in
Postgres (bytes uploaded, encode-seconds, session and job counts),
idempotent on `eventId` under redelivery. Storage usage is the
cumulative sum of `upload_completed` bytes; the metering contract has
no deletion event, so deletions do not reduce it (recorded honestly
here; a deletion event is a contract amendment for later).

## Configuration

All environment; no secrets in the repo. See
`infra/tenancy/tenancy.env.example` for the full list
(`TENANCY_DATABASE_URL`, `TENANCY_NATS_URL`, `TENANCY_AUTH_HS256_KEY`,
`TENANCY_OIDC_CLIENT_ID/SECRET/REDIRECT_URL`, `TENANCY_OIDC_ISSUER`,
`TENANCY_TIERS_CONFIG_PATH`, `TENANCY_INTERNAL_TOKEN`,
`TENANCY_LISTEN_ADDR` default `127.0.0.1:5401`, token TTLs). Plan
tiers are config-defined YAML (`infra/tenancy/plan-tiers.yaml`; demo
tier is deliberately generous).

## Migrations

golang-migrate file layout under `migrations/`
(`NNNNNN_label.up.sql` / `.down.sql`), applied by the embedded runner
in `migrate.go` (same precedent as FT-3: golang-migrate's module graph
carries MPL-2.0 dependencies outside the license allowlist), tracked
in `tenancy_schema_migrations`, serialized by an advisory lock.

## Tests

`go test ./...`: full OIDC flow against an httptest fake issuer
(discovery, JWKS, token endpoint enforcing client secret + PKCE),
refresh rotation and reuse-revocation, workspace/membership/role
rules, API key lifecycle including revocation and the JWT exchange,
quota math (size, storage, monthly encode-hours boundaries), rollup
consumption from recorded fixtures with idempotent replay, and the
internal quota API through `quotaclient`. `TestPostgresStore` runs
against a real database when `TENANCY_TEST_DATABASE_URL` is set and
skips otherwise; the in-memory store double is test-only.
