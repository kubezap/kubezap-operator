#!/usr/bin/env bash
#
# check-rbac-parity.sh — verify the operator's reconciler RBAC permissions
# are identical across all three install paths that hand- or generator-
# maintain a copy of them:
#
#   1. config/rbac/generated/role.yaml           (canonical: controller-gen
#      output from +kubebuilder:rbac markers — see the Makefile's `manifests`
#      target for why this is redirected out of config/rbac/ itself)
#   2. config/rbac/namespaced_role.yaml           (raw-manifest/kustomize's
#      hand-mirrored Role — actually deployed)
#   3. charts/kubezap-operator/templates/role.yaml (Helm's hand-templated
#      per-namespace Role — actually deployed; requires `helm template` to
#      render before its rules can be extracted)
#
# All three are hand- or tool-authored independently and must describe the
# exact same permission set, even though rule ordering and within-rule list
# ordering (verbs/resources/apiGroups) may legitimately differ. This script
# normalizes each source (sorting verbs/resources/apiGroups within a rule,
# then sorting the overall rule list by a canonical serialization of each
# rule) and diffs all three pairwise.
#
# Requires: yq (mikefarah/yq, v4+), helm. Both are preinstalled on GitHub's
# ubuntu-latest runners (see actions/runner-images' published software
# manifest) — no separate install step should be needed in CI, but the CI
# job installs a pinned Helm version anyway for reproducibility, matching
# this repo's existing convention in .github/workflows/release.yml.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

GENERATED_ROLE="config/rbac/generated/role.yaml"
NAMESPACED_ROLE="config/rbac/namespaced_role.yaml"
HELM_CHART="charts/kubezap-operator"

for bin in yq helm; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "::error::$bin is required by hack/check-rbac-parity.sh but was not found on PATH." >&2
    exit 1
  fi
done

for f in "$GENERATED_ROLE" "$NAMESPACED_ROLE"; do
  if [ ! -f "$f" ]; then
    echo "::error::$f not found. Run 'make manifests' first (for $GENERATED_ROLE) or check the repo layout." >&2
    exit 1
  fi
done

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

# normalize_rules FILE_PATH OUT_PATH
#
# Extracts .rules from a single-document Role/ClusterRole manifest, sorts
# each rule's verbs/resources/apiGroups/resourceNames arrays, then sorts the
# overall rule list by each rule's canonical JSON serialization — so
# differences in authoring order (which rule comes first, or which verb is
# listed first within a rule) never cause a false-positive mismatch. Only
# genuine differences in WHICH permissions are granted should survive this
# normalization.
normalize_rules() {
  local src="$1" out="$2"
  yq -o=json '
    .rules
    | map({
        "apiGroups":    ((.apiGroups    // []) | sort),
        "resources":    ((.resources    // []) | sort),
        "verbs":        ((.verbs        // []) | sort),
        "resourceNames": ((.resourceNames // []) | sort)
      })
    | sort_by(. | to_json)
  ' "$src" > "$out"
}

# normalize_rules_multidoc FILE_PATH OUT_PATH
#
# Same normalization, but for a rendered multi-document YAML stream (Helm's
# `helm template` output): finds the first `kind: Role` document in the
# stream (the chart renders one Role+RoleBinding pair per watched namespace,
# all identical, so any one is representative) and normalizes its rules.
# Uses `yq eval-all` (not a plain per-document `yq eval`) so `select()`
# collects across the whole stream into one list before `.[0]` picks the
# first match — a plain `yq eval` instead applies the pipeline
# independently to every document, which silently produces one (mostly
# empty) result per document rather than a single combined one.
normalize_rules_multidoc() {
  local src="$1" out="$2"
  yq ea -o=json '
    [select(.kind == "Role")]
    | .[0].rules
    | map({
        "apiGroups":    ((.apiGroups    // []) | sort),
        "resources":    ((.resources    // []) | sort),
        "verbs":        ((.verbs        // []) | sort),
        "resourceNames": ((.resourceNames // []) | sort)
      })
    | sort_by(. | to_json)
  ' "$src" > "$out"
}

echo "Extracting and normalizing RBAC rules from all three sources..."

normalize_rules "$GENERATED_ROLE" "$WORKDIR/generated.json"

normalize_rules "$NAMESPACED_ROLE" "$WORKDIR/namespaced.json"

helm template "$HELM_CHART" > "$WORKDIR/helm-rendered.yaml"
normalize_rules_multidoc "$WORKDIR/helm-rendered.yaml" "$WORKDIR/helm.json"

# Pretty-print for stable, readable diffs.
yq -P "$WORKDIR/generated.json"  > "$WORKDIR/generated.pretty.yaml"
yq -P "$WORKDIR/namespaced.json" > "$WORKDIR/namespaced.pretty.yaml"
yq -P "$WORKDIR/helm.json"       > "$WORKDIR/helm.pretty.yaml"

status=0

compare() {
  local key="$1" label_a="$2" file_a="$3" label_b="$4" file_b="$5"
  local diff_out="$WORKDIR/diff.$key.txt"
  if ! diff -u "$file_a" "$file_b" > "$diff_out"; then
    echo "::error::RBAC rule mismatch between $label_a and $label_b" >&2
    echo "--- $label_a" >&2
    echo "+++ $label_b" >&2
    cat "$diff_out" >&2
    echo "" >&2
    status=1
  fi
}

compare "generated-vs-namespaced" \
        "generated (config/rbac/generated/role.yaml)" "$WORKDIR/generated.pretty.yaml" \
        "namespaced (config/rbac/namespaced_role.yaml)" "$WORKDIR/namespaced.pretty.yaml"

compare "generated-vs-helm" \
        "generated (config/rbac/generated/role.yaml)" "$WORKDIR/generated.pretty.yaml" \
        "helm (charts/kubezap-operator)" "$WORKDIR/helm.pretty.yaml"

compare "namespaced-vs-helm" \
        "namespaced (config/rbac/namespaced_role.yaml)" "$WORKDIR/namespaced.pretty.yaml" \
        "helm (charts/kubezap-operator)" "$WORKDIR/helm.pretty.yaml"

if [ "$status" -ne 0 ]; then
  echo "::error::RBAC parity check failed. config/rbac/generated/role.yaml, config/rbac/namespaced_role.yaml, and charts/kubezap-operator/templates/role.yaml must all grant identical permissions. See the rule diffs above for exactly what diverged." >&2
  exit 1
fi

echo "RBAC parity check passed: config/rbac/generated/role.yaml, config/rbac/namespaced_role.yaml, and charts/kubezap-operator/templates/role.yaml all grant identical permissions."
