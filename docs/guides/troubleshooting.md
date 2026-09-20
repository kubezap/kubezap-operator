# Troubleshooting

This guide covers the most common issues when running KubeZap. Each section describes symptoms, how to diagnose, and how to fix.

---

## Contents

- [Controller not starting](#controller-not-starting)
- [Trigger not being accepted](#trigger-not-being-accepted)
- [Webhook not receiving requests](#webhook-not-receiving-requests)
- [FlowRun not being created](#flowrun-not-being-created)
- [FlowRun stuck in Running](#flowrun-stuck-in-running)
- [FlowRun stuck in Waiting](#flowrun-stuck-in-waiting)
- [Step failing unexpectedly](#step-failing-unexpectedly)
- [CEL expression errors](#cel-expression-errors)
- [Mockoon not receiving requests](#mockoon-not-receiving-requests)
- [Kafka gateway not consuming messages](#kafka-gateway-not-consuming-messages)
- [RBAC and permission errors](#rbac-and-permission-errors)
- [Using the kubezap CLI for debugging](#using-the-kubezap-cli-for-debugging)
- [Getting Help](#getting-help)

---

## Controller not starting

**Symptom**: The `kubezap-controller-manager` pod is in `CrashLoopBackOff` or `Pending`.

**Step 1 — Check pod status and events:**
```bash
kubectl get pods -n kubezap-system
kubectl describe pod -n kubezap-system -l control-plane=controller-manager
```

**Step 2 — Check controller logs:**
```bash
kubectl logs -n kubezap-system -l control-plane=controller-manager --previous
```

**Common causes:**

| Symptom in logs                                                | Fix                                                                    |
| -------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `failed to get API group resources`                            | CRDs not installed. Run `kubectl apply -k config/crd`                  |
| `leader election failed`                                       | Multiple replicas, no leader-election lease. Set `--leader-elect=true` |
| `forbidden: User ... cannot watch ...`                         | RBAC not applied. Run `kubectl apply -k config/rbac`                   |
| `no endpoints available for service "kubezap-webhook-service"` | Service not created. Check kustomize apply output.                     |

---

## Trigger not being accepted

**Symptom**: `kubectl get trigger <name>` shows `Accepted: False` or no conditions.

**Step 1 — Check conditions:**
```bash
kubectl get trigger <name> -o jsonpath='{.status.conditions}' | jq .
```

**Step 2 — Check controller logs for this trigger:**
```bash
kubectl logs -n kubezap-system -l control-plane=controller-manager | grep <trigger-name>
```

**Common causes:**

- **Invalid cron expression**: The `spec.cron.schedule` field failed validation. The condition message will contain the parsing error.
- **Referenced Flow not found**: The Flow named by `flowRef` must exist in the same namespace as the Trigger. Create the Flow first or check the namespace.
- **Integration not ready**: For `kafka`, `amqp`, and `nats` triggers, the referenced Integration must have a `Ready: True` condition.

```bash
# Check the referenced Flow exists
kubectl get flow <flow-name> -n <namespace>

# Check the referenced Integration is ready
kubectl get integration <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```

---

## Webhook not receiving requests

**Symptom**: Sending a request to `/hooks/<path>` returns `404 Not Found`.

**Step 1 — Verify the gateway is running:**
```bash
kubectl get deployment kubezap-webhook-gateway -n <namespace>
kubectl get pods -l app.kubernetes.io/component=webhook-gateway -n <namespace>
```

**Step 2 — Verify the Trigger is accepted:**
```bash
kubectl get trigger <name> -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Must be: True
```

**Step 3 — Check gateway logs for route registration:**
```bash
kubectl logs -l app.kubernetes.io/component=webhook-gateway -n <namespace> | grep "registered route\|deregistered\|<your-path>"
```

**Step 4 — Verify the path matches exactly:**

The `spec.webhook.path` value must start with `/`. Requests must use the exact path, including any prefix. For example, `spec.webhook.path: /hooks/orders` is reached at `http://<gateway>/hooks/orders` — not `/hooks/orders/` (trailing slash is different).

**Step 5 — Verify connectivity:**
```bash
# If testing from outside the cluster, ensure port-forward or Ingress is set up
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n <namespace>
curl -v http://localhost:8080/hooks/<your-path>
```

If `404` is returned and the trigger is `Accepted: True`, restart the gateway pod to force re-registration:
```bash
kubectl rollout restart deployment/kubezap-webhook-gateway -n <namespace>
```

---

## FlowRun not being created

**Symptom**: Request to webhook gateway returns `200`/`202` but no FlowRun appears.

**Step 1 — Check gateway logs:**
```bash
kubectl logs -l app.kubernetes.io/component=webhook-gateway -n <namespace> | tail -50
```

Look for `flowrun_create` entries or errors like `forbidden` / `failed to create FlowRun`.

**Step 2 — Check auth failures:**

If auth is configured on the Trigger, a failed auth check suppresses FlowRun creation. The gateway returns `401` on auth failure — check the HTTP response code.

```bash
# Auth failure metrics
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n <namespace>
curl -s http://localhost:8080/metrics | grep kubezap_webhook_auth_failures
```

**Step 3 — Check gateway RBAC:**
```bash
# Can the gateway ServiceAccount create FlowRuns?
kubectl auth can-i create flowruns \
  --as=system:serviceaccount:<namespace>:kubezap-webhook-gateway \
  -n <namespace>
# Expected: yes
```

If this returns `no`, the gateway Role is missing. See [Architecture → Gateway RBAC](../architecture.md#gateway-serviceaccount-and-rbac).

---

## FlowRun stuck in Running

**Symptom**: `kubectl get flowruns` shows a FlowRun in `Running` phase for longer than expected.

**Step 1 — Identify which step is stuck:**
```bash
kubezap history <flowrun-name>
# or:
kubectl get flowrun <name> -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
```

**Step 2 — Check controller logs for that FlowRun:**
```bash
kubectl logs -n kubezap-system -l control-plane=controller-manager | grep <flowrun-name>
```

**Common causes:**

| Cause                                           | Fix                                                                                                                                          |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| HTTP step calling an unreachable URL            | Verify the URL is reachable from inside the pod. Test with `kubectl run -it --rm --restart=Never --image=curlimages/curl test -- curl <url>` |
| HTTP step `timeoutSeconds` too long             | Add or reduce `spec.steps[*].action.http.timeoutSeconds`                                                                                     |
| Flow-level `timeout` not set                    | Add `spec.timeout` to the Flow to bound total execution                                                                                      |
| CEL expression error preventing step evaluation | Check controller logs for `CEL evaluation error`                                                                                             |
| Finalizer not cleared after controller restart  | The orphan FlowRun timeout (from the finalizer) should trigger after the configured limit. Force-reconcile by annotating the FlowRun.        |

**Step 3 — Check if the controller is healthy:**
```bash
kubectl get pods -n kubezap-system
# If the controller is restarting, FlowRuns may stall until it comes back
```

---

## FlowRun stuck in Waiting

**Symptom**: A FlowRun is in `Waiting` phase longer than the wait step duration.

**This may be expected.** The `type: wait` step pauses execution until `resumeAfter` elapses.

**Step 1 — Check the resume timestamp:**
```bash
kubectl get flowrun <name> \
  -o jsonpath='{.status.steps[?(@.name=="<wait-step-name>")].resumeAfter}'
```

If `resumeAfter` is in the past and the flow is still `Waiting`, the controller may have missed the requeue.

**Step 2 — Restart the controller to trigger re-sync:**
```bash
kubectl rollout restart deployment/kubezap-controller-manager -n kubezap-system
```

The controller re-evaluates all `Running`/`Waiting` FlowRuns on startup and requeues them.

---

## Step failing unexpectedly

**Symptom**: A step shows `Failed` in the FlowRun status.

**Step 1 — Get the failure message:**
```bash
kubectl get flowrun <name> -o yaml | grep -A 10 "phase: Failed"
# or:
kubezap history <name>
```

The `message` field in the step status contains the failure reason.

**Common HTTP step failures:**

| Symptom                                                            | Fix                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `connection refused` / `dial tcp: connect: connection refused`     | The target URL is not reachable. Check the URL and verify the service is running.                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `TLS handshake failed` / `certificate signed by unknown authority` | The target uses a self-signed or private CA. For an HTTP step, set `Integration.spec.http.tls.caBundleConfigMapRef` to a ConfigMap containing the PEM CA bundle (see [Integration CRD → HttpTLSSpec](../api/integration.md#httptlsspec)) — this adds the bundle to the system root pool, it doesn't replace it. For a broker Integration (Kafka/AMQP/NATS) hitting this instead, set `Integration.spec.{kafka,amqp,nats}.tls.caSecretRef` to the Secret containing your private CA (note: broker CA config *replaces* system trust rather than adding to it). |
| `non-2xx response: 401`                                            | The target requires authentication. Check headers and secrets.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `non-2xx response: 503`                                            | The target is temporarily unavailable. Add a `retryPolicy` to the step.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `context deadline exceeded`                                        | The step hit its `timeoutSeconds`. Increase the timeout or fix the slow endpoint.                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `resultMapping key "x" not found in response`                      | The JSONPath expression did not match. Verify the response shape with a manual curl.                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |

**Adding a retry policy:**
```yaml
- name: call-api
  action:
    type: http
    http:
      url: "https://api.internal/endpoint"
      timeoutSeconds: 30
  retryPolicy:
    maxRetries: 3
    backoffType: Exponential
    initialDelay: 1s
    maxDelay: 30s
```

---

## CEL expression errors

**Symptom**: A step with a `when` block is unexpectedly `Failed` rather than `Skipped`, or the controller logs show a CEL error.

**Step 1 — Check controller logs:**
```bash
kubectl logs -n kubezap-system -l control-plane=controller-manager | grep "CEL\|when\|expression"
```

**Common CEL mistakes:**

| Mistake                                                                  | Fix                                                                                                                         |
| ------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| Using hyphenated step names directly: `steps.my-step.results.*`          | CEL identifiers cannot contain hyphens. Use underscores: `steps.my_step.results.*`                                          |
| Comparing a string to an int: `steps.foo.results.count == 5`             | Results are always strings. Compare as strings: `steps.foo.results.count == "5"` or use `int(steps.foo.results.count) == 5` |
| Missing quotes on string literal: `steps.foo.results.tier == enterprise` | String literals require quotes: `steps.foo.results.tier == "enterprise"`                                                    |
| Accessing a result key that wasn't mapped                                | Only keys declared in `resultMappings` are available. Check the step definition.                                            |

**Test a CEL expression interactively** using the [CEL playground](https://cel.dev/playground).

---

## Mockoon not receiving requests

**Symptom**: Flow steps targeting the Mockoon mock server return `connection refused` or `404`, or `GET /api/logs` on the admin API shows no entries for the expected route.

**Step 1 — Verify the Mockoon pod is running:**
```bash
kubectl get pods -l app=mockoon -n <namespace>
# Expected: mockoon-<hash>   1/1   Running
```

If the pod is not `Running`, check events:
```bash
kubectl describe pod -l app=mockoon -n <namespace>
```

**Step 2 — Verify the route is defined in the ConfigMap:**
```bash
kubectl get configmap mockoon-env -n <namespace> -o jsonpath='{.data.environment\.json}' | jq '.routes[].endpoint'
```

The route `endpoint` value must match the path your Flow step calls (without a leading `/`). For example, if the step URL is `http://mockoon.<namespace>.svc.cluster.local:3000/notify-express`, the route endpoint must be `notify-express`.

**Step 3 — Check Mockoon pod logs for request activity:**
```bash
kubectl logs -l app=mockoon -n <namespace>
```

Each request is logged as a structured JSON line. If no log line appears when the Flow step fires, the step URL is targeting the wrong host or port.

**Step 4 — Inspect captured requests via the admin API:**
```bash
kubectl exec -n <namespace> \
  $(kubectl get pod -n <namespace> -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3001/api/logs | jq .
```

**Step 5 — Verify the mock server URL in the Flow step:**

The in-cluster URL format is:
```
http://mockoon.<namespace>.svc.cluster.local:3000/<endpoint>
```

Common mistakes:
- Wrong namespace — `mockoon.default.svc.cluster.local` will not resolve from a pod in a different namespace unless cross-namespace networking is allowed.

**Step 6 — If the ConfigMap was recently updated, restart the pod:**
```bash
kubectl rollout restart deployment/mockoon -n <namespace>
```

Mockoon reads its environment file at startup only. A pod restart is required after ConfigMap changes.

See [Mocking HTTP Endpoints](mocking-http-endpoints.md) for the full Mockoon setup guide.

---

## Kafka gateway not consuming messages

**Symptom**: Kafka messages are not producing FlowRuns, or the `kubezap-kafka-gateway` Deployment is not running.

**Step 1 — Verify the Integration and Trigger are both ready:**
```bash
kubectl get integration <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
kubectl get trigger <name> -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
```

**Step 2 — Check the Kafka gateway pod:**
```bash
kubectl get deployment -l kubezap.io/component=kafka-gateway -n <namespace>
kubectl logs -l kubezap.io/component=kafka-gateway -n <namespace>
```

**Common Kafka issues:**

| Symptom in logs                            | Fix                                                                                                                                                                                   |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `connection refused`                       | Wrong `bootstrapServers` address. Test from inside the cluster: `kubectl run -it --rm --restart=Never --image=bitnami/kafka test -- kafka-topics.sh --bootstrap-server <addr> --list` |
| `SASL authentication failed`               | Wrong username/password. Check the secret keys match what's in `spec.kafka.sasl`.                                                                                                     |
| `CERTIFICATE_UNKNOWN`                      | TLS CA mismatch. Provide the correct CA in `spec.kafka.tls.caSecretRef`.                                                                                                              |
| `consumer group already has a coordinator` | Normal — the consumer group is being rebalanced. Wait for it to settle.                                                                                                               |
| Gateway running but no FlowRuns            | Check consumer group lag: `kubezap integrations` shows current lag. Also verify the topic name in the Trigger matches the actual Kafka topic.                                         |

---

## RBAC and permission errors

**Symptom**: Controller logs show `forbidden` or `cannot get/list/watch/create` errors.

RBAC errors typically look like:
```
flowruns.automation.kubezap.io is forbidden: User "system:serviceaccount:kubezap-system:controller-manager" cannot create resource "flowruns" ...
```

**For MultiNamespace mode** — the controller uses a namespace-scoped `Role`, not a `ClusterRole`: one `Role`/`RoleBinding` pair per watched namespace. Confirm the pair exists in every namespace listed in `WATCH_NAMESPACES` (see `config/rbac/namespaced_role.yaml`'s header comment for the exact per-namespace `kubectl apply` commands on raw-manifest installs; the Helm chart templates this loop automatically).

**For SingleNamespace/OwnNamespace mode** — the controller uses a `Role` scoped to its namespace. Confirm the Role and RoleBinding are created in `kubezap-system`.

**For gateway RBAC** — the webhook gateway creates FlowRuns using a per-namespace `ServiceAccount`. The controller creates this automatically when the gateway `Deployment` is created. If it's missing, check the controller logs for errors during gateway reconciliation.

---

## Using the kubezap CLI for debugging

The `kubezap` CLI simplifies many of these diagnostic steps:

```bash
# Quick overview of all triggers and their health
kubezap triggers

# Find all failed FlowRuns in the last hour
kubezap history --phase Failed --since 1h

# Inspect a specific FlowRun step-by-step
kubezap history <flowrun-name>

# Watch FlowRun completions in real time
kubezap history --watch
```

See [Using the CLI](using-the-cli.md) for the full command reference.

---

## Getting Help

If you cannot find the answer here:

1. Check the controller logs with verbose output enabled: set `--zap-devel=true` on the controller Deployment temporarily.
2. Open an issue at [github.com/kubezap/kubezap-operator](https://github.com/kubezap/kubezap-operator/issues) with:
   - The controller version (`kubezap version`)
   - The relevant Trigger, Flow, and FlowRun YAML (with secrets redacted)
   - Controller and gateway logs from the time of the failure
