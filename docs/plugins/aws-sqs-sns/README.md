# AWS SQS/SNS Messaging Plugin

> **Status: pilot.** This is the first plugin KubeZap maintains itself. It is built from this repository and published as `ghcr.io/kubezap/aws-messaging-plugin`. It is also the reference implementation of the [plugin contract](../../api/plugin-contract.md): it implements both roles end to end with KubeZap's generic `Integration{type: plugin}` and `Trigger{type: plugin}` mechanism. No AWS-specific CRD fields exist. Design records: [AWS SQS/SNS Messaging Plugin](../../design/aws-sqs-sns-messaging-plugin.md) and [Plugin Publish Idempotency Key and Trace Propagation](../../design/plugin-publish-idempotency-and-trace.md).

The plugin runs as one Deployment per `Integration` and serves two roles:

- **Subscriber (SQS → FlowRun).** It watches `Trigger` resources with `type: plugin` that point at its Integration. For each one it long-polls the SQS queue named in `spec.plugin.config.queueUrl` and creates one `FlowRun` per message.
- **Publisher (Flow step → SNS).** It serves `POST /publish`. The controller calls this endpoint when a Flow step with `type: publish` references the Integration, and the plugin publishes the message to the SNS topic whose full ARN is the step's `topic`.

You can use either role alone. A publisher-only Integration needs no Trigger. A subscriber-only Integration needs no publish steps.

Source: [`cmd/aws-messaging-plugin/`](../../../cmd/aws-messaging-plugin/main.go) and [`internal/plugin/awsmessaging/`](../../../internal/plugin/awsmessaging/).

---

## Contents

