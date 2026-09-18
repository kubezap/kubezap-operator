#!/usr/bin/env bash
# Stands up the Kind cluster and both systems under test (KubeZap + Argo
# Workflows) plus the shared Mockoon target, per METHODOLOGY.md.
#
# Idempotent: safe to re-run (e.g. via run-benchmark.sh --skip-setup=false
# after a partial failure) — every step either creates-if-missing or applies
# manifests that are themselves idempotent.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

require_cmds kind kubectl docker jq curl make openssl

# ---------------------------------------------------------------------------
# 1. Kind cluster
# ---------------------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  log_info "Kind cluster '${CLUSTER_NAME}' already exists — reusing it"
else
  log_info "creating Kind cluster '${CLUSTER_NAME}'"
  kind create cluster --name "${CLUSTER_NAME}"
fi

log_info "writing kubeconfig for '${CLUSTER_NAME}' to ${BENCH_KUBECONFIG}"
mkdir -p "$(dirname "${BENCH_KUBECONFIG}")"
kind get kubeconfig --name "${CLUSTER_NAME}" > "${BENCH_KUBECONFIG}"

# ---------------------------------------------------------------------------
# 2. metrics-server, reconfigured per METHODOLOGY.md §3.2
# ---------------------------------------------------------------------------
if kubectl get deployment metrics-server -n kube-system >/dev/null 2>&1; then
  log_info "metrics-server already installed"
else
  log_info "installing metrics-server"
  kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
fi

if kubectl get deployment metrics-server -n kube-system \
    -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q kubelet-insecure-tls; then
  log_info "metrics-server already patched with --metric-resolution=15s/--kubelet-insecure-tls"
else
  log_info "patching metrics-server: --metric-resolution=15s (per METHODOLOGY.md §3.2), --kubelet-insecure-tls (required on Kind's self-signed kubelet certs)"
  kubectl patch deployment metrics-server -n kube-system --type=json -p='[
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"},
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--metric-resolution=15s"}
  ]'
fi

kubectl rollout status deployment/metrics-server -n kube-system --timeout=3m

log_info "waiting for metrics-server API to serve pod metrics"
for _ in $(seq 1 24); do
  if kubectl top pod -A >/dev/null 2>&1; then
    break
  fi
  sleep 5
done

# ---------------------------------------------------------------------------
# 3. Namespaces
# ---------------------------------------------------------------------------
kubectl create ns "${KUBEZAP_NS}" --dry-run=client -o yaml | kubectl apply -f -
kubectl create ns "${ARGO_NS}" --dry-run=client -o yaml | kubectl apply -f -

# ---------------------------------------------------------------------------
# 4. Build + load KubeZap images (mirrors test/e2e/e2e_suite_test.go)
# ---------------------------------------------------------------------------
pushd "${REPO_ROOT}" >/dev/null

log_info "building controller image ${CONTROLLER_IMG}"
make docker-build "IMG=${CONTROLLER_IMG}"

log_info "building webhook-gateway image ${WEBHOOK_GATEWAY_IMG}"
docker build -t "${WEBHOOK_GATEWAY_IMG}" -f cmd/webhook-gateway/Dockerfile .

log_info "building http-executor image ${HTTP_EXECUTOR_IMG}"
docker build -t "${HTTP_EXECUTOR_IMG}" -f cmd/http-executor/Dockerfile .

for img in "${CONTROLLER_IMG}" "${WEBHOOK_GATEWAY_IMG}" "${HTTP_EXECUTOR_IMG}"; do
  log_info "loading ${img} into Kind cluster '${CLUSTER_NAME}'"
  kind load docker-image "${img}" --name "${CLUSTER_NAME}"
done

# ---------------------------------------------------------------------------
# 5. Install KubeZap CRDs + controller
#
# `make install`/`make deploy` depend on the `manifests` Make target (CRD/RBAC
# codegen from api/v1alpha1 markers). This harness does not touch any file
# under api/v1alpha1, so that codegen step is a no-op re-run against unchanged
# Go source — identical to what test/e2e/e2e_suite_test.go already does on
# every e2e run. This harness never invokes `make generate`/`make manifests`
# directly itself.
# ---------------------------------------------------------------------------
log_info "installing KubeZap CRDs"
make install

