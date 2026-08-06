#!/usr/bin/env bash
# infra/ci/lint-rust.sh
# Rust lint gate: cargo fmt --check and cargo clippy with warnings denied, for
# every Cargo.toml that defines a package or workspace. Exits successfully
# with a notice while the tree has no Rust files yet; the moment a .rs file is
# tracked, the full gate applies. The toolchain comes from rust-toolchain.toml
# via rustup.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [ -z "$(git ls-files -- '*.rs')" ]; then
  echo "lint-rust: nothing to lint yet (no .rs files tracked); check is armed"
  exit 0
fi

fail=0
manifests_found=0

while IFS= read -r -d '' manifest; do
  case "$manifest" in
    rust-toolchain.toml) continue ;;
  esac
  manifests_found=1
  dir="$(dirname "$manifest")"
  echo "lint-rust: manifest $dir"
  (cd "$dir" && cargo fmt --check && cargo clippy --all-targets -- -D warnings) || fail=1
done < <(git ls-files -z -- 'Cargo.toml' '*/Cargo.toml')

if [ "$manifests_found" -eq 0 ]; then
  echo "lint-rust: FAIL (.rs files are tracked but no Cargo.toml manifest exists)"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "lint-rust: FAIL"
  exit 1
fi

echo "lint-rust: OK"
