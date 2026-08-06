#!/usr/bin/env bash
# infra/ci/check-first-line.sh
# Project rule C3: every source file starts with its repo-relative path as the
# first comment line, in the language's native comment style. For file types
# whose line 1 may legitimately be a shebang (or any file starting with #!),
# the path comment is expected on line 2, since the shebang is not a comment.
# Files matching a pattern in infra/ci/first-line-exceptions.txt (third-party
# verbatim trees) are skipped. Formats without comments (e.g. JSON) are not in
# the extension list and are therefore exempt by construction.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

EXCEPTIONS_FILE="infra/ci/first-line-exceptions.txt"

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

  case "$file" in
    *.go | *.rs | *.ts | *.tsx | *.js | *.jsx | *.py | *.css | *.sh | *.yml) ;;
    *) continue ;;
  esac

  if is_exempt "$file"; then
    continue
  fi

  line="$(head -n 1 -- "$file")"
  case "$line" in
    '#!'*)
      line="$(sed -n '2p' -- "$file")"
      ;;
  esac

  case "$file" in
    *.go | *.rs | *.ts | *.tsx | *.js | *.jsx)
      expected="// $file"
      ;;
    *.py | *.sh | *.yml)
      expected="# $file"
      ;;
    *.css)
      expected="/* $file */"
      ;;
  esac

  if [ "$line" != "$expected" ]; then
    echo "first-line: $file: expected \"$expected\" but found \"$line\""
    failures=$((failures + 1))
  fi
done < <(git ls-files -z)

if [ "$failures" -gt 0 ]; then
  echo "check-first-line: FAIL ($failures file(s))"
  exit 1
fi

echo "check-first-line: OK"