# Pre-existing gap discovered while building this harness (2026-09-13, not
# introduced by this benchmark and not fixed here — out of this harness's
# file footprint): config/crd/kustomization.yaml's `resources:` list is
# missing bases/automation.kubezap.io_webhookgatewayconfigs.yaml, so
# `make install` (kustomize build config/crd) never installs the
# WebhookGatewayConfig CRD. Without it, ExecutorReconciler/TriggerReconciler
# fail every reconcile with "no matches for kind WebhookGatewayConfigList"
# and no Trigger in the cluster — not just this benchmark's — ever reaches
# Accepted. Apply the CRD directly from its already-generated bases file as
# a live workaround so this harness can run against current main; this does
# not touch api/v1alpha1, any controller, or config/crd/kustomization.yaml
# itself. Report this gap upstream as its own Story — do not let this
# workaround hide it.
if ! kubectl get crd webhookgatewayconfigs.automation.kubezap.io >/dev/null 2>&1; then
  log_warn "WebhookGatewayConfig CRD missing after 'make install' (config/crd/kustomization.yaml gap) — applying it directly as a workaround"
  kubectl apply -f "${REPO_ROOT}/config/crd/bases/automation.kubezap.io_webhookgatewayconfigs.yaml"
  kubectl wait crd/webhookgatewayconfigs.automation.kubezap.io --for=condition=Established --timeout=60s
fi

for crd in flows.automation.kubezap.io flowruns.automation.kubezap.io \
           integrations.automation.kubezap.io triggers.automation.kubezap.io; do
  kubectl wait "crd/${crd}" --for=condition=Established --timeout=2m
done

log_info "deploying KubeZap controller manager"
make deploy "IMG=${CONTROLLER_IMG}" \
  "WEBHOOK_GATEWAY_IMAGE=${WEBHOOK_GATEWAY_IMG}"

log_info "scoping controller to WATCH_NAMESPACES=${KUBEZAP_NS} (least-privilege SingleNamespace mode)"
kubectl set env deployment/kubezap-controller-manager -n kubezap-system \
  "WATCH_NAMESPACES=${KUBEZAP_NS}"

# cmd/main.go self-provisions its own admission webhook TLS cert on boot
# (internal/webhookcerts) — no manual cert generation or patching needed. See
# docs/design/2026-09-18-self-managed-webhook-certs.md.

# The scenario's HTTP steps target an in-cluster Service DNS name; the
# controller's SSRF blocklist rejects that by default. Mirrors
# test/e2e/e2e_suite_test.go's --ssrf-allow-in-cluster=true flag.
if ! kubectl get deployment/kubezap-controller-manager -n kubezap-system \
    -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q ssrf-allow-in-cluster; then
  log_info "enabling --ssrf-allow-in-cluster so the benchmark's in-cluster HTTP target is permitted"
  kubectl patch deployment/kubezap-controller-manager -n kubezap-system --type=json -p='[
    {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--ssrf-allow-in-cluster=true"}
  ]'
fi

log_info "waiting for controller manager to be Ready"
kubectl rollout status deployment/kubezap-controller-manager -n kubezap-system --timeout=3m

popd >/dev/null

# ---------------------------------------------------------------------------
# 6. Shared Mockoon fixture + KubeZap Flow/Trigger
# ---------------------------------------------------------------------------
log_info "applying Mockoon fixture and KubeZap Flow/Trigger into ${KUBEZAP_NS}"
kubectl apply -f "${MANIFESTS_DIR}/mockoon-configmap.yaml"
kubectl apply -f "${MANIFESTS_DIR}/mockoon-deployment.yaml"
kubectl apply -f "${MANIFESTS_DIR}/mockoon-service.yaml"
kubectl apply -f "${MANIFESTS_DIR}/kubezap-flow.yaml"
kubectl apply -f "${MANIFESTS_DIR}/kubezap-trigger.yaml"

