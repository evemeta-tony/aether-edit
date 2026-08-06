#!/usr/bin/env bash
# infra/ci/lint-go.sh
# Go lint gate: gofmt -l must be empty, then go vet, go build, and go test for
# every module. Exits successfully with a notice while the tree has no Go
# files yet; the moment a .go file is tracked, the full gate applies.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [ -z "$(git ls-files -- '*.go')" ]; then
  echo "lint-go: nothing to lint yet (no .go files tracked); check is armed"
  exit 0
fi

fail=0

unformatted="$(git ls-files -z -- '*.go' | xargs -0 gofmt -l)"
if [ -n "$unformatted" ]; then
  echo "lint-go: gofmt required for:"
  printf '%s\n' "$unformatted"
  fail=1
fi

modules_found=0
while IFS= read -r -d '' modfile; do
  modules_found=1
  dir="$(dirname "$modfile")"
  echo "lint-go: module $dir"
  (cd "$dir" && go vet ./... && go build ./... && go test ./...) || fail=1
done < <(git ls-files -z -- 'go.mod' '*/go.mod')

if [ "$modules_found" -eq 0 ]; then
  echo "lint-go: FAIL (.go files are tracked but no go.mod module contains them)"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "lint-go: FAIL"
  exit 1
fi

echo "lint-go: OK"
