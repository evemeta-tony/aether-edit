#!/usr/bin/env bash
# infra/ci/check-lf.sh
# Whole-tree line ending and BOM check (project rule C2).
# Fails if any tracked text file contains a CR byte (CRLF or bare CR line
# endings) or starts with a byte order mark (UTF-8, UTF-16 LE, UTF-16 BE).
# Binary files are skipped for the CR scan via grep's binary detection (-I),
# so genuine binary payloads do not false-positive; a UTF-16 file is still
# caught by the BOM test, which looks at raw leading bytes.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

cr="$(printf '\r')"
failures=0

while IFS= read -r -d '' file; do
  [ -f "$file" ] || continue

  head3="$(head -c 3 -- "$file" | od -An -tx1 | tr -d ' \n')"
  case "$head3" in
    efbbbf)
      echo "BOM (UTF-8): $file"
      failures=$((failures + 1))
      ;;
    fffe*)
      echo "BOM (UTF-16 LE): $file"
      failures=$((failures + 1))
      ;;
    feff*)
      echo "BOM (UTF-16 BE): $file"
      failures=$((failures + 1))
      ;;
  esac

  if grep -qI -- "$cr" "$file"; then
    echo "CR/CRLF: $file"
    failures=$((failures + 1))
  fi
done < <(git ls-files -z)

if [ "$failures" -gt 0 ]; then
  echo "check-lf: FAIL ($failures problem(s) found)"
  exit 1
fi

echo "check-lf: OK (all tracked text files are LF, no BOMs)"