log_info "waiting for Mockoon to be available"
kubectl wait deployment/bench-mockoon -n "${KUBEZAP_NS}" --for=condition=Available --timeout=3m

log_info "waiting for bench-webhook Trigger to be Accepted"
for _ in $(seq 1 40); do
  status="$(kubectl get trigger bench-webhook -n "${KUBEZAP_NS}" \
    -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}' 2>/dev/null || true)"
  [ "$status" = "True" ] && break
  sleep 3
done
if [ "$status" != "True" ]; then
  log_err "bench-webhook Trigger never reached Accepted"
  exit 1
fi

log_info "waiting for the webhook gateway Deployment"
kubectl wait deployment/kubezap-webhook-gateway -n "${KUBEZAP_NS}" --for=condition=Available --timeout=3m

log_info "waiting for the http-executor Deployment"
kubectl wait deployment/kubezap-http-executor -n "${KUBEZAP_NS}" --for=condition=Available --timeout=3m

# Record the actually-running controller image reference (§5.3 requires the
# *actually installed* version string, not the pin).
kubectl get deployment/kubezap-controller-manager -n kubezap-system \
  -o jsonpath='{.spec.template.spec.containers[0].image}' > "${BENCH_ROOT}/results/.kubezap-version"

# ---------------------------------------------------------------------------
# 7. Argo Workflows, pinned version (re-check METHODOLOGY.md §2's confidence
#    flag before relying on this pin for a future run — it may be stale).
# ---------------------------------------------------------------------------
ARGO_INSTALL_URL="https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION}/namespace-install.yaml"
# Keyed on the workflows.argoproj.io CRD, not the workflow-controller
# Deployment: a prior partial failure (see the --server-side comment below)
# can leave the Deployment/RBAC/etc. created while the CRDs — applied later
# in the same manifest — never landed, which a Deployment-only check would
# miss on a re-run.
if kubectl get crd workflows.argoproj.io >/dev/null 2>&1; then
  log_info "Argo Workflows CRDs already installed"
else
  log_info "installing Argo Workflows ${ARGO_VERSION} (namespace-install) into ${ARGO_NS}"
  # --server-side is required, not optional: Argo's CRDs (workflows.argoproj.io
  # etc.) carry a large embedded OpenAPI schema, and a plain `kubectl apply`
  # writes a kubectl.kubernetes.io/last-applied-configuration annotation that
  # exceeds the API server's 256KiB annotation size limit for these specific
  # CRDs — apply fails with "metadata.annotations: Too long" for exactly the
  # 4 largest Argo CRDs while every other object in the manifest applies fine.
  # Server-side apply tracks field ownership instead of that annotation, which
  # is the officially recommended way to install Argo's manifests. This was
  # discovered live while validating this harness end-to-end.
  kubectl apply --server-side --force-conflicts -n "${ARGO_NS}" -f "${ARGO_INSTALL_URL}"
fi

# Known upstream gap in namespace-install.yaml's RBAC — see the manifest's
# own header comment for the full explanation and the upstream issue link.
# Idempotent: a Role/RoleBinding apply is safe to repeat.
log_info "applying workflowtaskresults RBAC workaround (known Argo namespace-install gap, argoproj/argo-workflows#8068)"
kubectl apply -f "${MANIFESTS_DIR}/argo-workflowtaskresults-rbac.yaml"

log_info "waiting for Argo workflow-controller to be Ready"
kubectl rollout status deployment/workflow-controller -n "${ARGO_NS}" --timeout=3m

# Record the actually-installed Argo version string (§5.3).
kubectl get deployment/workflow-controller -n "${ARGO_NS}" \
  -o jsonpath='{.spec.template.spec.containers[0].image}' > "${BENCH_ROOT}/results/.argo-version"

log_info "setup complete"
