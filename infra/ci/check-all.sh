#!/usr/bin/env bash
# infra/ci/check-all.sh
# Local mirror of the CI gate: runs the same scripts GitHub Actions runs, in
# order, and stops on the first failure. This mirrors the check scripts, not
# the hosted-runner provisioning steps (actions/setup-go, actions/setup-node,
# rustup on the runner), which execute only in GitHub Actions; a green local
# run is a strong predictor of green CI, not a byte-for-byte replay of it.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

bash infra/ci/check-lf.sh
bash infra/ci/check-first-line.sh
bash infra/ci/check-dashes.sh
bash infra/ci/check-licenses.sh
bash infra/ci/lint-go.sh
bash infra/ci/lint-rust.sh
bash infra/ci/lint-js.sh

echo "check-all: OK"
