#!/usr/bin/env bash
# infra/ci/lint-js.sh
# JS/TS lint gate: for every package.json whose package tracks .ts/.tsx/.js/
# .jsx sources, install pinned dependencies (npm ci, which requires the
# committed lockfile) and run eslint with zero warnings allowed. Exits
# successfully with a notice while the tree has no JS/TS files yet; the moment
# one is tracked, the full gate applies, and a package with sources but no
# usable eslint setup is a failure, not a skip.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [ -z "$(git ls-files -- '*.ts' '*.tsx' '*.js' '*.jsx')" ]; then
  echo "lint-js: nothing to lint yet (no .ts/.tsx/.js/.jsx files tracked); check is armed"
  exit 0
fi

fail=0
covered=0

while IFS= read -r -d '' manifest; do
  case "$manifest" in
    */node_modules/*) continue ;;
  esac
  dir="$(dirname "$manifest")"
  # Match sources at any depth under the package directory, so nested layouts
  # (e.g. $dir/src/**) cannot dodge the gate.
  if [ -z "$(git ls-files -- "$dir" | grep -E '\.(ts|tsx|js|jsx)$' || true)" ]; then
    continue
  fi
  covered=1
  echo "lint-js: package $dir"
  if [ ! -f "$dir/package-lock.json" ]; then
    echo "lint-js: FAIL ($dir has JS/TS sources but no committed package-lock.json)"
    fail=1
    continue
  fi
  (cd "$dir" && npm ci --ignore-scripts && npx --no-install eslint . --max-warnings 0) || fail=1
done < <(git ls-files -z -- 'package.json' '*/package.json')

if [ "$covered" -eq 0 ]; then
  echo "lint-js: FAIL (JS/TS files are tracked outside any package.json package)"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "lint-js: FAIL"
  exit 1
fi

echo "lint-js: OK"
