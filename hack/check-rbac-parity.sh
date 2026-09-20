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
# exact same permission set, even though the source files may express it
# differently: rule ordering, within-rule list ordering, and even how
# resources/verbs are grouped into rule objects can legitimately differ
# (e.g. controller-gen merges resources that happen to share an identical
# verb set into one rule, where a hand-authored file lists them as separate
# rules — same effective permissions, different rule shape). To be immune to
# all of that, this script flattens every source down to its atomic set of
# granted (apiGroup, resource, verb, resourceNames) permission tuples —
# which is what RBAC actually enforces — sorts and deduplicates that flat
# set, and diffs the three flat sets pairwise. Comparing at the rule-object
# level instead (just sorting rules and their internal lists) was tried
# first and produced false-positive failures purely from this
# same-permissions-different-grouping pattern; flattening to tuples is what
# actually captures "same permission set" as the story requires.
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

# ALLOWLIST_FILTER — rules that are PERMANENT, by-design divergences between
# config/rbac/generated/role.yaml (canonical, from +kubebuilder:rbac markers)
# and the two actually-deployed, namespace-scoped sources
# (config/rbac/namespaced_role.yaml and the Helm chart's Role). Matched by
# rule content (apiGroups/resources), not by which file they came from, so
# the same filter applies uniformly to all three normalized rulesets before
# comparison. Anything selected out here is expected to appear ONLY in
# config/rbac/generated/role.yaml and never in the other two — that is not a
# bug for this check to catch.
#
#   1. admissionregistration.k8s.io/validatingwebhookconfigurations — this
#      permission is cluster-scoped (a Role/namespaced install can never
#      grant it) and is instead supplied via the separate, always-applied
#      config/rbac/webhook_cert_role.yaml + webhook_cert_role_binding.yaml
#      ClusterRole pair (marker: internal/webhookcerts/webhookcerts.go:112).
#      This is pre-existing precedent, not introduced by this check.
#
#   2. apiGroups: ["*"], resources: ["*"], verbs: [get,list,watch] — backs
#      the alpha "Resource" trigger type's dynamic informers (marker:
#      internal/controller/resource_watcher.go:57). By explicit product
#      decision this is NOT granted by default in the deployed
#      Role/Helm chart — it is opt-in only (see the Resource-trigger opt-in
#      RBAC example and docs, provided separately). Its absence from
#      namespaced_role.yaml/the Helm chart is intentional, not drift.
# NOTE: literals are deliberately placed on the LEFT of every `==` below
# (e.g. `"*" == .apiGroups[0]`, not `.apiGroups[0] == "*"`). yq's `==`
# operator does glob-style matching on its right-hand operand, so a literal
# "*" on the right would match every apiGroups/resources value, not just a
# literal "*" — silently allowlisting every rule. Array-vs-array equality
# (`.apiGroups == ["*"]`) has the same problem one level down and is avoided
# here too; length + indexed element comparison sidesteps both.
ALLOWLIST_FILTER='
  (
    (.apiGroups | length) == 1 and ("admissionregistration.k8s.io" == .apiGroups[0])
    and (.resources | length) == 1 and ("validatingwebhookconfigurations" == .resources[0])
  )
  or (
    (.apiGroups | length) == 1 and ("*" == .apiGroups[0])
    and (.resources | length) == 1 and ("*" == .resources[0])
  )
'
# NOTE: every call site wraps this in an extra pair of parens before piping
# to `| not` — i.e. `select( ($ALLOWLIST_FILTER) | not )`, not
# `select($ALLOWLIST_FILTER | not)`. Without the extra parens, yq's `|`
# binds tighter than the trailing `or` clause above, so `not` silently
# applies only to the second `or` operand instead of the whole expression,
# and the first allowlisted rule stops being filtered out. Verified by hand
# before relying on it — this is not a hypothetical gotcha.

# FLATTEN_EXPR — takes a (post-allowlist-filter) rule array on the pipeline
# and expands it to a flat array of "apiGroup|resource|verb|resourceNames"
# strings, one per atomic granted permission (the cross product of each
# rule's apiGroups x resources x verbs; resourceNames — unused by any rule
# in any of these three sources today — is folded in as a sorted,
# comma-joined suffix so a future rule that does scope by resourceNames
# still compares correctly instead of being silently ignored).
FLATTEN_EXPR='
  [
    .[] as $rule
    | ($rule.resourceNames // [] | sort | join(",")) as $names
    | $rule.apiGroups[] as $group
    | $rule.resources[] as $res
    | $rule.verbs[] as $verb
    | ($group + "|" + $res + "|" + $verb + "|" + $names)
  ]
  | sort
  | unique
'

# normalize_rules FILE_PATH OUT_PATH
#
# Extracts .rules from a single-document Role/ClusterRole manifest, drops
# the permanently-allowlisted rules described above, then flattens what's
# left to the sorted, deduplicated set of atomic permission tuples described
# above. Only genuine differences in WHICH permissions are granted survive
# this normalization — not rule ordering, not within-rule list ordering, and
# not how resources/verbs happen to be grouped into rule objects.
normalize_rules() {
  local src="$1" out="$2"
  yq -o=json '
    .rules
    | map(select( ('"$ALLOWLIST_FILTER"') | not))
    | '"$FLATTEN_EXPR"'
  ' "$src" > "$out"
}

# normalize_rules_multidoc FILE_PATH OUT_PATH
#
# Same normalization (allowlist filter + flatten to permission tuples), but
# for a rendered multi-document YAML stream (Helm's `helm template` output):
# finds the first `kind: Role` document in the stream (the chart renders one
# Role+RoleBinding pair per watched namespace, all identical, so any one is
# representative) and normalizes its rules. Uses `yq eval-all` (not a plain
# per-document `yq eval`) so `select()` collects across the whole stream into
# one list before `.[0]` picks the first match — a plain `yq eval` instead
# applies the pipeline independently to every document, which silently
# produces one (mostly empty) result per document rather than a single
# combined one.
normalize_rules_multidoc() {
  local src="$1" out="$2"
  yq ea -o=json '
    [select(.kind == "Role")]
    | .[0].rules
    | map(select( ('"$ALLOWLIST_FILTER"') | not))
    | '"$FLATTEN_EXPR"'
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
