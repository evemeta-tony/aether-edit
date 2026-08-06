#!/usr/bin/env bash
# infra/ci/check-dashes.sh
# Project rule C4: no em dashes or en dashes in authored files. Scans every
# tracked file for U+2014 (em dash) and U+2013 (en dash). Files matching a
# pattern in infra/ci/dash-exceptions.txt (third-party verbatim trees) are
# skipped, and binary files are skipped via grep's binary detection (-I).
# The dash characters are constructed from octal escapes below so that this
# script passes its own scan.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

EXCEPTIONS_FILE="infra/ci/dash-exceptions.txt"

em_dash="$(printf '\342\200\224')"
en_dash="$(printf '\342\200\223')"

is_exempt() {
  local candidate="$1" pattern
  [ -f "$EXCEPTIONS_FILE" ] || return 1
  while IFS= read -r pattern; do
    case "$pattern" in
      '' | '#'*) continue ;;
    esac
    # In a bash case pattern, * matches any characters including slashes, so
    # the ** convention used in the exceptions file works as written here.
    # shellcheck disable=SC2254
    case "$candidate" in
      $pattern) return 0 ;;
    esac
  done < "$EXCEPTIONS_FILE"
  return 1
}

failures=0

while IFS= read -r -d '' file; do
  [ -f "$file" ] || continue

  if is_exempt "$file"; then
    continue
  fi

  if grep -qI -e "$em_dash" -e "$en_dash" -- "$file"; then
    echo "dashes: $file contains an em dash (U+2014) or en dash (U+2013)"
    failures=$((failures + 1))
  fi
done < <(git ls-files -z)

if [ "$failures" -gt 0 ]; then
  echo "check-dashes: FAIL ($failures file(s))"
  exit 1
fi

echo "check-dashes: OK (no em or en dashes outside exempt trees)"
