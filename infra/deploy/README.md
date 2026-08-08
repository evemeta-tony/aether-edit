infra/deploy/README.md

# aether-edit deploy kit (V-8)

Versioned copies of what runs the file-transcoder on transcoder.evemeta.com
(the OVH L4 box). Secrets are NOT here: every *.env.template shows the shape
with secret values redacted; the real files live at /etc/aether-edit/*.env on
the box (0640 root:aether-edit) and are composed from the OVH S3 credential,
the Google OIDC client, the shared HS256 key, and the Postgres DSN.

Services (systemd, User=aether-edit, hardened; loopback ports fronted by nginx):
  aether-nats.service         127.0.0.1:9214  dedicated JetStream broker
  aether-tenancy.service      127.0.0.1:9210  FT-6a auth/tenancy/quota/metering
  aether-upload.service       127.0.0.1:9211  FT-2 resumable upload -> OVH S3
  aether-orchestrator.service 127.0.0.1:9212  FT-3 jobs + NVENC transcode
  aether-telemetry.service    127.0.0.1:9213  FT-4 SSE telemetry

Public surface: nginx-transcoder.evemeta.com.conf serves the console SPA
(/var/www/aether-console) and proxies /api/{tenancy,upload,jobs,telemetry};
Google OIDC is the single login gate. ffmpeg is the C-5 LGPL+NVENC build from
build-ffmpeg.sh installed at /opt/aether-edit/ffmpeg.

Quota configs diverge by service (upload uses the contracts format,
orchestrator its own maxActiveJobs format) - flagged to Ivo to unify.

## Deploy posture, honestly stated (Argus PR#11)

- Activation order (V-5): FT-6a tenancy is brought up first; FT-2/3/4 use its
  Google-issued HS256 JWTs. Liveness is VERIFIED on the box (2026-08-08): a full
  upload -> S3 -> auto-created job -> NVENC transcode -> S3 output round-trip
  passed end to end, and the same login JWT is accepted by upload/jobs/telemetry
  (no-token = 401 on every service).
- Auth at the edge: the nginx /api/* locations are pass-throughs with NO edge
  auth by design; each service enforces the bearer JWT itself (verified:
  /api/jobs returns 401 without a token). Google OIDC is the single login gate.
- Quota (KNOWN NON-CONFORMANCE, not just flagged): upload consumes the frozen
  contracts ConfigQuota shape; orchestrator consumes its OWN {maxActiveJobs,
  maxUploadBytes} shape. This deploy runs DIVERGENT quota enforcement and does
  NOT conform to the single frozen QuotaChecker/ConfigQuota contract. Required
  follow-up (owner Ivo, its own Argus-gated code PR): unify the orchestrator
  onto contracts.ConfigQuota. On the shared box, orchestrator autocreate is
  bounded (maxActiveJobs=4 ~ scheduler slots=3) to avoid runaway GPU/CPU work.
- Postgres DSNs use sslmode=disable: justified only because every service
  connects over loopback to 127.0.0.1:5435 on the same host; not acceptable for
  any off-box connection.
- /api/upload has no edge body cap (client_max_body_size 0, request buffering
  off) by resumable-upload design; the upload service enforces the size cap
  (UPLOAD_MAX_OBJECT_BYTES, 20 GiB).
