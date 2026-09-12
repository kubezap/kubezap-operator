#!/usr/bin/env bash
# Generates a self-signed TLS cert for the controller-manager's admission
# webhook server and stores it as a Secret.
#
# The operator's ValidatingWebhookConfigurations are not currently wired to
# cert-manager (config/webhook and config/certmanager kustomize components
# are not scaffolded — see docs/design/2026-09-11-executor-egress-networkpolicy.md
# and docs/schedule.md), but cmd/main.go unconditionally starts a webhook TLS
# server on boot. Without a cert at --webhook-cert-path, the controller-manager
# crash-loops immediately on any `make deploy`. This script + the
# config/dev/manager_dev_patch.yaml overlay (or an equivalent patch in CI/e2e)
# is the local/dev/test workaround until that's fixed properly.
#
# NOT for production use.
set -euo pipefail

NAMESPACE="${1:-kubezap-system}"
SECRET_NAME="${2:-kubezap-webhook-certs}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$TMPDIR/tls.key" -out "$TMPDIR/tls.crt" -days 3650 \
  -subj "/CN=kubezap-webhook-service.${NAMESPACE}.svc" \
  -addext "subjectAltName=DNS:kubezap-webhook-service.${NAMESPACE}.svc,DNS:kubezap-webhook-service.${NAMESPACE}.svc.cluster.local" \
  >/dev/null 2>&1

kubectl create secret tls "$SECRET_NAME" -n "$NAMESPACE" \
  --cert="$TMPDIR/tls.crt" --key="$TMPDIR/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f -
