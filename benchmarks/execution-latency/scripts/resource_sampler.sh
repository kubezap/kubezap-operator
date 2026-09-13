#!/usr/bin/env bash
# Background resource-overhead sampler, per METHODOLOGY.md §3.2.
#
# Polls `kubectl top pod` every 5s (chosen to sample faster than the 15s
# metrics-server resolution set up in setup.sh, without hammering the API
# server for no benefit) across the *entire* sequential run of all N firings
# for one system, and separately tracks every distinct pod name that ever
# existed (via `kubectl get pods`) vs. every distinct pod name ever captured
# by a `top pod` sample, per §3.2's capture-rate requirement.
#
# Usage: resource_sampler.sh <namespace> <output_prefix>
#   Runs until it receives SIGTERM. Writes:
#     <output_prefix>.samples.jsonl   - one JSON object per poll per pod
#     <output_prefix>.created.txt     - union of pod names ever observed to exist
#     <output_prefix>.captured.txt    - union of pod names ever seen in a top-pod sample
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

NS="${1:?namespace required}"
OUT_PREFIX="${2:?output prefix required}"
INTERVAL_S="${3:-5}"

SAMPLES_FILE="${OUT_PREFIX}.samples.jsonl"
CREATED_FILE="${OUT_PREFIX}.created.txt"
CAPTURED_FILE="${OUT_PREFIX}.captured.txt"

: > "${SAMPLES_FILE}"
: > "${CREATED_FILE}"
: > "${CAPTURED_FILE}"

_stop=0
trap '_stop=1' TERM INT

log_info "resource sampler started for ns=${NS} interval=${INTERVAL_S}s -> ${OUT_PREFIX}.*"

while [ "${_stop}" -eq 0 ]; do
  ts="$(now_iso)"

  # Every pod that currently exists in the namespace (created or not yet GC'd).
  kubectl get pods -n "${NS}" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
    >> "${CREATED_FILE}" || true

  # `kubectl top pod` fails loudly if a pod has no metrics yet (or has already
  # terminated) — that's expected and exactly the capture-rate gap §3.2 asks
  # us to measure, not a script bug, so failures here are swallowed.
  top_out="$(kubectl top pod -n "${NS}" --no-headers 2>/dev/null || true)"
  if [ -n "${top_out}" ]; then
    while IFS= read -r line; do
      [ -z "$line" ] && continue
      pod="$(awk '{print $1}' <<<"$line")"
      cpu="$(awk '{print $2}' <<<"$line")"
      mem="$(awk '{print $3}' <<<"$line")"
      printf '%s\n' "$pod" >> "${CAPTURED_FILE}"
      jq -n --arg ts "$ts" --arg pod "$pod" --arg cpu "$cpu" --arg mem "$mem" \
        '{timestamp:$ts,pod:$pod,cpu:$cpu,memory:$mem}' >> "${SAMPLES_FILE}"
    done <<< "$top_out"
  fi

  for _ in $(seq 1 "${INTERVAL_S}"); do
    [ "${_stop}" -eq 1 ] && break
    sleep 1
  done
done

log_info "resource sampler stopping for ns=${NS}"

# finalize: sort+dedupe the created/captured sets in place so the wrapper
# script can just count lines.
sort -u -o "${CREATED_FILE}" "${CREATED_FILE}"
sort -u -o "${CAPTURED_FILE}" "${CAPTURED_FILE}"
