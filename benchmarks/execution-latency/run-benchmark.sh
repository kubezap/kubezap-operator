#!/usr/bin/env bash
# Single entry point for the KubeZap vs. Argo Workflows execution-latency
# benchmark, implementing the scenario/metrics/run-count defined in
# benchmarks/execution-latency/METHODOLOGY.md.
#
# Usage:
#   ./run-benchmark.sh [--runs N] [--skip-setup] [--only kubezap|argo|both]
#                       [--results-dir DIR] [--cluster-name NAME] [--smoke]
#
# Options:
#   --runs N          Number of sequential firings per system (default 200,
#                      per METHODOLOGY.md §4.1). Overriding this changes the
#                      statistical guarantees §4.1 derives for p95 — use only
#                      for smoke-testing the harness itself.
#   --smoke           Shorthand for --runs 3. Clearly labels the results
#                      directory as a smoke run, not a methodology-compliant
#                      benchmark result.
#   --skip-setup      Assume the Kind cluster + both systems are already
#                      installed (from a prior run) and go straight to
#                      firing. Useful for iterating on the runner scripts.
#   --only WHICH       kubezap | argo | both (default: both)
#   --results-dir DIR  Where to write per-run JSON files (default:
#                       benchmarks/execution-latency/results/<UTC timestamp>/)
#   --cluster-name N    Kind cluster name (default: kubezap-bench)
#
# What this does NOT do: compute summary statistics (p50/p95, etc.) from the
# raw per-run files. METHODOLOGY.md §4.3 deliberately scopes this harness to
# producing raw per-run JSON so a later story can recompute every statistic
# independently; see METHODOLOGY.md §5.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./scripts/common.sh
source "${SCRIPT_DIR}/scripts/common.sh"

RUNS=200
SKIP_SETUP=0
ONLY="both"
RESULTS_DIR=""
SMOKE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --runs) RUNS="$2"; shift 2 ;;
    --smoke) SMOKE=1; RUNS=3; shift ;;
    --skip-setup) SKIP_SETUP=1; shift ;;
    --only) ONLY="$2"; shift 2 ;;
    --results-dir) RESULTS_DIR="$2"; shift 2 ;;
    --cluster-name) CLUSTER_NAME="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,26p' "$0"
      exit 0
      ;;
    *)
      log_err "unknown argument: $1"
      exit 1
      ;;
  esac
done

case "${ONLY}" in
  kubezap|argo|both) ;;
  *) log_err "--only must be kubezap, argo, or both (got '${ONLY}')"; exit 1 ;;
esac

if [ -z "${RESULTS_DIR}" ]; then
  label="full"
  [ "${SMOKE}" -eq 1 ] && label="smoke"
  RESULTS_DIR="${BENCH_ROOT}/results/${label}-$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "${RESULTS_DIR}"
log_info "results directory: ${RESULTS_DIR}"
if [ "${SMOKE}" -eq 1 ]; then
  log_warn "SMOKE RUN (--smoke): ${RUNS} firings per system, not the methodology's 200. Results here validate the harness mechanically — they are NOT a methodology-compliant benchmark result."
fi

if [ "${SKIP_SETUP}" -eq 1 ]; then
  log_info "--skip-setup: assuming cluster and both systems are already installed"
else
  "${SCRIPT_DIR}/scripts/setup.sh"
fi

