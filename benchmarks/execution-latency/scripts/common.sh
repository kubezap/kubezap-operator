#!/usr/bin/env bash
# Shared helpers for the execution-latency benchmark harness.
# Sourced by every other script in this directory — not meant to be run
# directly.
set -euo pipefail

# --- Configuration (overridable via env) -----------------------------------
CLUSTER_NAME="${CLUSTER_NAME:-kubezap-bench}"
KUBEZAP_NS="${KUBEZAP_NS:-kubezap-bench}"
ARGO_NS="${ARGO_NS:-argo-bench}"
ARGO_VERSION="${ARGO_VERSION:-v4.1.3}"
CONTROLLER_IMG="${CONTROLLER_IMG:-ghcr.io/kubezap/controller:latest}"
WEBHOOK_GATEWAY_IMG="${WEBHOOK_GATEWAY_IMG:-ghcr.io/kubezap/webhook-gateway:latest}"
HTTP_EXECUTOR_IMG="${HTTP_EXECUTOR_IMG:-ghcr.io/kubezap/http-executor:latest}"

BENCH_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${BENCH_ROOT}/../.." && pwd)"
MANIFESTS_DIR="${BENCH_ROOT}/manifests"

# KUBECONFIG used for every kubectl/kind/helm invocation this harness makes.
# Kept separate from the caller's default kubeconfig so this never disturbs
# an unrelated cluster context (mirrors test/e2e/e2e_suite_test.go's own
# temp-kubeconfig pattern).
BENCH_KUBECONFIG="${BENCH_KUBECONFIG:-${BENCH_ROOT}/results/.kubeconfig-${CLUSTER_NAME}}"
export KUBECONFIG="${BENCH_KUBECONFIG}"

# --- Logging ----------------------------------------------------------------
log_info() { printf '[%s] INFO  %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
log_warn() { printf '[%s] WARN  %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
log_err()  { printf '[%s] ERROR %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# now_iso prints the current UTC wall-clock time, millisecond precision.
now_iso() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }

# require_cmds fails fast (before any cluster work) if a required binary is
# missing, per METHODOLOGY.md §0's "go/no-go signal" framing — no value in
# discovering a missing dependency 150 runs into a 200-run sequence.
require_cmds() {
  local missing=()
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || missing+=("$c")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    log_err "missing required commands: ${missing[*]}"
    exit 1
  fi
}

# retry_until_terminal polls a getter function's stdout against a set of
# terminal values until one matches or the timeout elapses.
#   retry_until_terminal <timeout_s> <poll_interval_s> <getter_fn> <terminal_regex>
retry_until_terminal() {
  local timeout_s="$1" interval_s="$2" getter="$3" terminal_regex="$4"
  local waited=0
  local val=""
  while [ "$waited" -lt "$timeout_s" ]; do
    val="$("$getter" || true)"
    if [[ "$val" =~ $terminal_regex ]]; then
      printf '%s' "$val"
      return 0
    fi
    sleep "$interval_s"
    waited=$((waited + interval_s))
  done
  log_err "timed out after ${timeout_s}s waiting for terminal state (getter=${getter}, last value='${val}')"
  return 1
}
