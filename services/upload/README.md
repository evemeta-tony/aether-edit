<!-- services/upload/README.md -->
# upload service (FT-2)

Resumable chunked upload service for the aether-edit file transcoder
track. Contracts: see `docs/contracts/ft-contracts-v0.md`.

## API

All `/v1` routes require `Authorization: Bearer <token>` (HS256 signed,
claims `sub` and `workspaceId`). Full OIDC issuer wiring is FT-6/S5
scope.

- `POST /v1/uploads` with `{"filename","sizeBytes","mime"}` creates a
  session after `QuotaChecker.CheckUploadSession` passes. The quota
  check is fail closed: the mounted config must explicitly set
  `allowUploads: true` for the workspace (or in `defaults`) or creation
  is denied. Returns `uploadId` (uuidv7), `chunkSizeBytes` (64 MiB),
  `chunkCount`.
- `GET /v1/uploads/{id}` returns the session and chunk map (the resume
  query).
- `PUT /v1/uploads/{id}/chunks/{n}` uploads one chunk body. Headers:
  exact `Content-Length` and `X-Chunk-Sha256` (hex64). The body hash is
  verified before the part is written. Re uploading a DONE chunk is a
  no op. When the inflight byte ceiling is reached the service answers
  429 with `Retry-After` and a JSON backoff hint that escalates per
  session.
- `POST /v1/uploads/{id}/complete` verifies the chunk map, assembles
  server side (S3 multipart complete plus server side copy), mints the
  whole object sha256, writes to
  `assets/<workspaceId>/sha256/<hex64>`, emits the landed object and
  metering events over JetStream, and returns `objectKey` and `sha256`.
  Safe to retry: publication failures leave the session ASSEMBLED.
- `DELETE /v1/uploads/{id}` cancels and garbage collects parts.

### Integrity guarantee boundary

Per chunk `X-Chunk-Sha256` verification protects the transport of each
chunk at write time. Part ETags recorded then are passed through to the
multipart complete without a second per part comparison against stored
bytes. The authoritative integrity gate is the whole object sha256,
recomputed by streaming the assembled object at complete time; a
corrupted or resized part fails there. Downstream consumers (FT-3)
must rely on the minted whole object hash, not on any per part
guarantee.

### Backpressure scope

The inflight bytes ceiling gates concurrent chunk bodies at admission
time; each body releases its reservation when its request finishes. A
single threaded client under the ceiling never trips it. Sustained
throughput behavior under real load is measured at OVH acceptance
(R10).

## Configuration (environment)

| Variable | Meaning | Default |
|---|---|---|
| `UPLOAD_LISTEN_ADDR` | HTTP bind address | `127.0.0.1:5301` |
| `UPLOAD_DATABASE_URL` | Postgres URL (pgx) | required |
| `UPLOAD_S3_ENDPOINT` | S3 compatible endpoint URL | required |
| `UPLOAD_S3_REGION` | Region name | `gra` |
| `UPLOAD_S3_BUCKET` | Bucket | required |
| `UPLOAD_S3_ACCESS_KEY` | Access key | required |
| `UPLOAD_S3_SECRET_KEY` | Secret key | required |
| `UPLOAD_S3_PATH_STYLE` | Path style addressing | `true` |
| `UPLOAD_NATS_URL` | NATS URL | `nats://127.0.0.1:4222` |
| `UPLOAD_QUOTA_CONFIG_PATH` | Quota YAML or JSON file | required |
| `UPLOAD_AUTH_HS256_KEY` | base64url HMAC key, 32+ bytes | required |
| `UPLOAD_MAX_INFLIGHT_BYTES` | Backpressure ceiling | `1073741824` |
| `UPLOAD_MAX_OBJECT_BYTES` | Max declared object size | `1099511627776` |

Secrets arrive via the environment (systemd `EnvironmentFile`), never
the repo.

## Database

Schema migrations live in `migrations/` in golang-migrate layout:

```
migrate -path migrations -database "$UPLOAD_DATABASE_URL" up
```

## Tests

```
go test ./...
```

The suite runs the real S3 client against a local fake S3 endpoint and
an in memory store double; the Postgres store itself is exercised when
`UPLOAD_TEST_DATABASE_URL` points at a scratch database.