# --- Resource sampler lifecycle ---------------------------------------------
SAMPLER_PID=""
start_sampler() {
  local ns="$1" prefix="$2"
  "${SCRIPT_DIR}/scripts/resource_sampler.sh" "${ns}" "${prefix}" 5 &
  SAMPLER_PID=$!
}
stop_sampler_and_finalize() {
  local system="$1" prefix="$2" out_file="$3"
  if [ -n "${SAMPLER_PID}" ]; then
    kill "${SAMPLER_PID}" 2>/dev/null || true
    wait "${SAMPLER_PID}" 2>/dev/null || true
    SAMPLER_PID=""
  fi
  local samples="${prefix}.samples.jsonl"
  local created="${prefix}.created.txt"
  local captured="${prefix}.captured.txt"
  # Ensure all three files exist (possibly empty) so --slurpfile/--rawfile
  # below never fail on a missing path.
  [ -f "${samples}" ] || : > "${samples}"
  [ -f "${created}" ] || : > "${created}"
  [ -f "${captured}" ] || : > "${captured}"
  # Read directly from the files via --slurpfile/--rawfile rather than passing
  # the arrays through --argjson: at full 200-run scale (~1300+ 5s-interval
  # samples over a ~100min Argo arm) a serialized samples array on the exec
  # argv exceeds Linux's per-argument MAX_ARG_STRLEN (128KiB), and `jq` fails
  # with "Argument list too long" -- silently losing the entire
  # resource-overhead file.
  jq -n \
    --arg system "${system}" \
    --slurpfile samples "${samples}" \
    --rawfile created_raw "${created}" \
    --rawfile captured_raw "${captured}" \
    '
     ($created_raw | split("\n") | map(select(length>0))) as $pods_created
     | ($captured_raw | split("\n") | map(select(length>0))) as $pods_captured
     | {
       system: $system,
       polling_interval_seconds: 5,
       metrics_server_resolution_seconds: 15,
       samples: $samples,
       pods_created_count: ($pods_created | length),
       pods_captured_count: ($pods_captured | length),
       pods_created: $pods_created,
       pods_captured: $pods_captured,
       capture_rate: (if ($pods_created | length) > 0 then (($pods_captured | length) / ($pods_created | length)) else null end)
     }' > "${out_file}"
  rm -f "${samples}" "${created}" "${captured}"
  log_info "wrote ${out_file}"
}

PF_PID=""
cleanup() {
  [ -n "${SAMPLER_PID}" ] && kill "${SAMPLER_PID}" 2>/dev/null || true
  [ -n "${PF_PID}" ] && kill "${PF_PID}" 2>/dev/null || true
}
trap cleanup EXIT

# --- KubeZap arm -------------------------------------------------------------
if [ "${ONLY}" = "kubezap" ] || [ "${ONLY}" = "both" ]; then
  log_info "starting port-forward to svc/kubezap-webhook-gateway (KubeZap arm firing mechanism)"
  kubectl port-forward -n "${KUBEZAP_NS}" svc/kubezap-webhook-gateway 18080:8080 \
    >/tmp/kubezap-bench-portforward.log 2>&1 &
  PF_PID=$!
  # Wait for the forwarded port to accept connections before firing.
  for _ in $(seq 1 30); do
    curl -s -o /dev/null -m 2 "http://127.0.0.1:18080/" && break
    sleep 1
  done

  RESOURCE_PREFIX="${RESULTS_DIR}/.kubezap-resources"
  start_sampler "${KUBEZAP_NS}" "${RESOURCE_PREFIX}"

  log_info "firing ${RUNS} sequential webhook requests against KubeZap"
  fail_count=0
  for i in $(seq 1 "${RUNS}"); do
    "${SCRIPT_DIR}/scripts/run_kubezap.sh" "${i}" "${RESULTS_DIR}" 18080 || fail_count=$((fail_count + 1))
  done
  log_info "KubeZap arm done: ${RUNS} firings, ${fail_count} failed to complete"

  stop_sampler_and_finalize "kubezap" "${RESOURCE_PREFIX}" "${RESULTS_DIR}/kubezap-resource-samples.json"

  kill "${PF_PID}" 2>/dev/null || true
  wait "${PF_PID}" 2>/dev/null || true
  PF_PID=""
fi

# --- Argo arm -----------------------------------------------------------------
if [ "${ONLY}" = "argo" ] || [ "${ONLY}" = "both" ]; then
  RESOURCE_PREFIX="${RESULTS_DIR}/.argo-resources"
  start_sampler "${ARGO_NS}" "${RESOURCE_PREFIX}"

  log_info "firing ${RUNS} sequential Workflow submissions against Argo"
  fail_count=0
  for i in $(seq 1 "${RUNS}"); do
    "${SCRIPT_DIR}/scripts/run_argo.sh" "${i}" "${RESULTS_DIR}" || fail_count=$((fail_count + 1))
  done
  log_info "Argo arm done: ${RUNS} firings, ${fail_count} failed to complete"

  stop_sampler_and_finalize "argo" "${RESOURCE_PREFIX}" "${RESULTS_DIR}/argo-resource-samples.json"
fi

log_info "benchmark complete. Raw per-run files and resource-sample files are in: ${RESULTS_DIR}"
ls "${RESULTS_DIR}" | sort | sed 's/^/  /' >&2
