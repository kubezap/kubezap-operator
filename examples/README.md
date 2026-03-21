# KubeZap Examples

End-to-end examples demonstrating KubeZap automation patterns. Each example is
self-contained: a `README.md` with setup steps, plus the Kubernetes manifests
needed to run it.

---

## Prerequisites

- A running Kubernetes cluster with KubeZap installed — see
  [docs/overview.md](../docs/overview.md#installation)
- `kubectl` configured to reach the cluster
- The webhook gateway accessible (via `kubectl port-forward` or a LoadBalancer Service)

---

## Examples

| Example                                     | Trigger                       | What it demonstrates                                               |
| ------------------------------------------- | ----------------------------- | ------------------------------------------------------------------ |
| [order-router](order-router/)               | Webhook                       | Core feature tour: transform, CEL branching, MockEndpoints         |
| [kafka-enrichment](kafka-enrichment/)       | Kafka                         | Event enrichment pipeline: enrich → conditional route → re-publish |
| [incident-escalation](incident-escalation/) | Webhook                       | Parallel steps, wait/resume, conditional escalation                |
| [github-autolabel](github-autolabel/)       | Webhook (HMAC)                | HMAC auth, header extraction, GitHub API integration               |
| [slack-router](slack-router/)               | Webhook (HMAC + IP allowlist) | Form-encoded payload, multi-branch CEL routing, fire-and-forget    |

---

## Quick apply

Each example can be applied with:

```bash
kubectl apply -k examples/<name>/
```

And removed with:

```bash
kubectl delete -k examples/<name>/
```

Some examples require Secrets to be created manually before applying — see the
individual README for details.
