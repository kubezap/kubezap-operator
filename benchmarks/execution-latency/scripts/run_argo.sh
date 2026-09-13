#!/usr/bin/env bash
# Submits one Workflow (from manifests/argo-workflow-template.yaml) against
# the Argo arm, waits for it to reach a terminal phase, and writes one
# raw-result JSON file, per METHODOLOGY.md §4.3.
#
# Usage: run_argo.sh <run_index> <results_dir>
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

RUN_INDEX="${1:?run_index required}"
RESULTS_DIR="${2:?results_dir required}"

NS="${ARGO_NS}"
RUN_TAG="$(printf 'run-%03d' "${RUN_INDEX}")"
WF_NAME="bench-argo-${RUN_TAG}"
OUT_FILE="${RESULTS_DIR}/argo-${RUN_TAG}.json"

VERSION="unknown"
[ -f "${BENCH_ROOT}/results/.argo-version" ] && VERSION="$(cat "${BENCH_ROOT}/results/.argo-version")"

TMP_WF="$(mktemp)"
trap 'rm -f "${TMP_WF}"' EXIT
sed "s/__RUN_NAME__/${WF_NAME}/" "${MANIFESTS_DIR}/argo-workflow-template.yaml" > "${TMP_WF}"

run_started_at="$(now_iso)"
kubectl apply -f "${TMP_WF}" >/dev/null

# Wait for terminal phase.
phase=""
waited=0
while [ "${waited}" -lt 90 ]; do
  phase="$(kubectl get workflow "${WF_NAME}" -n "${NS}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  case "${phase}" in
    Succeeded|Failed|Error) break ;;
  esac
  sleep 1
  waited=$((waited + 1))
done

if [ -z "${phase}" ]; then
  log_err "run ${RUN_INDEX}: workflow ${WF_NAME} never reported a phase within 90s"
  jq -n --arg system argo --argjson run_index "${RUN_INDEX}" \
    --arg run_started_at "${run_started_at}" --arg name "${WF_NAME}" \
    '{system:$system, run_index:$run_index, run_started_at:$run_started_at, error:"workflow never reported phase", workflow_name:$name}' \
    > "${OUT_FILE}"
  exit 1
fi

raw_json="$(kubectl get workflow "${WF_NAME}" -n "${NS}" -o json)"
pods_json="$(kubectl get pods -n "${NS}" -l "workflows.argoproj.io/workflow=${WF_NAME}" -o json)"
run_completed_at="$(now_iso)"

# Derived fields per METHODOLOGY.md §3.1 and §3.3.
jq -n \
  --arg system "argo" \
  --arg version "${VERSION}" \
  --argjson run_index "${RUN_INDEX}" \
  --arg run_started_at "${run_started_at}" \
  --arg run_completed_at "${run_completed_at}" \
  --arg final_phase "${phase}" \
  --argjson raw "${raw_json}" \
  --argjson pods "${pods_json}" \
  '
  # status.nodes is a map keyed by opaque node ID. Filter to actual step pods
  # (type: Pod, templateName: curl-ok) per METHODOLOGY.md §3.1 — this drops
  # the outer "main" steps-template node and the top-level Workflow entry
  # node, which would otherwise pollute the per-step sample set with
  # whole-workflow durations.
  ($raw.status.nodes // {}) as $nodes
  | ($nodes | to_entries | map(select(.value.type=="Pod" and .value.templateName=="curl-ok"))) as $stepNodeEntries
  | (reduce $stepNodeEntries[] as $e ({}; . + {($e.value.displayName): $e.value})) as $byDisplayName
  | ($raw.metadata.creationTimestamp) as $wf_created
  | ($pods.items // []) as $podItems
  # Join each step node to its Pod via the workflows.argoproj.io/node-id
  # annotation (the documented, stable way to map a node ID to a pod name;
  # pod name == node ID only holds for short workflow/step names and is not
  # relied on here).
  | (reduce $stepNodeEntries[] as $e ({};
      . + {
        ($e.key): (
          $podItems
          | map(select(.metadata.annotations["workflows.argoproj.io/node-id"] == $e.key))
          | first
        )
      }
    )) as $podByNodeId
  | {
      system: $system,
      version: $version,
      run_index: $run_index,
      run_started_at: $run_started_at,
      run_completed_at: $run_completed_at,
      final_phase: $final_phase,
      raw: $raw,
      raw_pods: $pods,
      derived: {
        workflow_created_at: $wf_created,
        first_step_started_at: ($byDisplayName.step1.startedAt // null),
        steps: (
          ["step1","step2","step3"] | map({
            (.): (
              ($byDisplayName[.]) as $n
              | ($n.id) as $nid
              | (if $nid == null then null else $podByNodeId[$nid] end) as $pod
              | {
                  startedAt: ($n.startedAt // null),
                  finishedAt: ($n.finishedAt // null),
                  phase: ($n.phase // null),
                  pod_name: ($pod.metadata.name // null),
                  # METHODOLOGY.md §3.3(b): the pod-scheduling + image-pull +
                  # container-start tax Argo pays per step. `main`
                  # specifically, not `wait`/`init`.
                  #
                  # Deviation from the METHODOLOGY.md §3.3(b) literal
                  # `.state.running.startedAt` jsonpath example: this harness
                  # only fetches pod status *after* the Workflow has already
                  # reached a terminal phase (per the §4.2 wait-for-terminal-
                  # before-next-firing design), by which point a steps main
                  # container has almost always already exited — so
                  # `.state.running` is null and `.state.terminated` is set
                  # instead. Both ContainerStateRunning and
                  # ContainerStateTerminated carry a `startedAt` field marking
                  # the same instant (kubelet copies it forward at the
                  # Running->Terminated transition, not recomputed), so
                  # falling back to `.state.terminated.startedAt` preserves
                  # exactly what §3.3(b) is measuring.
                  main_container_started_at: (
                    ($pod.status.containerStatuses // [])
                    | map(select(.name=="main"))
                    | first
                    | (.state.running.startedAt // .state.terminated.startedAt // null)
                  )
                }
            )
          }) | add
        ),
        execution_start_source: "pod_main_container_running_startedAt"
      }
    }
  ' > "${OUT_FILE}"

kubectl delete workflow "${WF_NAME}" -n "${NS}" --ignore-not-found >/dev/null 2>&1 || true

log_info "run ${RUN_INDEX}: workflow=${WF_NAME} phase=${phase} -> ${OUT_FILE}"
