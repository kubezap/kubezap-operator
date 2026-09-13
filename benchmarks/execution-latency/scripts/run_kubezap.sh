#!/usr/bin/env bash
# Fires one webhook POST against the KubeZap scenario Trigger, waits for the
# resulting FlowRun to reach a terminal phase, and writes one raw-result JSON
# file, per METHODOLOGY.md §4.3.
#
# Usage: run_kubezap.sh <run_index> <results_dir> <local_port>
#   run_index   - 1-based firing number (zero-padded to 3 digits in the filename)
#   results_dir - directory to write kubezap-run-NNN.json into
#   local_port  - localhost port a `kubectl port-forward` to
#                 svc/kubezap-webhook-gateway:8080 is already listening on
#                 (started once by run-benchmark.sh, not per-run — spinning up
#                 a fresh curl pod per firing, as the e2e suite does for a
#                 single one-off assertion, would add ~seconds of pod
#                 scheduling noise per firing across 200 runs and would also
#                 pollute the KubeZap side's resource-sampling window with
#                 curl-pod churn that has no Argo-side equivalent; a single
#                 long-lived port-forward avoids both. This does not affect
#                 any measured timestamp — every metric this script derives
#                 comes from the FlowRun object, not from client-side curl
#                 timing.)
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

RUN_INDEX="${1:?run_index required}"
RESULTS_DIR="${2:?results_dir required}"
LOCAL_PORT="${3:?local_port required}"

TRIGGER_NAME="bench-webhook"
NS="${KUBEZAP_NS}"
RUN_TAG="$(printf 'run-%03d' "${RUN_INDEX}")"
OUT_FILE="${RESULTS_DIR}/kubezap-${RUN_TAG}.json"

VERSION="unknown"
[ -f "${BENCH_ROOT}/results/.kubezap-version" ] && VERSION="$(cat "${BENCH_ROOT}/results/.kubezap-version")"

before_names="$(kubectl get flowruns -n "${NS}" -l "kubezap.io/trigger=${TRIGGER_NAME}" \
  -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)"

run_started_at="$(now_iso)"

http_status="$(curl -s -o /dev/null -w '%{http_code}' -m 15 \
  -X POST "http://127.0.0.1:${LOCAL_PORT}/hooks/bench-webhook" \
  -H 'Content-Type: application/json' \
  -d "{\"bench_run\":${RUN_INDEX}}" || echo "000")"

# Find the FlowRun this firing created: the one name present now that wasn't
# in before_names. Sequential firing (METHODOLOGY.md §4.2) guarantees at most
# one new name appears.
new_name=""
waited=0
while [ "${waited}" -lt 30 ]; do
  current_names="$(kubectl get flowruns -n "${NS}" -l "kubezap.io/trigger=${TRIGGER_NAME}" \
    -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)"
  for n in ${current_names}; do
    if [[ ! " ${before_names} " == *" ${n} "* ]]; then
      new_name="${n}"
      break 2
    fi
  done
  sleep 1
  waited=$((waited + 1))
done

if [ -z "${new_name}" ]; then
  log_err "run ${RUN_INDEX}: no new FlowRun appeared within 30s (http_status=${http_status})"
  jq -n --arg system kubezap --argjson run_index "${RUN_INDEX}" \
    --arg run_started_at "${run_started_at}" --arg http_status "${http_status}" \
    '{system:$system, run_index:$run_index, run_started_at:$run_started_at, error:"no FlowRun created", http_status:$http_status}' \
    > "${OUT_FILE}"
  exit 1
fi

# Wait for terminal phase.
phase=""
waited=0
while [ "${waited}" -lt 90 ]; do
  phase="$(kubectl get flowrun "${new_name}" -n "${NS}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  case "${phase}" in
    Succeeded|Failed|Cancelled) break ;;
  esac
  sleep 1
  waited=$((waited + 1))
done

raw_json="$(kubectl get flowrun "${new_name}" -n "${NS}" -o json)"
run_completed_at="$(now_iso)"

# Derived fields per METHODOLOGY.md §3.1 and §3.3.
jq -n \
  --arg system "kubezap" \
  --arg version "${VERSION}" \
  --argjson run_index "${RUN_INDEX}" \
  --arg run_started_at "${run_started_at}" \
  --arg run_completed_at "${run_completed_at}" \
  --arg http_status "${http_status}" \
  --arg final_phase "${phase}" \
  --argjson raw "${raw_json}" \
  '
  # metav1.Time timestamps are always whole-second RFC3339 (no fractional
  # seconds), but strip any fractional part defensively before parsing in
  # case that ever changes upstream.
  def toEpoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
  def stepByName(n): ($raw.status.steps // []) | map(select(.name==n)) | first;
  ($raw.status.steps // []) as $steps
  | ($raw.metadata.creationTimestamp) as $trigger_fired
  | (stepByName("step1")) as $s1
  | (stepByName("step2")) as $s2
  | (stepByName("step3")) as $s3
  | {
      system: $system,
      version: $version,
      run_index: $run_index,
      run_started_at: $run_started_at,
      run_completed_at: $run_completed_at,
      http_status: $http_status,
      final_phase: $final_phase,
      raw: $raw,
      derived: {
        trigger_fired_at: $trigger_fired,
        first_step_started_at: ($s1.startTime // null),
        steps: {
          step1: {startTime: ($s1.startTime // null), completionTime: ($s1.completionTime // null), phase: ($s1.phase // null), attempts: ($s1.attempts // null)},
          step2: {startTime: ($s2.startTime // null), completionTime: ($s2.completionTime // null), phase: ($s2.phase // null), attempts: ($s2.attempts // null)},
          step3: {startTime: ($s3.startTime // null), completionTime: ($s3.completionTime // null), phase: ($s3.phase // null), attempts: ($s3.attempts // null)}
        },
        # METHODOLOGY.md §3.3(b): http-executor (internal/executor/http/handler.go,
        # confirmed at harness-build time) emits no per-request log line or trace
        # span with a timestamp, so the precise "RPC received" instant is not
        # available. Falling back to the documented weaker proxy: the full
        # step duration (target adds 0 latency, so no subtraction term applies).
        # This is *not* directly comparable to the Argo container-start
        # timestamp measurement — see execution_start_source.
        dispatch_to_execution_proxy_seconds: {
          step1: (if ($s1.startTime!=null and $s1.completionTime!=null) then (($s1.completionTime | toEpoch) - ($s1.startTime | toEpoch)) else null end),
          step2: (if ($s2.startTime!=null and $s2.completionTime!=null) then (($s2.completionTime | toEpoch) - ($s2.startTime | toEpoch)) else null end),
          step3: (if ($s3.startTime!=null and $s3.completionTime!=null) then (($s3.completionTime | toEpoch) - ($s3.startTime | toEpoch)) else null end)
        },
        execution_start_source: "proxy_no_executor_timestamp"
      }
    }
  ' > "${OUT_FILE}"

kubectl delete flowrun "${new_name}" -n "${NS}" --ignore-not-found >/dev/null 2>&1 || true

log_info "run ${RUN_INDEX}: flowrun=${new_name} phase=${phase} -> ${OUT_FILE}"
