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
