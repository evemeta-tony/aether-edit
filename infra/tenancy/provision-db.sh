#!/usr/bin/env bash
# infra/tenancy/provision-db.sh
# One-time Postgres provisioning for the tenancy service (V-8). Run as
# a Postgres superuser ON THE BOX. Creates only the tenancy role and
# database; never touches anything else on the shared cluster. Schema
# migrations run automatically at service start.
#   sudo -u postgres ./provision-db.sh 'strong-password-here'
set -euo pipefail

if [ $# -ne 1 ] || [ -z "$1" ]; then
  echo "usage: $0 <aether_tenancy password>" >&2
  exit 1
fi
PASSWORD="$1"

psql -v ON_ERROR_STOP=1 -v pw="$PASSWORD" <<'SQL'
SELECT 'CREATE ROLE aether_tenancy LOGIN'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'aether_tenancy')
\gexec
ALTER ROLE aether_tenancy WITH LOGIN PASSWORD :'pw';
SELECT 'CREATE DATABASE aether_tenancy OWNER aether_tenancy'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'aether_tenancy')
\gexec
SQL

echo "provisioned role and database aether_tenancy"
