#!/usr/bin/env bash
# infra/ci/check-all.sh
# Local mirror of the CI gate: runs the same scripts GitHub Actions runs, in
# order, and stops on the first failure. A fresh clone plus this command
# passing means CI will be green.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

bash infra/ci/check-lf.sh
bash infra/ci/check-first-line.sh
bash infra/ci/check-licenses.sh
bash infra/ci/lint-go.sh
bash infra/ci/lint-rust.sh
bash infra/ci/lint-js.sh

echo "check-all: OK"
