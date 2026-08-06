#!/usr/bin/env bash
# infra/tenancy/provision-db.sh
# One-time Postgres provisioning for the tenancy service (V-8). Run as
# a Postgres superuser ON THE BOX. Creates only the tenancy role and
# database; never touches anything else on the shared cluster. Schema
# migrations run automatically at service start.
#
# The password is NEVER taken as an argument (argv is visible in the
# process table): supply it via the TENANCY_DB_PASSWORD environment
# variable, or run interactively and type it at the hidden prompt.
#   sudo -u postgres TENANCY_DB_PASSWORD=... ./provision-db.sh
#   sudo -u postgres ./provision-db.sh   # prompts, no echo
set -euo pipefail

if [ $# -ne 0 ]; then
  echo "usage: $0   (password via TENANCY_DB_PASSWORD env or interactive prompt, never argv)" >&2
  exit 1
fi

PASSWORD="${TENANCY_DB_PASSWORD:-}"
if [ -z "$PASSWORD" ]; then
  if [ ! -t 0 ]; then
    echo "error: TENANCY_DB_PASSWORD is not set and stdin is not a terminal" >&2
    exit 1
  fi
  read -rs -p "aether_tenancy password: " PASSWORD
  echo
fi
if [ -z "$PASSWORD" ]; then
  echo "error: empty password" >&2
  exit 1
fi

# The password reaches psql through its environment and \getenv
# (psql 15+), never on any process's argv. Environment is only
# readable by the invoking user and root.
export AETHER_TENANCY_PW="$PASSWORD"
psql -v ON_ERROR_STOP=1 <<'SQL'
\getenv pw AETHER_TENANCY_PW
SELECT 'CREATE ROLE aether_tenancy LOGIN'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'aether_tenancy')
\gexec
ALTER ROLE aether_tenancy WITH LOGIN PASSWORD :'pw';
SELECT 'CREATE DATABASE aether_tenancy OWNER aether_tenancy'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'aether_tenancy')
\gexec
SQL
unset AETHER_TENANCY_PW

echo "provisioned role and database aether_tenancy"
