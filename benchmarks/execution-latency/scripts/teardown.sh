#!/usr/bin/env bash
# Deletes the benchmark's Kind cluster. Not run automatically by
# run-benchmark.sh (the cluster is left up so results can be spot-checked
# live and so a re-run doesn't pay setup cost twice) — invoke explicitly.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

require_cmds kind

log_info "deleting Kind cluster '${CLUSTER_NAME}'"
kind delete cluster --name "${CLUSTER_NAME}"
rm -f "${BENCH_KUBECONFIG}"
log_info "done"