- [How it works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Setup](#setup)
  - [1. IAM policy](#1-iam-policy)
  - [2. Credentials Secret](#2-credentials-secret)
  - [3. Integration](#3-integration)
  - [4. Publisher Service (required for publish steps)](#4-publisher-service-required-for-publish-steps)
  - [5. Trigger (SQS subscriber)](#5-trigger-sqs-subscriber)
  - [6. Flow with an SNS publish step](#6-flow-with-an-sns-publish-step)
  - [7. Try it](#7-try-it)
  - [Optional: controller-side mTLS](#optional-controller-side-mtls)
- [Configuration reference](#configuration-reference)
- [Semantics](#semantics)
  - [Subscriber: FlowRun naming and deduplication](#subscriber-flowrun-naming-and-deduplication)
  - [Subscriber: delete-after-create and redelivery](#subscriber-delete-after-create-and-redelivery)
  - [Subscriber: what lands in the FlowRun](#subscriber-what-lands-in-the-flowrun)
  - [Subscriber: subscription lifecycle and throughput](#subscriber-subscription-lifecycle-and-throughput)
  - [Publisher: request mapping and validation](#publisher-request-mapping-and-validation)
  - [Publisher: FIFO topics, deduplication and ordering](#publisher-fifo-topics-deduplication-and-ordering)
  - [Publisher: HTTP status mapping](#publisher-http-status-mapping)
  - [Health and readiness](#health-and-readiness)
  - [Trace context](#trace-context)
- [Local testing with LocalStack](#local-testing-with-localstack)
- [Troubleshooting](#troubleshooting)
- [Known limitations](#known-limitations)
- [Contract compliance (worked example)](#contract-compliance-worked-example)

---

## How it works

```
                 ┌──────────────────────────────────────────────┐
  SQS queue ───► │ kubezap-plugin-<integration> pod             │
  (long poll)    │                                              │
                 │  subscriber: one poll loop per plugin Trigger│ ──► FlowRun <trigger>-msg-<MessageId>
                 │  publisher:  POST /publish  ─────────────────│ ──► SNS Publish (TopicArn = step topic)
                 │  GET /healthz (readiness)                    │
                 └──────────────────────────────────────────────┘
                          ▲  POST /publish (controller, optional mTLS)
                          │
                    KubeZap controller ── runs the Flow, executes `type: publish` steps
```

The operator creates and owns these objects for each `type: plugin` Integration:

- a `Deployment`, a `ServiceAccount`, a `Role` and a `RoleBinding`, all named `kubezap-plugin-<integration>`;
- the injected `KUBEZAP_*` environment variables;
- if mTLS is enabled, a per-Integration mTLS Secret.

The Role grants `get`/`list`/`watch` on Triggers and `create` (plus `get`/`list`/`update`/`patch`) on FlowRuns in the plugin's own namespace. The plugin needs nothing else from the Kubernetes API. The operator never reads `spec.plugin.config` and never talks to AWS.

---

## Prerequisites

- KubeZap installed, with the controller managing the namespace where you create the Integration. This guide uses `automation`.
- An SQS queue (subscriber role) and/or an SNS topic (publisher role), plus their URL and ARN.
- An IAM user, or a role you can mint keys for, with the policy below. The plugin uses **static access keys only**. IRSA and other workload identity mechanisms are not supported in this pilot (see [Known limitations](#known-limitations)).
- Outbound HTTPS (443) from the plugin pod to the regional SQS and SNS endpoints (`sqs.<region>.amazonaws.com`, `sns.<region>.amazonaws.com`), and access to the Kubernetes API server. If you use NetworkPolicy, allow both.
- The image `ghcr.io/kubezap/aws-messaging-plugin:<version>` must be pullable from the cluster. Release images are tagged with the release's semver **without** the leading `v` (e.g. `0.3.0`), signed with cosign and scanned with Trivy (see [`SECURITY.md`](../../../SECURITY.md)).

---

## Setup

### 1. IAM policy

The plugin makes exactly four AWS API calls:

| Role       | Call                     | When                                                                              |
| ---------- | ------------------------ | --------------------------------------------------------------------------------- |
| Subscriber | `sqs:GetQueueAttributes` | Once per subscription start or re-establishment, to check the queue and credentials (requests only `QueueArn`) |
| Subscriber | `sqs:ReceiveMessage`     | Continuously (20 s long poll, up to 10 messages)                                  |
| Subscriber | `sqs:DeleteMessage`      | After each message's FlowRun was created (or already existed)                     |
| Publisher  | `sns:Publish`            | Once per `/publish` request                                                       |

Least-privilege policy, scoped to specific queues and topics:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "KubeZapSqsSubscriber",
      "Effect": "Allow",
      "Action": [
        "sqs:ReceiveMessage",
        "sqs:DeleteMessage",
        "sqs:GetQueueAttributes"
      ],
      "Resource": [
        "arn:aws:sqs:us-east-1:123456789012:orders",
        "arn:aws:sqs:us-east-1:123456789012:orders.fifo"
      ]
    },
    {
      "Sid": "KubeZapSnsPublisher",
      "Effect": "Allow",
      "Action": "sns:Publish",
      "Resource": [
        "arn:aws:sns:us-east-1:123456789012:order-events",
        "arn:aws:sns:us-east-1:123456789012:order-events.fifo"
      ]
    }
  ]
}
```

Drop the statement for any role you don't use. The plugin never calls `sns:ListTopics`, `sns:GetTopicAttributes`, `sqs:GetQueueUrl`, `sqs:ChangeMessageVisibility` or `sqs:SendMessage`, so don't grant them.

> **SSE-KMS:** if a queue or topic is encrypted with a customer-managed KMS key, AWS also requires KMS permissions on that key: `kms:Decrypt` to receive from an encrypted queue, and `kms:GenerateDataKey*` plus `kms:Decrypt` to publish to an encrypted topic. The plugin maps a KMS authorization failure on publish to HTTP 403.

### 2. Credentials Secret

The plugin reads credentials only from environment variables. You choose the key names in the Secret and map them to the env var names with `envVarMappings`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: aws-messaging-credentials
  namespace: automation
type: Opaque
stringData:
  access-key-id: AKIAXXXXXXXXXXXXXXXX
  secret-access-key: REPLACE_ME
  # session-token: REPLACE_ME   # only for temporary (STS) credentials; see note below
```

`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` are required. The plugin exits at startup if either is empty. `AWS_SESSION_TOKEN` is optional. Map it only if the Secret really contains that key: the injected `secretKeyRef` is not optional, so a mapping to a missing key leaves the pod stuck in `CreateContainerConfigError`.

> **Temporary credentials expire.** Credentials are loaded once at startup and never refreshed. STS session credentials therefore stop working when they expire, and the plugin has to be restarted with new ones. Use them for short tests only.

### 3. Integration

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: aws-messaging
  namespace: automation
spec:
  type: plugin
  plugin:
    image: ghcr.io/kubezap/aws-messaging-plugin:<version>
    # imageDigest: "<64-hex sha256 digest>"   # recommended in production
    publisherPort: 8090                        # default; KUBEZAP_PUBLISHER_PORT
    env:
      - name: AWS_REGION                       # required
        value: us-east-1
      # - name: AWS_ENDPOINT_URL               # optional override (LocalStack, VPC endpoint)
      #   value: http://localstack.localstack.svc.cluster.local:4566
    secretRefs:
      - secretName: aws-messaging-credentials
        envVarMappings:                        # <secret key>: <env var name>
          access-key-id: AWS_ACCESS_KEY_ID
          secret-access-key: AWS_SECRET_ACCESS_KEY
          # session-token: AWS_SESSION_TOKEN
    # mtls:
    #   enabled: true                          # safe from day one: this plugin implements the mTLS contract
```

A copy lives at [`config/samples/automation_v1alpha1_integration_aws_messaging.yaml`](../../../config/samples/automation_v1alpha1_integration_aws_messaging.yaml). The `config/samples/` copies use the `default` namespace; this guide uses `automation`.

The operator creates the Deployment `kubezap-plugin-aws-messaging`, which runs one replica. Check it with:

```sh
kubectl get integration aws-messaging -n automation
kubectl rollout status deployment/kubezap-plugin-aws-messaging -n automation
kubectl logs deployment/kubezap-plugin-aws-messaging -n automation
```

The Integration's `Ready` condition only means that the operator reconciled the Deployment. It says nothing about whether AWS is reachable. Use the Deployment's readiness and the plugin logs for that (see [Health and readiness](#health-and-readiness)).

### 4. Publisher Service (required for publish steps)

> **Known gap — create this Service yourself.** The controller sends `/publish` to `http://kubezap-plugin-<integration>.<namespace>.svc.cluster.local:<publisherPort>/publish` (`https://` with mTLS). The operator does **not** currently create that Service for `type: plugin` Integrations, so publish steps fail with a DNS resolution error until it exists. This affects every plugin, not just this one. Subscriber-only setups don't need the Service.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kubezap-plugin-aws-messaging     # must be exactly kubezap-plugin-<integration name>
  namespace: automation
spec:
  selector:
    app: kubezap-plugin-aws-messaging    # the label the operator puts on plugin pods
  ports:
    - name: publisher
      port: 8090                         # must equal spec.plugin.publisherPort
      targetPort: 8090
      protocol: TCP
```

The name matters for mTLS as well. The operator issues the plugin's server certificate for `kubezap-plugin-<integration>.<namespace>.svc` and `.svc.cluster.local`. Copy: [`config/samples/aws-messaging-plugin-publisher-service.yaml`](../../../config/samples/aws-messaging-plugin-publisher-service.yaml).

### 5. Trigger (SQS subscriber)

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: orders-sqs
  namespace: automation
spec:
  type: plugin
  enabled: true
  plugin:
    integrationRef:
      name: aws-messaging                # must be a type: plugin Integration in the same namespace
    config:
      queueUrl: https://sqs.us-east-1.amazonaws.com/123456789012/orders
  flowRef:
    name: process-sqs-order
```

Copy: [`config/samples/automation_v1alpha1_trigger_sqs.yaml`](../../../config/samples/automation_v1alpha1_trigger_sqs.yaml).

- `spec.plugin.config.queueUrl` is the **only** config key the plugin reads. Use the full queue URL as `aws sqs get-queue-url` returns it, not the ARN and not a bare queue name. The plugin rejects any value that isn't an `http(s)` URL with a host and a non-empty path. A rejected Trigger gets no subscription, and the pod reports not ready (see [Health and readiness](#health-and-readiness)).
- The operator passes `config` through without looking at it. A typo such as `queueURL` or `queue_url` passes admission without complaint, and the plugin then reports `plugin.config["queueUrl"] is missing or empty`.
- Several Triggers can share one Integration. Each Trigger gets its own poll loop.

What the operator checks for a `type: plugin` Trigger is only whether `spec.plugin.integrationRef` resolves. It records the result in the `PluginIntegrationInvalid` condition, which is set on enabled Triggers only:

| `PluginIntegrationInvalid` | Reason | Meaning |
| --- | --- | --- |
| `False` | `PluginIntegrationValid` | The ref resolves to a `type: plugin` Integration. This says **nothing** about the queue or AWS credentials. |
| `True` | `PluginIntegrationNotFound` | No Integration with that name exists in the Trigger's namespace. |
| `True` | `PluginIntegrationWrongType` | The Integration exists but is not `type: plugin` (for example `type: kafka`). |
| `True` | `PluginSpecMissing` | `spec.plugin.integrationRef.name` is empty or `spec.plugin` is absent. |

When the condition is `True`, `Ready` and `Accepted` are `False` with reason `PluginIntegrationInvalid`. The controller does not watch Integrations, so it re-checks the ref every 30 s and the condition clears on its own once the Integration exists. See [Trigger CRD → `PluginIntegrationInvalid`](../../api/trigger.md#pluginintegrationinvalid-plugin-only).

```sh
kubectl get trigger orders-sqs -n automation \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} ({.reason}){"\n"}{end}'
```

### 6. Flow with an SNS publish step

The step's `topic` must be the **full SNS topic ARN**. The plugin never resolves topic names. The other fields map as follows: `body` becomes the SNS `Message` and `headers` become SNS `MessageAttributes`.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: process-sqs-order
  namespace: automation
spec:
  steps:
    - name: publish-order-event
      action:
        type: publish
        publish:
          integrationRef:
            name: aws-messaging
          topic: arn:aws:sns:us-east-1:123456789012:order-events
          headers:
            event-type: order.received
            order-id: "$(trigger.body.orderId)"
          body: |
            {"orderId": "$(trigger.body.orderId)", "status": "received"}
      retryPolicy:
        maxRetries: 3
        backoffType: Exponential
        initialDelay: "2s"
```

Copy: [`config/samples/automation_v1alpha1_flow_sns_publish.yaml`](../../../config/samples/automation_v1alpha1_flow_sns_publish.yaml).

`$(trigger.body.<field>)` works on SQS message bodies that contain JSON. The controller tries a JSON parse on any trigger body that isn't form-encoded, even though the plugin sets no `contentType`. When the step succeeds, it exposes the SNS message ID as the result `messageId` (e.g. `$(steps.publish_order_event.results.messageId)`).

**FIFO topics.** Use the topic's ARN, which ends in `.fifo`, as the `topic`, e.g. `arn:aws:sns:us-east-1:123456789012:order-events.fifo`. Nothing else changes in the Flow:

- The plugin sets `MessageGroupId` to `kubezap-<namespace>-<integration>` itself.
- The plugin sets `MessageDeduplicationId` from the controller's `idempotencyKey`. The current controller always sends that key.

See [FIFO topics, deduplication and ordering](#publisher-fifo-topics-deduplication-and-ordering) for the consequences.

### 7. Try it

```sh
aws sqs send-message \
  --queue-url https://sqs.us-east-1.amazonaws.com/123456789012/orders \
  --message-body '{"orderId": "A-1001"}' \
  --message-attributes 'event-type={DataType=String,StringValue=order.created}'

kubectl get flowruns -n automation -l kubezap.io/trigger=orders-sqs
# NAME                                                  ...
# orders-sqs-msg-6f1c2e0a-3b7d-4c55-9a51-2f0f4d6c8e11
```

The plugin logs `flowrun created` with `trigger`, `flowrun` and `messageId` fields, and then deletes the message from the queue.

### Optional: controller-side mTLS

This plugin implements the [controller-side mTLS contract](../../api/plugin-contract.md#controller-side-mtls) in full. You can set `spec.plugin.mtls.enabled: true` on the Integration from the start. Once you do:

- `/publish` is served over TLS (1.2+) on `KUBEZAP_PUBLISHER_PORT`, and every request must present a client certificate signed by the Integration's CA.
- `/healthz` moves to a separate plain-HTTP listener on `KUBEZAP_MTLS_HEALTH_PORT`. The operator injects `8091` and points the readiness probe there. Startup fails if this port equals the publisher port.
- The certificate, key and CA are read from `KUBEZAP_MTLS_CERT_FILE` / `KUBEZAP_MTLS_KEY_FILE` / `KUBEZAP_MTLS_CA_FILE`. If those are unset, the plugin falls back to `/etc/kubezap/mtls/{tls.crt,tls.key,ca.crt}`.
- **Rotation without a restart.** The operator rotates the CA and both leaf certs roughly every 23 h. On each TLS handshake the plugin checks the files' modification times and reloads them when they change. If a reload fails, for example because the files are mid-update, it keeps serving the last good material.

The Service from step 4 still targets the publisher port. Nothing about it changes.

---

## Configuration reference

The plugin reads environment variables only.

| Variable | Source | Required | Default | Notes |
| --- | --- | --- | --- | --- |
| `KUBEZAP_NAMESPACE` | operator | yes | — | Namespace whose Triggers the plugin watches. The Trigger cache is restricted to it. |
| `KUBEZAP_INTEGRATION_NAME` | operator | yes | — | Triggers are selected by `spec.plugin.integrationRef.name` equal to this value. It is also part of the FIFO `MessageGroupId`. |
| `KUBEZAP_PUBLISHER_PORT` | operator | no | `8090` | From `spec.plugin.publisherPort`. Serves `/publish`, plus `/healthz` when mTLS is off. |
| `KUBEZAP_LOG_LEVEL` | operator | no | `info` | zap level (`debug`, `info`, `warn`, `error`). Logs are JSON on stdout. |
| `KUBEZAP_MTLS_ENABLED` | operator (mTLS) | no | — | `true` switches on the mTLS listener layout described above. |
| `KUBEZAP_MTLS_HEALTH_PORT` | operator (mTLS) | with mTLS | — | Plain-HTTP `/healthz` port. Must differ from the publisher port. |
| `KUBEZAP_MTLS_CERT_FILE` / `_KEY_FILE` / `_CA_FILE` | operator (mTLS) | no | `/etc/kubezap/mtls/tls.crt` / `tls.key` / `ca.crt` | Server certificate, server key, and the CA used to verify the controller's client cert. |
| `AWS_REGION` | `spec.plugin.env` | yes | — | Region for both the SQS and the SNS client. |
| `AWS_ENDPOINT_URL` | `spec.plugin.env` | no | — | Endpoint override for **both** SQS and SNS. Must be an `http(s)` URL with a host. |
| `AWS_ACCESS_KEY_ID` | `secretRefs` | yes | — | Static credentials. |
| `AWS_SECRET_ACCESS_KEY` | `secretRefs` | yes | — | Static credentials. |
| `AWS_SESSION_TOKEN` | `secretRefs` | no | — | Only for temporary credentials. These are never refreshed. |

The plugin validates all of these at startup and reports every problem in one error message. If any check fails, the process exits and the pod goes into `CrashLoopBackOff`. The first log line names what is wrong.

Per-Trigger configuration (`spec.plugin.config`):

| Key | Required | Description |
| --- | --- | --- |
| `queueUrl` | yes | Full SQS queue URL, either standard or FIFO. The plugin ignores all other keys. |

---

## Semantics

### Subscriber: FlowRun naming and deduplication

Each SQS message produces a FlowRun named:

```
<trigger-name>-msg-<MessageId>
```

The name is lowercased, every character outside `[a-z0-9-]` becomes `-`, and the result is cut to 253 characters with trailing `-` trimmed. SQS assigns `MessageId`, and every `ReceiveMessage` returns it, for standard and FIFO queues alike. One rule therefore covers both queue types. `MessageDeduplicationId` is not used because it isn't guaranteed to be present on receive.

The FlowRun name is the dedup key. If the same message is delivered again, the plugin tries to create the same name. The Kubernetes API answers `409 AlreadyExists`, and the plugin treats that as success. Some consequences:

- A producer that sends the same logical event twice creates two SQS messages with different `MessageId`s, and therefore two FlowRuns. Make producers idempotent, or use a FIFO queue with deduplication on the producer side.
- Dedup lasts only as long as the FlowRun does. A FlowRun removed by TTL GC can't absorb a later redelivery of the same message. In practice this needs a delete failure followed by redelivery after the TTL has passed.
- Two Triggers polling the **same** queue are competing consumers: each message goes to one of them. For fan-out, subscribe one queue per consumer to an SNS topic.

### Subscriber: delete-after-create and redelivery

For each received message the plugin:

1. Creates the FlowRun.
2. **Only if** the create succeeded or returned `409`, calls `DeleteMessage` with that receipt handle. The delete has its own 5 s timeout, independent of subscription shutdown, so a FlowRun created just before shutdown still gets its message deleted.

| Outcome | What happens to the message |
| --- | --- |
| FlowRun created | Deleted. |
| `409 AlreadyExists` (duplicate) | Deleted. Logged at debug level. |
| Any other create error (RBAC, API server unavailable, webhook rejection, ...) | Left on the queue. SQS makes it visible again when the queue's visibility timeout expires, and the plugin retries it then. |
| `DeleteMessage` fails after a successful create | SQS redelivers the message after the visibility timeout. The redelivery returns `409` and is deleted then. |
| Message without a `MessageId` | Skipped and **not** deleted. |

A message is never deleted before its FlowRun exists, so a crash at any point loses no messages; the cost is possible redelivery, which the FlowRun name absorbs. Configure a **redrive policy (dead-letter queue)** with a sensible `maxReceiveCount` on every subscribed queue. Otherwise a message whose FlowRun can never be created is redelivered forever.

### Subscriber: what lands in the FlowRun

| FlowRun field | Value |
| --- | --- |
| labels | `kubezap.io/trigger=<trigger>`, `kubezap.io/trigger-type=plugin`, `kubezap.io/flow=<flow>` |
| annotation `kubezap.io/traceparent` | The message's `traceparent` message attribute, if present |
| `spec.flowRef.name` | The Trigger's `spec.flowRef.name` |
| `spec.triggerRef` | `{name: <trigger>, type: plugin}` |
| `spec.triggerData.source` | `plugin` |
| `spec.triggerData.body` | The message body verbatim if it's valid UTF-8, otherwise base64 |
| `spec.triggerData.bodyEncoding` | `utf8` or `base64` |
| `spec.triggerData.headers` | The message's **message attributes**. String and Number values are copied as-is, Binary values are base64-encoded. Names on the built-in sensitive list (`Authorization`, `X-Api-Key`, `Cookie`, `X-Amz-Security-Token`, ...) are replaced with `[REDACTED]`. |

SQS system attributes such as `SentTimestamp`, `ApproximateReceiveCount` or `MessageGroupId` are not captured. The `MessageId` only appears in the FlowRun name. The plugin doesn't truncate the body, so the FlowRun object's size limit (etcd) is the effective ceiling. Read attributes in a Flow with `$(trigger.headers.<name>)`, which matches names case-insensitively.

> **SNS → SQS subscriptions:** if the queue is subscribed to an SNS topic, enable **raw message delivery** on the subscription. Without it, SQS receives SNS's JSON envelope. Your payload is then the string `Message` field inside that envelope, and the SNS message attributes are inside the body instead of on the SQS message, so `$(trigger.headers.*)` won't see them.

### Subscriber: subscription lifecycle and throughput

- **Selection.** A Trigger is served when all of these hold: it is in `KUBEZAP_NAMESPACE`, `spec.type: plugin`, `spec.plugin.integrationRef.name` equals `KUBEZAP_INTEGRATION_NAME`, and `spec.enabled` is true (the default).
- **Changes take effect without a restart.** Disabling, deleting or repointing a Trigger cancels its poll loop. Changing `queueUrl` or `flowRef.name` restarts it. Messages that were received but not yet processed when a loop stops are not deleted, and SQS redelivers them after the visibility timeout.
- **Polling.** Each Trigger has one loop doing `ReceiveMessage` with a 20 s long poll and up to 10 messages per call. It processes the batch **sequentially**: create the FlowRun, then delete the message. After an SQS error the loop backs off exponentially, from 1 s up to 30 s, and re-checks the queue with `GetQueueAttributes` before polling again.
- **No visibility extension.** The plugin never calls `ChangeMessageVisibility`. Handling one message is a single API create plus a delete, so this rarely matters. Still, keep the queue's visibility timeout comfortably above the time to handle a batch of 10 under API server load; 30 s, the SQS default, is plenty. If the timeout expires early, the result is a duplicate delivery that dedup absorbs, not a lost message.
- **Scaling.** The Deployment always runs one replica. Throughput per Trigger is bounded by one sequential loop. To spread a heavy queue's load, use more queues and Triggers, or more Integrations. During a rolling update the old and the new pod may both poll for a moment, and FlowRun-name dedup makes that harmless.

### Publisher: request mapping and validation

The controller POSTs the [publish envelope](../../api/plugin-contract.md#post-publish). The plugin maps it like this:

| Envelope field | SNS `Publish` parameter | Rules |
| --- | --- | --- |
| `destination` (the step's `topic`) | `TopicArn` | Required. Must parse as a full SNS **topic** ARN, `arn:<partition>:sns:<region>:<account-id>:<topic-name>`, with non-empty region and account. Bare names and subscription ARNs (resource containing `:`) are rejected with `400`. The ARN's region is **not** compared with `AWS_REGION`. |
| `body` | `Message` | Required and non-empty (`400` otherwise). Message bodies are never logged. |
| `headers` | `MessageAttributes` (`DataType: String`) | Names must match `[A-Za-z0-9_.-]{1,256}`, must not start or end with `.`, must not contain `..`, and must not start with `aws.` or `amazon.` (case-insensitive). Values must be non-empty. At most **10** attributes in total, counting `traceparent`. Any violation returns `400`. |
| `idempotencyKey` | `MessageDeduplicationId` | `.fifo` topics only. Ignored for standard topics. |
| HTTP header `traceparent` | `MessageAttributes["traceparent"]` | Forwarded when it is a well-formed W3C version-`00` value. If the step also sets a `traceparent` header, this one wins. A malformed value is dropped without failing the publish. |
| `integration`, `namespace` | — | Not used. The plugin takes its identity from `KUBEZAP_*`. |

Unknown envelope fields are ignored, as the contract requires. Other limits: request bodies over 1 MiB get `413`, non-POST requests get `405`, and invalid JSON gets `400`. Each SNS call times out after 25 s, below the controller's 30 s per-attempt timeout, so the plugin always answers before the controller gives up. On success the plugin responds `200 {"messageId": "<SNS MessageId>"}`.

### Publisher: FIFO topics, deduplication and ordering

The plugin treats a topic as FIFO when its ARN ends in `.fifo`. For FIFO topics:

- **`MessageGroupId`** is always `kubezap-<namespace>-<integration>`, cut to 128 characters. Every message published through one Integration therefore lands in **one message group**. That gives strict ordering across everything that Integration publishes, and **no group-level parallelism**: FIFO subscribers such as SQS FIFO queues deliver a group's messages one batch at a time. Per-step or per-message group IDs are not configurable in this pilot. If you need parallel consumption, publish through several Integrations, which gives one group each, or use a standard topic.
- **`MessageDeduplicationId`** is the controller's `idempotencyKey`, `kz1-` followed by 64 hex characters ([derivation](../../api/plugin-contract.md#idempotency)). The key is identical across every retry of a step in one FlowRun and across re-execution after controller failover. SNS therefore drops the duplicates a retried publish would otherwise produce. A user-initiated re-run creates a new FlowRun with a new key, so it is **not** suppressed. A key that doesn't fit SNS's rules (1–128 characters, alphanumerics and punctuation) gets `400`, but the controller's keys always fit.
- **Dedup window: 5 minutes.** SNS FIFO only deduplicates within 5 minutes. A step re-executed after a longer outage can still publish a duplicate, so downstream consumers that need exactly-once effects must be idempotent themselves.
- **No key → ContentBasedDeduplication required.** The current controller always sends a key. If a request arrives without one (an older controller, or calling `/publish` directly), the plugin leaves `MessageDeduplicationId` unset. The topic must then have `ContentBasedDeduplication` enabled, or SNS rejects the publish and the plugin returns `400`.

For standard topics the plugin sets neither field. SNS standard topics deliver at least once and can duplicate on retry.

### Publisher: HTTP status mapping

Following the contract, `4xx` means retrying can't help and `5xx` means the failure is transient:

| Condition | Status |
| --- | --- |
| Invalid request: bad ARN, empty body, bad header, bad key, invalid JSON | `400` |
| Request body too large | `413` |
| SNS auth/permission errors (`AuthorizationError`, `AccessDenied`, `InvalidClientTokenId`, `SignatureDoesNotMatch`, `ExpiredToken`, `KMSAccessDenied`, ...) | `403` |
| Topic not found (`NotFound`, `KMSNotFound`, ...) | `404` |
| SNS parameter/state errors (`InvalidParameter`, `ValidationError`, `KMSDisabled`, ...), and any other client-fault error | `400` |
| Throttling (`Throttled`, `ThrottlingException`, `KMSThrottling`, ...), or request cancelled | `503` |
| SNS internal errors, other server-fault errors, network/DNS/TLS failures | `502` |
| SNS call exceeded 25 s | `504` |

> **The controller retries on every non-200.** It does not distinguish `4xx` from `5xx`. Any failure, permanent or not, consumes the step's `retryPolicy` attempts before the step fails. The status code and the plugin's `error` text show up in the step's error message, so check them before raising retries. Retrying a `400 destination ... is not a full SNS topic ARN` will never succeed.

### Health and readiness

`GET /healthz` returns `200 ok` when the plugin is ready. Otherwise it returns `503` with a plain-text reason. The operator wires it as the Deployment's **readiness** probe. There is no liveness probe.

The plugin is ready only when **all** of these hold:

1. The initial Trigger cache sync has finished (`initial Trigger sync not complete` until then).
2. **Every** selected Trigger has an established SQS session. A session counts as established after `GetQueueAttributes` succeeded and the latest `ReceiveMessage` didn't fail. An invalid `queueUrl`, missing IAM permissions, a non-existent queue or an SQS outage on **any one** Trigger makes the whole pod unready, with the reason listed per Trigger, e.g. `no SQS session for: orders-sqs: plugin.config["queueUrl"] is missing or empty`.
3. The SNS client was constructed. This is a startup check only; the plugin does **not** call SNS to probe it.

**Idle readiness.** A plugin with no selected Triggers reports ready, because no session exists that could be failing. A publisher-only Integration therefore becomes ready without any Trigger, as does a new Integration before its first Trigger exists.

> **One bad queue blocks publishing for the whole Integration.** Readiness controls whether the pod is an endpoint of the publisher Service. If one Trigger's queue is broken, the pod goes unready, its Service endpoint is removed, and every `type: publish` step through that Integration fails, even though SNS is fine. Fix or disable the broken Trigger. Alternatively, keep subscriber and publisher traffic on separate Integrations, or set `publishNotReadyAddresses: true` on the Service from step 4. The last option keeps publishing while a subscription is broken, at the cost of also routing to a pod that hasn't finished its initial sync. The publisher listener starts before that sync and works independently of it.

The probe only reports readiness. It never restarts the pod. A pod that is unready because of a bad queue keeps retrying with backoff, and recovers on its own once the queue or IAM problem is fixed.

### Trace context

The plugin passes W3C trace context through, but it creates **no spans of its own** and has no OTel exporter.

- **SQS → FlowRun.** A `traceparent` message attribute is copied to the FlowRun annotation `kubezap.io/traceparent`, so the controller continues the producer's trace.
- **`/publish` → SNS.** The `traceparent` HTTP header that the controller injects from the publish step's span is forwarded as the `traceparent` message attribute. It is also logged, together with its trace ID, on the plugin's publish log lines.

---

## Local testing with LocalStack

The plugin works against [LocalStack](https://docs.localstack.cloud/) unchanged. Set `AWS_ENDPOINT_URL`, which applies to **both** clients, and use LocalStack's dummy credentials and account ID:

```yaml
# Integration excerpt
    env:
      - name: AWS_REGION
        value: us-east-1
      - name: AWS_ENDPOINT_URL
        value: http://localstack.localstack.svc.cluster.local:4566
# credentials Secret: any non-empty values, e.g. access-key-id: test / secret-access-key: test
```

```sh
# against LocalStack (from a pod or with a port-forward to 4566)
aws --endpoint-url http://localhost:4566 sqs create-queue --queue-name orders
aws --endpoint-url http://localhost:4566 sns create-topic --name order-events
```

- Use the queue URL the cluster can resolve. `queueUrl` is dialed **from the plugin pod**, and `http://` URLs are accepted, e.g. `http://localstack.localstack.svc.cluster.local:4566/000000000000/orders`. If `create-queue` returns a URL with a host that only resolves on your machine, rewrite the host.
- Topic ARNs look like `arn:aws:sns:us-east-1:000000000000:order-events`.

The publish path still needs the Service from [step 4](#4-publisher-service-required-for-publish-steps).

---

## Troubleshooting

| Symptom | Likely cause and fix |
| --- | --- |
| Pod in `CrashLoopBackOff`, log starts `aws-messaging-plugin: invalid configuration:` | A required env var is missing or invalid: `AWS_REGION`, the credentials, a bad `AWS_ENDPOINT_URL`, or a mTLS health port equal to the publisher port. The message lists every problem. |
| Pod stuck in `CreateContainerConfigError` | The credentials Secret is missing, or `envVarMappings` names a key the Secret doesn't have (often `session-token`). `kubectl describe pod` names it. |
| Pod `Running` but `0/1 READY`, `/healthz` says `no SQS session for: <trigger>: ...` | That Trigger's queue can't be used. The text after the Trigger name is the plugin's validation error or the AWS error, e.g. an access-denied error, a queue-does-not-exist error, or a wrong region in the URL. Fix the queue URL or IAM policy. The plugin retries with backoff. Check with `kubectl logs deployment/kubezap-plugin-<integration>`, or `kubectl port-forward` to the health port and `curl /healthz` (the image is distroless and has no shell). |
| Trigger `Ready=False`, reason `PluginIntegrationInvalid` | The Integration is missing or not `type: plugin`. Read the condition's reason and message. It re-checks every 30 s. |
| Trigger `Ready=True` but no FlowRuns appear | The operator doesn't check queues or credentials. Look at the plugin's logs and readiness. Also check: is the Trigger `enabled`; does `integrationRef.name` match the Integration exactly; is the key spelled `queueUrl`; and is the queue subscribed to SNS without raw delivery (the FlowRun is created, but the body is an SNS envelope). |
| FlowRuns created, but the same message keeps coming back | `DeleteMessage` is failing, usually because `sqs:DeleteMessage` is missing from the policy. Look for `failed to delete SQS message after FlowRun create` in the logs. The FlowRun isn't duplicated (`409`), but the message cycles until it reaches the DLQ or the retention limit. |
| Logs show `failed to create FlowRun; leaving message for redelivery` | The Kubernetes API rejected the create. Read the logged error: RBAC, admission, object too large... The message stays on the queue and is retried after the visibility timeout. |
| Publish step fails with `dial tcp: lookup kubezap-plugin-<name>...: no such host` | The publisher Service is missing. Create it ([step 4](#4-publisher-service-required-for-publish-steps)). |
| Publish step fails with `connection refused` or times out | The pod is unready (see the readiness rows above and [one bad queue blocks publishing](#health-and-readiness)), the Service port doesn't match `publisherPort`, or a NetworkPolicy blocks controller → plugin traffic. |
| Publish step fails with `status 400: destination ... is not a full SNS topic ARN` | Put the full topic ARN in `topic`. |
| `status 400: ... InvalidParameter ...` on a `.fifo` topic | No `idempotencyKey` was sent and the topic doesn't have `ContentBasedDeduplication` enabled, or an attribute or parameter breaks SNS FIFO rules. |
| `status 403: SNS error AuthorizationError ...` | Grant `sns:Publish` on that topic ARN, and KMS permissions if the topic is SSE-KMS encrypted. |
| `status 400: N message attributes ... exceed the SNS limit of 10` | The step sets too many `headers`. Remember that the forwarded `traceparent` counts toward the limit. |
| TLS handshake errors on publish after enabling mTLS | The Service name doesn't match `kubezap-plugin-<integration>`, so the server cert SANs don't match, or the Service targets the health port instead of the publisher port. |
| Credentials rotated, but the plugin still uses the old ones | Credentials are read once at startup. Restart the pod: `kubectl delete pod -n <ns> -l app=kubezap-plugin-<integration>`. A `rollout restart` annotation gets reverted by the operator's reconcile. |

---

## Known limitations

- **Static access keys only. No IRSA or other workload identity, and no refresh.** The operator owns the plugin's ServiceAccount and has no field for an `eks.amazonaws.com/role-arn` annotation. Credentials are read once at startup, so **rotating keys requires restarting the pod**: `kubectl delete pod -l app=kubezap-plugin-<integration>`. Temporary STS credentials stop working when they expire.
- **The operator does not check `spec.plugin.config`.** Typos, missing keys and malformed queue URLs pass admission. They surface only in the plugin's logs and `/healthz`, and the Trigger's own status stays `Ready=True`.
- **The operator does not create the publisher Service.** You must create `kubezap-plugin-<integration>` yourself for publish steps to work ([step 4](#4-publisher-service-required-for-publish-steps)). This gap applies to every `type: plugin` Integration.
- **One bad queue makes the whole pod unready**, which also takes the publisher role out of the Service ([details](#health-and-readiness)).
- **No Prometheus metrics, and no OTel spans.** The plugin exposes no `/metrics` endpoint. Observability is JSON logs and `/healthz`, plus trace-context pass-through.
- **Sequential processing per Trigger. No visibility-timeout extension, no tunable batch size or concurrency.** The Deployment always runs one replica.
- **`TriggerData.source` is `plugin`**, not `sqs`, and `triggerRef.type` is `plugin`. A Flow can't tell an SQS-originated FlowRun from another plugin's except through the FlowRun name or labels. SQS system attributes and the `MessageId` are not in `triggerData`.
- **FIFO `MessageGroupId` is fixed per Integration.** One group means total ordering and no group parallelism, and it isn't configurable per step.
- **Topic ARN region is not checked against `AWS_REGION`.** A cross-region ARN is sent to the configured region's endpoint. The step then fails with whatever error SNS returns, not with a clear plugin validation message. Use one Integration per region.
- **The operator does not verify the plugin image.** As with any `type: plugin` image, the operator runs whatever `spec.plugin.image` points to. Pin `imageDigest`, and verify the cosign signature yourself (see [`SECURITY.md`](../../../SECURITY.md#container-image-signing)).
- **Pilot status.** The plugin's source and release cadence are tied to the operator repository for now (see the design record's Tradeoffs).

---

## Contract compliance (worked example)

This plugin is the worked example referenced by [Integration CRD → Community Plugin Graduation](../../api/integration.md#community-plugin-graduation). It shows that the [plugin contract](../../api/plugin-contract.md) is enough to build a real, non-trivial broker integration with no CRD changes beyond the generic `Trigger{type: plugin}`.

| Contract requirement | How this plugin meets it | Code |
| --- | --- | --- |
| [Trigger selection](../../api/plugin-contract.md#trigger-selection) | Namespace + `type: plugin` + `integrationRef` + `enabled`, via a namespace-scoped informer | `subscriber.go` (`Selects`), `main.go` |
| [FlowRun creation](../../api/plugin-contract.md#flowrun-creation) and [dedup key](../../api/plugin-contract.md#dedup-key-requirements) | `<trigger>-msg-<MessageId>` (the contract's "Message ID" pattern), with standard labels, `triggerRef` and `triggerData` | `subscriber.go` (`FlowRunName`, `BuildFlowRun`) |
| [Handling duplicates](../../api/plugin-contract.md#handling-duplicates) | `409 AlreadyExists` → success | `subscriber.go` (`handleMessage`) |
| [Offset/cursor commit](../../api/plugin-contract.md#offsetcursor-commit) | `DeleteMessage` only after create or `409` | `subscriber.go` (`handleMessage`) |
| [Dynamic subscription management](../../api/plugin-contract.md#dynamic-subscription-management) | Add/update/delete handlers start, restart and cancel per-Trigger loops | `subscriber.go` (`Reconcile`, `Remove`) |
| [`POST /publish`](../../api/plugin-contract.md#post-publish) | Envelope decoding that ignores unknown fields, `200 {messageId}` / `4xx`/`5xx {error}` | `publisher.go` |
| [Idempotency](../../api/plugin-contract.md#idempotency) | `idempotencyKey` → SNS FIFO `MessageDeduplicationId` | `publisher.go` (`buildInput`) |
| [Health check](../../api/plugin-contract.md#health-check) | Session-aware readiness with idle readiness | `health.go` |
| [Controller-side mTLS](../../api/plugin-contract.md#controller-side-mtls) | `RequireAndVerifyClientCert`, separate plain-HTTP health port, hot reload of cert, key and CA | `mtls.go` |
| [Logging](../../api/plugin-contract.md#logging) | zap JSON on stdout. Subscriber lines carry `integration`/`namespace`/`trigger`/`flowrun`; publisher lines carry `destination`/`messageId`/`traceparent`. | `main.go`, `subscriber.go`, `publisher.go` |
| [Trace propagation](../../api/plugin-contract.md#trace-context-propagation) | SQS `traceparent` attribute → FlowRun annotation; `/publish` `traceparent` header → SNS attribute | `subscriber.go`, `publisher.go` |

Measured against the [graduation criteria](../../api/integration.md#graduation-criteria): this plugin wraps a vendor-proprietary API (SQS/SNS over the AWS SDK), not an open wire protocol. It is a first-party plugin rather than a candidate for a built-in `type: sqs`. Its purpose is to show the plugin path working end to end.
