#!/usr/bin/env bash
# infra/ci/check-licenses.sh
# Project rule S7: dependencies must be MIT, BSD, Apache-2.0, or ISC licensed.
# Scans every tracked go.mod, package.json, and Cargo.toml, resolves licenses
# where that is possible offline (the curated map in license-map.txt, or an
# installed node_modules copy of the package), and fails on any dependency
# whose license is outside the allowlist or cannot be resolved, unless the
# dependency is listed in infra/ci/license-exceptions.txt (signed exceptions
# only). Unresolvable is a failure on purpose: an unverified license is not an
# approved license.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

MAP_FILE="infra/ci/license-map.txt"
EXCEPTIONS_FILE="infra/ci/license-exceptions.txt"

failures=0
checked=0

allowed_single() {
  case "$1" in
    MIT | ISC | Apache-2.0 | 0BSD | BSD | BSD-2-Clause | BSD-3-Clause | BSD-3-Clause-Clear)
      return 0 ;;
  esac
  return 1
}

# Handles plain SPDX ids plus simple "A OR B" / "A AND B" expressions.
# Expressions mixing AND and OR are never passed here: judge() rejects them
# up front as UNRESOLVED (fail closed) rather than risking a mis-parse.
allowed_license() {
  local expr="$1" part ok
  expr="${expr//(/}"
  expr="${expr//)/}"
  if [[ "$expr" == *" OR "* ]]; then
    for part in ${expr// OR / }; do
      if allowed_single "$part"; then return 0; fi
    done
    return 1
  fi
  if [[ "$expr" == *" AND "* ]]; then
    ok=0
    for part in ${expr// AND / }; do
      if ! allowed_single "$part"; then ok=1; fi
    done
    return "$ok"
  fi
  allowed_single "$expr"
}

is_excepted() {
  local dep="$1" first rest
  [ -f "$EXCEPTIONS_FILE" ] || return 1
  while read -r first rest; do
    case "$first" in
      '' | '#') continue ;;
      '#'*) continue ;;
    esac
    if [ "$first" = "$dep" ]; then return 0; fi
  done < "$EXCEPTIONS_FILE"
  return 1
}

# Longest-prefix lookup: a map entry matches the dep exactly or as a leading
# path segment (entry "golang.org/x" matches "golang.org/x/sys").
map_license() {
  local dep="$1" entry license best="" best_len=0
  [ -f "$MAP_FILE" ] || return 1
  while read -r entry license; do
    case "$entry" in
      '' | '#'*) continue ;;
    esac
    if [ "$dep" = "$entry" ] || [[ "$dep" == "$entry"/* ]]; then
      if [ "${#entry}" -gt "$best_len" ]; then
        best="$license"
        best_len="${#entry}"
      fi
    fi
  done < "$MAP_FILE"
  [ -n "$best" ] && printf '%s\n' "$best"
}

judge() {
  local dep="$1" license="$2" manifest="$3"
  checked=$((checked + 1))
  if is_excepted "$dep"; then
    echo "exception: $dep ($manifest) is covered by a signed entry in $EXCEPTIONS_FILE"
    return
  fi
  if [ -z "$license" ]; then
    echo "UNRESOLVED: $dep ($manifest): license not resolvable offline; verify upstream and add it to $MAP_FILE, or record a signed exception in $EXCEPTIONS_FILE"
    failures=$((failures + 1))
    return
  fi
  if [[ "$license" == *" OR "* && "$license" == *" AND "* ]]; then
    echo "UNRESOLVED: $dep ($manifest): mixed AND/OR license expression '$license' is not parsed here and fails closed; verify upstream and record the verified license in $MAP_FILE, or record a signed exception in $EXCEPTIONS_FILE"
    failures=$((failures + 1))
    return
  fi
  if ! allowed_license "$license"; then
    echo "DISALLOWED: $dep ($manifest): license '$license' is outside the MIT/BSD/Apache-2.0/ISC allowlist and has no signed exception"
    failures=$((failures + 1))
  fi
}

# --- go.mod -----------------------------------------------------------------
check_go_mod() {
  local manifest="$1" dep
  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    judge "$dep" "$(map_license "$dep" || true)" "$manifest"
  done < <(awk '
    /^require \(/ { inblock = 1; next }
    inblock && /^\)/ { inblock = 0; next }
    inblock { print $1 }
    /^require [^(]/ { print $2 }
  ' "$manifest")
}

# --- package.json -----------------------------------------------------------
check_package_json() {
  local manifest="$1" dir dep license installed
  dir="$(dirname "$manifest")"
  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    license=""
    installed="$dir/node_modules/$dep/package.json"
    if [ -f "$installed" ]; then
      license="$(python3 - "$installed" <<'PYEOF'
import json, sys
with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
lic = data.get("license", "")
if isinstance(lic, dict):
    lic = lic.get("type", "")
print(lic)
PYEOF
)"
    fi
    if [ -z "$license" ]; then
      license="$(map_license "$dep" || true)"
    fi
    judge "$dep" "$license" "$manifest"
  done < <(python3 - "$manifest" <<'PYEOF'
import json, sys
with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
deps = set()
for key in ("dependencies", "devDependencies", "optionalDependencies"):
    deps.update((data.get(key) or {}).keys())
for name in sorted(deps):
    print(name)
PYEOF
)
}

# --- Cargo.toml -------------------------------------------------------------
check_cargo_toml() {
  local manifest="$1" dep
  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    judge "$dep" "$(map_license "$dep" || true)" "$manifest"
  done < <(awk '
    /^\[(dependencies|dev-dependencies|build-dependencies)(\..*)?\]/ { indeps = 1; next }
    /^\[/ { indeps = 0 }
    indeps && /^[A-Za-z0-9_-]+[ \t]*=/ { split($0, kv, /[ \t=]/); print kv[1] }
  ' "$manifest")
}

manifests=0
while IFS= read -r -d '' manifest; do
  case "$manifest" in
    */node_modules/*) continue ;;
  esac
  manifests=$((manifests + 1))
  case "$manifest" in
    go.mod | */go.mod) check_go_mod "$manifest" ;;
    package.json | */package.json) check_package_json "$manifest" ;;
    Cargo.toml | */Cargo.toml) check_cargo_toml "$manifest" ;;
  esac
done < <(git ls-files -z -- 'go.mod' '*/go.mod' 'package.json' '*/package.json' 'Cargo.toml' '*/Cargo.toml')

if [ "$failures" -gt 0 ]; then
  echo "check-licenses: FAIL ($failures dependency problem(s))"
  exit 1
fi

if [ "$manifests" -eq 0 ]; then
  echo "check-licenses: OK (no dependency manifests tracked yet; the check is armed)"
else
  echo "check-licenses: OK ($manifests manifest(s), $checked dependency(ies) checked)"
fi
