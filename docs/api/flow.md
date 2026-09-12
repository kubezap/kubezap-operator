# Flow CRD

A `Flow` defines the sequence of steps to execute when a `Trigger` fires. It is a reusable template — the same `Flow` can be referenced by multiple `Trigger` resources and executed concurrently.

Flows support HTTP actions, data transformations, timed waits, conditional step execution, retry policies, and chaining data between steps.

---

## Contents

- [Overview](#overview)
- [Execution Model](#execution-model)
- [Payload Formats](#payload-formats)
- [Data and Expressions](#data-and-expressions)
- [Conditions and CEL](#conditions-and-cel)
- [Spec Reference](#spec-reference)
- [Status Reference](#status-reference)
- [Examples](#examples)
  - [Simple HTTP Call](#example-1-simple-http-call)
  - [Multi-Step with Data Passing](#example-2-multi-step-with-data-passing)
  - [Conditional Branching](#example-3-conditional-branching)
  - [Retry and Error Handling](#example-4-retry-and-error-handling)
  - [Data Transformation](#example-5-data-transformation)
  - [XML Payload Processing](#example-6-xml-payload-processing)
  - [Incident Escalation with Wait](#example-7-incident-escalation-with-wait)
- [Step Action Types](#step-action-types)
- [Expression Reference](#expression-reference)
- [Limitations](#limitations)

---

## Overview

A `Flow` is composed of one or more **steps**. Each step performs an action (such as an HTTP call) and may produce **results** — named output values that downstream steps can consume as parameters.

Steps execute sequentially by default. You can control ordering explicitly with `runAfter` or enable parallel execution by having multiple steps with the same `runAfter` set.

Conditional execution is controlled by `when` blocks using [CEL (Common Expression Language)](https://cel.dev). A step whose `when` condition evaluates to false is **skipped**, not failed.

```
  Trigger fires
       │
       ▼
  Resolve params from payload
       │
       ▼
  ┌─────────┐
  │ Step A  │  action → results
  └────┬────┘
       │ results
       ├──────────────────────┐
       ▼                      ▼
  ┌─────────┐           ┌─────────┐
  │ Step B  │           │ Step C  │  parallel (same runAfter)
  │  (when) │           │  (when) │
  └────┬────┘           └────┬────┘
       └──────────┬───────────┘
                  ▼
             ┌─────────┐
             │ Step D  │  notification
             └─────────┘
```

---

## Payload Formats

KubeZap infers the payload format from the `Content-Type` of the triggering event (webhook request body or Kafka message value). For JSON and form-urlencoded bodies, the parsed data is navigable from both `$(...)` interpolation (`$(trigger.body.<field>)`) and CEL `when` conditions (`trigger.bodyFields.<field>` — a differently-named field, since `trigger.body` itself is always the raw string in CEL; see [Using Trigger Data in CEL Conditions](#using-trigger-data-in-cel-conditions)).

| Content-Type                        | Parsed as        | Access pattern                                                                                     |
| ----------------------------------- | ---------------- | --------------------------------------------------------------------------------------------------- |
| `application/json`                  | JSON object      | `$(trigger.body.userId)`, `$(trigger.body.order.id)` — full dot-path; `trigger.bodyFields.*` in CEL |
| `application/x-www-form-urlencoded` | flat form fields | `$(trigger.body.field)` — top-level only, no nested dot-path; `trigger.bodyFields.*` in CEL         |
| `application/xml`, `text/xml`       | raw string       | `$(trigger.body)` — full body as string only                                                        |
| `text/plain`                        | raw string       | `$(trigger.body)`                                                                                    |
| Other / binary                      | raw string       | `$(trigger.body)`                                                                                    |

### Accessing Trigger Body Fields

Use `$(trigger.body)` for the raw body and `$(trigger.body.<field>)` for a body field. JSON supports full dot-path traversal — for example, `$(trigger.body.order.id)` navigates into a nested object. Form-urlencoded bodies are flat: only top-level field names resolve (no nested paths, since form encoding has no nesting concept).

```yaml
steps:
  - name: extract
    action:
      type: transform
      transform:
        mappings:
          orderId: "$(trigger.body.orderId)"
          raw: "$(trigger.body)"
```

### Using Trigger Data in CEL Conditions

`trigger.body` in a CEL `when` expression is always the raw string, regardless of content type — the same value `$(trigger.body)` interpolates. Use string methods on it directly:

```yaml
when:
  - expression: 'trigger.body.contains("order.placed")'
```

For a JSON or form-urlencoded body, `trigger.bodyFields` gives full, typed, nested access into the same parsed structure `$(trigger.body.<field>)` interpolation traverses — dot-path into nested objects, index into arrays, and compare numbers/booleans as native CEL values (no `double()`/`bool()` cast needed, unlike `steps.*.results` and `params.*`, which are always strings):

```yaml
when:
  - expression: 'trigger.bodyFields.order.customer.tier == "enterprise"'
  - expression: 'trigger.bodyFields.total >= 1000.0'
```

`trigger.bodyFields` is an empty map (never an evaluation error) when the body isn't JSON or form-urlencoded — e.g. XML — so `has(trigger.bodyFields.x)` and direct access are always safe; fall back to `trigger.body.contains(...)` for those content types (see [Example 6](#example-6-xml-payload-processing)).

### HTTP Response Formats

HTTP step `resultMappings` support both JSON and XML responses. The mapping expression syntax is auto-detected:

| Prefix | Language | Example                                     |
| ------ | -------- | ------------------------------------------- |
| `$.`   | JSONPath | `$.user.name`, `$.items[0].id`              |
| `/`    | XPath    | `/response/user/name`, `/items/item[1]/@id` |

```yaml
resultMappings:
  # JSON response
  userId: "$.user.id"
  # XML response
  orderId: "/order/id"
  orderStatus: "/order/@status"
```

---

## Execution Model

When a `Trigger` fires, the KubeZap operator:

1. Resolves the `flowRef` to a `Flow` in the same namespace
2. Evaluates declared `params` against the trigger payload
3. Creates an in-memory execution context and runs steps in dependency order
4. Records execution results to the `Trigger` status

> **FlowRun CRD**: Each time a trigger fires, the gateway creates a `FlowRun` CR. The controller watches `FlowRun` resources and executes the referenced Flow. Every execution is a Kubernetes resource — inspect history with `kubectl get flowruns`. See [FlowRun CRD](flowrun.md) for the full spec.

**Step ordering rules:**

- Steps without `runAfter` run in the order they appear in the spec, each waiting for the previous step to complete
- Steps with `runAfter` wait for all listed steps to complete before starting
- Multiple steps with the same `runAfter` set run in parallel

**Failure behavior:**

- By default (`failurePolicy: Fail`), the flow stops immediately when any step fails
- `failurePolicy: Continue` allows remaining steps to run even when a step fails; skipped-due-to-failure steps have status `Skipped`
- Individual steps can override this with `onFailure: Continue` or `onFailure: Skip`

---

## Data and Expressions

### Parameter Interpolation

Use `$(syntax)` to reference dynamic values in string fields (URLs, headers, bodies, parameter values):

| Expression                                 | Resolves to                                                                                    |
| ------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `$(trigger.body)`                          | Raw trigger event body (webhook request body or Kafka message value)                           |
| `$(trigger.body.<field>)`                  | A field from the trigger body; JSON bodies support full dot-path traversal (e.g., `$(trigger.body.order.id)`), `application/x-www-form-urlencoded` bodies support flat top-level fields only (e.g., `$(trigger.body.text)`) |
| `$(trigger.headers.<name>)`                | An HTTP header from the triggering request (case-insensitive lookup)                            |
| `$(trigger.topic)` / `$(trigger.partition)` / `$(trigger.offset)` | Kafka topic/partition/offset (empty for non-Kafka triggers)                       |
| `$(trigger.scheduledTime)`                 | RFC3339 scheduled fire time (cron triggers only)                                                |
| `$(steps.<stepName>.results.<resultName>)` | A result produced by a previous step (hyphens in the step name become underscores)              |
| `$(secrets.<secretName>.<key>)`            | A value from a Kubernetes Secret in the same namespace                                         |
| `$(params.<name>)`                         | A resolved Flow parameter — see [ParamDeclaration](#paramdeclaration) for the resolution order  |

There is no `$(trigger.name)`, `$(trigger.namespace)`, or `$(trigger.type)` — none of these are implemented. If you need a step to see the triggering Trigger's name, thread it through explicitly (e.g. a header or a step result), not via interpolation.

**Example:**
```yaml
# Flow.spec
params:
  - name: userId
    required: true

steps:
  - name: fetch-user
    action:
      type: http
      http:
        url: "https://api.internal/users/$(params.userId)"
        headers:
          Authorization: "Bearer $(secrets.api-credentials.token)"
```

Here `userId` resolves automatically from a same-named top-level `trigger.body.userId` field. To override it explicitly instead, the invoking `FlowRun.spec.params` would set `{name: userId, value: "..."}` (a literal or a `$(...)`-interpolated value).

The trigger payload structure depends on the trigger type and content type. See [Trigger CRD → Trigger Types](trigger.md#trigger-types) for the exact payload fields available from each trigger source.

> String interpolation is evaluated at step execution time. Referencing a result from a step that has not yet run will cause a validation error at flow start.

---

## Conditions and CEL

KubeZap uses [CEL (Common Expression Language)](https://cel.dev) for all boolean conditions in `when` blocks. CEL is the condition language used natively by Kubernetes in `ValidatingAdmissionPolicy`, `CRD validation rules`, and other core APIs — K8s operators and platform engineers are increasingly familiar with it.

**Why CEL and not alternatives:**

| Language       | Verdict                                                                                       |
| -------------- | --------------------------------------------------------------------------------------------- |
| **CEL**        | Recommended. Kubernetes-native, sandboxed, rich operators, growing adoption                   |
| Go templates   | Better for string rendering (used for `$(...)` interpolation), not designed for boolean logic |
| JMESPath       | Query language (good for extraction), limited boolean expression support                      |
| JSONPath       | Kubernetes-native but very limited — no boolean operators                                     |
| expr-lang/expr | Simpler syntax but not standardized in the Kubernetes ecosystem                               |

CEL is sandboxed: it cannot make network calls, access the filesystem, or execute arbitrary code, making it safe to run user-provided expressions.

### CEL cost limits

KubeZap enforces a CEL computation budget per `when` expression to prevent runaway expressions (such as nested comprehensions on large arrays) from stalling the controller. The default budget is **10,000 cost units** (tunable via the `--cel-cost-limit` flag on the controller). Expressions that exceed the budget are treated as evaluation errors and cause the step to be skipped with an `EvalError` reason in the FlowRun status. The `kubezap_when_expression_errors_total{flow,reason="eval_error"}` Prometheus counter increments on budget overruns.

### Conditions (CEL)

`when` block expressions use [CEL (Common Expression Language)](https://cel.dev). The expression must evaluate to a boolean. If it evaluates to `false`, the step is **skipped**.

Available CEL variables:

| Variable                | Type                  | Description                                                              |
| ----------------------- | --------------------- | ------------------------------------------------------------------------- |
| `trigger.body`          | `string`              | Raw trigger event body — **not parsed into a map**; string ops only (`==`, `contains()`, etc.) |
| `trigger.topic`         | `string`              | Kafka topic (empty for non-Kafka triggers)                              |
| `trigger.partition`     | `string`              | Kafka partition, formatted as a string (empty for non-Kafka triggers)    |
| `trigger.offset`        | `string`              | Kafka offset, formatted as a string (empty for non-Kafka triggers)      |
| `trigger.scheduledTime` | `string`              | RFC3339 scheduled fire time (cron only; empty otherwise)                 |
| `trigger.headers`       | `map<string, dyn>`    | Trigger request headers                                                  |
| `steps.<name>.status`   | `string`              | Step phase: `Succeeded`, `Failed`, `Skipped`, etc.                       |
| `steps.<name>.results`  | `map<string, string>` | Step results map                                                          |
| `params.<name>`         | `dyn`                 | A resolved Flow parameter (see [ParamDeclaration](#paramdeclaration)); use `has(params.name)` to test presence |

There is no `trigger.name`/`trigger.type`/`trigger.namespace` variable, and `trigger.body` is **not** parsed into a structured object — unlike `$(trigger.body.<field>)` interpolation (used in step params/URLs/bodies), which does support JSON dot-path traversal. To branch on a structured field from the trigger body, extract it in an upstream `transform` step via interpolation, then reference the result:

```yaml
steps:
  - name: extract-fields
    action:
      type: transform
      transform:
        mappings:
          environment: "$(trigger.body.environment)"
  - name: deploy-prod
    runAfter: [extract-fields]
    when:
      - expression: 'steps.extract_fields.results.environment == "production"'
```

**Examples:**

```yaml
# Only run if the previous step succeeded and returned a non-empty userId
when:
  - expression: 'steps.auth.status == "Succeeded" && steps.auth.results.userId != ""'

# Run only if the HTTP response was successful (result stored as a string; cast to int)
when:
  - expression: 'int(steps.fetch_data.results.statusCode) < 400'

# Simple substring match directly against the raw trigger body
when:
  - expression: 'trigger.body.contains("production")'
```

> CEL is sandboxed and cannot access external systems, make network calls, or execute arbitrary code. It is safe to use with user-provided expressions.

### Skipped Steps and Dependency Cascading

When a step's `when` condition evaluates to `false`, the step is set to `Skipped`. The
behavior of dependent steps (steps that list it in `runAfter`) follows this rule:

**A step whose _entire_ `runAfter` set consists of skipped steps is itself skipped.**

This means a skip cascades naturally down the dependency graph. For example:

```
enrich-order → notify-express (when: tier == "express")
             → notify-express-sms (runAfter: notify-express)
```

If `notify-express` is skipped, `notify-express-sms` is also skipped automatically —
you don't need a `when` condition on every downstream step.

**If a step has multiple `runAfter` dependencies and only some are skipped**, it still
runs — the skipped steps are treated as satisfied (they did not fail, so they do not
block the flow). This allows fan-out patterns where some branches complete and some skip:

```
enrich → [notify-express (skipped), notify-standard (succeeded)] → summary-step (runs)
```

The `summary-step` runs because at least one `runAfter` dependency succeeded. Steps in
the `when` expression can check `steps.<name>.status == "Skipped"` to react to this.

**In CEL, skipped upstream results are accessible:**

```yaml
when:
  - expression: 'steps.notify_express.status == "Succeeded" || steps.notify_standard.status == "Succeeded"'
```

---

## Spec Reference

### FlowSpec

| Field           | Type               | Required | Default | Description                                                |
| --------------- | ------------------ | -------- | ------- | ---------------------------------------------------------- |
| `description`   | string             | No       | —       | Human-readable description of the flow                     |
| `timeout`       | duration           | No       | `10m`   | Maximum time allowed for the entire flow to complete       |
| `failurePolicy` | enum               | No       | `Fail`  | What to do when a step fails: `Fail` or `Continue`         |
| `params`        | []ParamDeclaration | No       | —       | Input parameters the flow accepts from the trigger payload |
| `steps`         | []FlowStep         | **Yes**  | —       | Ordered list of steps to execute                           |

### ParamDeclaration

Declares an input parameter the flow accepts. For each declared param, the controller resolves a value in this order (see [`docs/design/2026-09-10-flow-parameters.md`](../design/2026-09-10-flow-parameters.md) for the full design):

1. An explicit entry in the invoking `FlowRun.spec.params` with a matching name — its `value` is itself resolved through `$(...)` interpolation, so it may reference `$(trigger.body.x)`, `$(steps.*.results.x)`, `$(secrets.x.y)`, etc.
2. Otherwise, a same-named **top-level** field in the trigger body is used automatically (JSON objects or form-urlencoded bodies; this only matches a flat field — `name: orderId` matches a top-level `orderId` in the body, not a nested one).
3. Otherwise, the declared `default` is used as a literal value (not interpolated).
4. Otherwise, if `required: true`, the FlowRun fails **before any step is dispatched**, with a message naming the missing parameter.
5. Otherwise, the parameter resolves to an empty string.

Resolution happens once per FlowRun and is available to every step via `$(params.<name>)` and to `when:` CEL conditions via the `params` variable.

| Field         | Type    | Required | Default | Description                                                        |
| ------------- | ------- | -------- | ------- | ------------------------------------------------------------------ |
| `name`        | string  | **Yes**  | —       | Parameter name, used in `$(params.name)` expressions               |
| `description` | string  | No       | —       | Human-readable description                                         |
| `required`    | boolean | No       | `false` | If true, the flow fails to start if this parameter is not provided |
| `default`     | string  | No       | —       | Default value if the parameter is absent from the trigger payload  |

### FlowStep

| Field         | Type                | Required | Default | Description                                                           |
| ------------- | ------------------- | -------- | ------- | --------------------------------------------------------------------- |
| `name`        | string              | **Yes**  | —       | Unique name for this step within the flow. Used to reference results. |
| `description` | string              | No       | —       | Human-readable description                                            |
| `runAfter`    | []string            | No       | —       | Step names that must complete before this step starts                 |
| `when`        | []WhenExpression    | No       | —       | CEL conditions that must all be true for the step to run              |
| `action`      | StepAction          | **Yes**  | —       | The action this step performs                                         |
| `results`     | []ResultDeclaration | No       | —       | Outputs this step produces                                            |
| `retryPolicy` | RetryPolicy         | No       | —       | Retry behavior on failure                                             |
| `timeout`     | duration            | No       | —       | Step-level timeout, overrides flow timeout for this step              |
| `onFailure`   | enum                | No       | `Fail`  | Per-step failure behavior: `Fail`, `Continue`, or `Skip`              |

> Step names must be unique within a flow, start with a letter, and contain only letters, numbers, and hyphens. When referencing step names in expressions, replace hyphens with underscores: a step named `fetch-user` is referenced as `steps.fetch_user.results.field`.

### WhenExpression

| Field        | Type   | Required | Description                                                     |
| ------------ | ------ | -------- | --------------------------------------------------------------- |
| `expression` | string | **Yes**  | CEL expression that must evaluate to `true` for the step to run |

### ParamValue

Used by `FlowRun.spec.params` (see [`docs/api/flowrun.md`](flowrun.md)) to provide an explicit override for a Flow parameter declared via [ParamDeclaration](#paramdeclaration). Each entry's `value` is itself resolved through `$(...)` interpolation before being bound to the named parameter.

| Field   | Type   | Required | Description                                        |
| ------- | ------ | -------- | -------------------------------------------------- |
| `name`  | string | **Yes**  | Name of the parameter to set                       |
| `value` | string | **Yes**  | Value, which may use `$(...)` interpolation syntax |

### StepAction

| Field       | Type            | Required    | Description                                            |
| ----------- | --------------- | ----------- | ------------------------------------------------------ |
| `type`      | enum            | **Yes**     | Action type: `http`, `transform`, `publish`, or `wait` |
| `http`      | HTTPAction      | Conditional | Required when `type: http`                             |
| `transform` | TransformAction | Conditional | Required when `type: transform`                        |
| `publish`   | PublishAction   | Conditional | Required when `type: publish`                          |
| `wait`      | WaitAction      | Conditional | Required when `type: wait`                             |

### HTTPAction

| Field            | Type              | Required | Default | Description                                                                                                                 |
| ---------------- | ----------------- | -------- | ------- | --------------------------------------------------------------------------------------------------------------------------- |
| `url`            | string            | **Yes**  | —       | URL to call. Supports `$(...)` interpolation.                                                                               |
| `method`         | enum              | No       | `POST`  | HTTP method: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`                                                                        |
| `headers`        | map[string]string | No       | —       | HTTP headers. Values support `$(...)` interpolation.                                                                        |
| `body`           | string            | No       | —       | Request body. Supports `$(...)` interpolation.                                                                              |
| `timeoutSeconds` | integer           | No       | `30`    | Request timeout in seconds                                                                                                  |
| `resultMappings` | map[string]string | No       | —       | Map response fields to step results. Keys are result names; values are JSONPath into the response body (e.g., `$.user.id`). |

### TransformAction

Produces results from existing params and step results without making any network calls. Use this to rename, merge, or reshape data between steps.

| Field      | Type              | Required | Description                                                                     |
| ---------- | ----------------- | -------- | ------------------------------------------------------------------------------- |
| `mappings` | map[string]string | **Yes**  | Keys are result names; values are strings using `$(...)` interpolation (e.g. `$(trigger.body.orderId)`, `$(steps.fetch_user.results.email)`) — not CEL expressions. See [`$(...)` Interpolation Quick Reference](#-interpolation-quick-reference). |

### PublishAction

Sends a message to an external system via an `Integration`. The controller
calls the Integration's gateway `/publish` endpoint, which handles
serialisation, authentication, and delivery.

| Field            | Type                 | Required | Description                                                            |
| ---------------- | -------------------- | -------- | ---------------------------------------------------------------------- |
| `integrationRef` | LocalObjectReference | **Yes**  | Name of the `Integration` resource to publish through                  |
| `topic`          | string               | **Yes**  | Target topic, queue, or subject name. Supports `$(...)` interpolation. |
| `body`           | string               | No       | Message body. Supports `$(...)` interpolation.                         |
| `headers`        | map[string]string    | No       | Message headers / metadata. Values support `$(...)` interpolation.     |

**Example**:

```yaml
- name: publish-enriched
  runAfter:
    - enrich-profile
  action:
    type: publish
    publish:
      integrationRef:
        name: customer-kafka
      topic: customer-events-enriched
      headers:
        X-Customer-Tier: "$(steps.enrich_profile.results.tier)"
      body: |
        {
          "customerId": "$(steps.extract_customer.results.customerId)",
          "tier":       "$(steps.enrich_profile.results.tier)"
        }
```

#### Execution

The controller routes the publish call based on the `Integration` type referenced by `integrationRef`:

**If the Integration type is `kafka`** (`spec.kafka` is set):
- The controller publishes the message directly via a cached `sarama.SyncProducer` connected to the integration's bootstrap servers.
- No HTTP call is made. Kafka headers from `headers` are forwarded as Kafka message headers.
- Producers are cached per broker address and reused across publish steps to avoid per-call TCP handshake overhead.

**If the Integration type is `plugin`** (`spec.plugin` is set):
- The controller makes an HTTP POST to the plugin's `/publish` endpoint:
  ```
  POST http://kubezap-plugin-{integration-name}.{namespace}.svc.cluster.local:{port}/publish
  ```
  where `{port}` defaults to `8090` if `spec.plugin.publisherPort` is unset.
- **Content-Type**: defaults to `application/json`; override by setting `Content-Type` in `headers`.
- **Body**: the value of `body` after `$(...)` interpolation. If `body` is empty, the request is sent with no body.
- **Timeout**: 30 seconds, unless the enclosing step or flow timeout context is shorter.
- **Success**: HTTP 2xx response — the step succeeds with an empty results map.
- **Failure**: HTTP status >= 400 — the step fails with an error message containing the status code. The response body is not included in the error message.

**Retry interaction**: publish steps respect `retryPolicy` if configured on the step. Each retry re-executes the full publish call (Kafka produce or HTTP POST).

### WaitAction

Pauses the FlowRun for a fixed duration before continuing. When the step is
reached, the controller records the resume time in the FlowRun status and
stops executing until that time is reached. The wait survives controller
restarts.

| Field      | Type            | Required | Description                                                                     |
| ---------- | --------------- | -------- | ------------------------------------------------------------------------------- |
| `duration` | duration string | **Yes**  | How long to pause. Accepts any Go duration string: `30s`, `10m`, `1h`, `2h30m`. |

**Behavior:**

- The step enters a `Waiting` phase visible in `status.steps[*].phase` on the FlowRun.
- The flow is fully blocked during the wait — no other steps execute while the wait is active.
- The resume timestamp is persisted in `status.steps[*].resumeAfter` on the FlowRun so the wait survives controller restarts.
- The wait step produces no results (empty results map).
- Downstream steps run normally once the duration elapses.

> **Note — full-block model:** Because KubeZap uses a linear execution model, steps with no `runAfter` dependency on the wait step are also blocked while the wait is active. For fire-and-forget parallel actions (for example, triggering mitigation via a webhook), model them as `http` steps that return immediately (202 Accepted) so they complete *before* the wait step begins. See [Example 7](#example-7-incident-escalation-with-wait) for the recommended pattern.

**Example:**

```yaml
- name: cool-down
  description: "Wait 10 minutes before checking system health"
  runAfter:
    - trigger-remediation
  action:
    type: wait
    wait:
      duration: "10m"
```

### ResultDeclaration

| Field         | Type   | Required | Description                                                     |
| ------------- | ------ | -------- | --------------------------------------------------------------- |
| `name`        | string | **Yes**  | Result name, referenced as `$(steps.<stepName>.results.<name>)` |
| `description` | string | No       | Human-readable description of what this result contains         |

### RetryPolicy

| Field          | Type      | Required | Default       | Description                                         |
| -------------- | --------- | -------- | ------------- | --------------------------------------------------- |
| `maxRetries`   | integer   | **Yes**  | —             | Maximum number of retry attempts                    |
| `backoffType`  | enum      | No       | `Exponential` | Delay strategy: `Fixed`, `Linear`, or `Exponential` |
| `initialDelay` | duration  | No       | `1s`          | Delay before the first retry                        |
| `maxDelay`     | duration  | No       | `60s`         | Maximum delay cap between retries                   |
| `multiplier`   | string    | No       | `"2.0"`       | Multiplier for exponential backoff                  |

Retries occur on any non-2xx HTTP response — there is currently no way to filter which status codes trigger a retry.

---

## Status Reference

### FlowStatus

| Field               | Type        | Description                                                                   |
| ------------------- | ----------- | ----------------------------------------------------------------------------- |
| `conditions`        | []Condition | Standard Kubernetes conditions. See condition types below.                    |
| `executionCount`    | integer     | Total number of times this flow has been executed                             |
| `lastExecutionTime` | timestamp   | Timestamp of the most recent execution                                        |
| `lastResult`        | string      | Outcome of the most recent execution: `Succeeded`, `Failed`                   |
| `lastError`         | string      | Error message from the most recent failed execution                           |

### Condition Types

| Type    | Status  | Meaning                                                          |
| ------- | ------- | ---------------------------------------------------------------- |
| `Ready` | `True`  | The flow spec is valid and the flow is ready to be executed      |
| `Ready` | `False` | The flow spec has a validation error. See `message` for details. |

---

## Examples

### Example 1: Simple HTTP Call

Call a webhook URL when the trigger fires. No data passing required.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: notify-on-event
  namespace: automation
spec:
  description: "Notify an external service when triggered"
  steps:
    - name: send-notification
      action:
        type: http
        http:
          url: "https://hooks.example.com/notify"
          method: POST
          headers:
            Content-Type: "application/json"
          body: '{"event": "triggered"}'
```

---

### Example 2: Multi-Step with Data Passing

Fetch user details from an API, then use the result to send a personalized notification.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: user-notification-flow
  namespace: automation
spec:
  description: "Fetch user info and send a personalized Slack message"
  timeout: "2m"
  params:
    - name: userId
      description: "The user ID from the trigger payload"
      required: true
    - name: eventType
      description: "The type of event that occurred"
      default: "unknown"
  steps:
    - name: fetch-user
      description: "Retrieve user details from the user service"
      action:
        type: http
        http:
          url: "https://users.internal/api/v1/users/$(params.userId)"
          method: GET
          headers:
            Authorization: "Bearer $(secrets.user-service-creds.token)"
          timeoutSeconds: 10
          resultMappings:
            name: "$.name"
            email: "$.email"
            department: "$.department"
      results:
        - name: name
          description: "User's full name"
        - name: email
          description: "User's email address"
        - name: department

    - name: send-slack-message
      description: "Post a notification to the team Slack channel"
      runAfter: [fetch-user]
      action:
        type: http
        http:
          url: "https://slack.com/api/chat.postMessage"
          method: POST
          headers:
            Authorization: "Bearer $(secrets.slack-bot-token.token)"
            Content-Type: "application/json"
          body: |
            {
              "channel": "#$(steps.fetch_user.results.department)-alerts",
              "text": "Event '$(params.eventType)' occurred for $(steps.fetch_user.results.name) <$(steps.fetch_user.results.email)>"
            }
```

---

### Example 3: Conditional Branching

Route processing differently based on the trigger payload content.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: order-processing-flow
  namespace: automation
spec:
  description: "Process orders with different paths for high-value orders"
  failurePolicy: Continue
  params:
    - name: orderId
      required: true
    - name: orderTotal
      required: true
  steps:
    - name: validate-order
      action:
        type: http
        http:
          url: "https://orders.internal/validate/$(params.orderId)"
          method: GET
          resultMappings:
            valid: "$.valid"
            reason: "$.reason"
      results:
        - name: valid
        - name: reason

    - name: process-standard
      description: "Standard processing path"
      runAfter: [validate-order]
      when:
        - expression: 'steps.validate_order.results.valid == "true" && double(params.orderTotal) < 1000.0'
      action:
        type: http
        http:
          url: "https://orders.internal/process/standard/$(params.orderId)"
          method: POST

    - name: process-high-value
      description: "High-value order path with manual approval notification"
      runAfter: [validate-order]
      when:
        - expression: 'steps.validate_order.results.valid == "true" && double(params.orderTotal) >= 1000.0'
      action:
        type: http
        http:
          url: "https://orders.internal/process/high-value/$(params.orderId)"
          method: POST

    - name: reject-invalid
      description: "Notify on validation failure"
      runAfter: [validate-order]
      when:
        - expression: 'steps.validate_order.results.valid == "false"'
      action:
        type: http
        http:
          url: "https://orders.internal/reject/$(params.orderId)"
          method: POST
          body: '{"reason": "$(steps.validate_order.results.reason)"}'
```

---

### Example 4: Retry and Error Handling

Call an unreliable external API with exponential backoff.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: resilient-api-call
  namespace: automation
spec:
  description: "Call an external API with retry logic"
  steps:
    - name: call-external-api
      action:
        type: http
        http:
          url: "https://api.external-vendor.com/events"
          method: POST
          headers:
            X-API-Key: "$(secrets.vendor-api.key)"
          body: '{"payload": "$(trigger.body.data)"}'
          timeoutSeconds: 15
      retryPolicy:
        maxRetries: 5
        backoffType: Exponential
        initialDelay: "2s"
        maxDelay: "2m"
        multiplier: "2.5"
      onFailure: Continue

    - name: record-failure
      description: "Log the failure if the API call ultimately failed"
      runAfter: [call-external-api]
      when:
        - expression: 'steps.call_external_api.status == "Failed"'
      action:
        type: http
        http:
          url: "https://logging.internal/failures"
          method: POST
          body: '{"flow": "resilient-api-call", "trigger": "$(trigger.name)", "step": "call-external-api"}'
```

---

### Example 5: Data Transformation

Use a `transform` step to reshape data between two HTTP calls.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: data-transform-flow
  namespace: automation
spec:
  description: "Fetch data from one API, transform it, and send to another"
  steps:
    - name: fetch-raw-data
      action:
        type: http
        http:
          url: "https://source-system.internal/api/records/$(trigger.body.recordId)"
          method: GET
          resultMappings:
            firstName: "$.first_name"
            lastName: "$.last_name"
            accountNumber: "$.acct_num"
      results:
        - name: firstName
        - name: lastName
        - name: accountNumber

    - name: reshape-payload
      description: "Transform source format to target system format"
      runAfter: [fetch-raw-data]
      action:
        type: transform
        transform:
          mappings:
            fullName: '"$(steps.fetch_raw_data.results.firstName) $(steps.fetch_raw_data.results.lastName)"'
            account: '"ACC-$(steps.fetch_raw_data.results.accountNumber)"'
            processedAt: 'string(now())'
      results:
        - name: fullName
        - name: account
        - name: processedAt

    - name: send-to-target
      runAfter: [reshape-payload]
      action:
        type: http
        http:
          url: "https://target-system.internal/api/ingest"
          method: POST
          headers:
            Content-Type: "application/json"
          body: |
            {
              "name": "$(steps.reshape_payload.results.fullName)",
              "accountId": "$(steps.reshape_payload.results.account)",
              "timestamp": "$(steps.reshape_payload.results.processedAt)"
            }
```

---

### Example 6: XML Payload Processing

XML is one of the supported trigger `Content-Type`s, but — unlike JSON and form-urlencoded bodies — it is never parsed into a navigable structure (see [Payload Formats](#payload-formats)). An XML request body is only ever available as the raw string via `$(trigger.body)`, both in step interpolation and in `when` conditions. There is no dot-path or CEL field access into XML trigger bodies today.

The incoming webhook sends:
```xml
<orderEvent type="created">
  <orderId>ORD-9921</orderId>
  <customer id="C-42">
    <name>Acme Corp</name>
    <tier>enterprise</tier>
  </customer>
  <total currency="USD">4850.00</total>
</orderEvent>
```

If you need to branch or extract individual fields from an XML payload like this, the current options are: pre-process the request into JSON before it reaches KubeZap (e.g. a small ingress-side transform), or fall back to string matching on the raw body:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-xml-webhook
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/orders-xml
    method: POST
  flowRef:
    name: process-xml-order
---
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: process-xml-order
  namespace: automation
spec:
  description: "Route XML order events by a string match on the raw body"
  steps:
    - name: route-enterprise
      description: "Route likely-enterprise orders to priority queue for closer inspection"
      when:
        - expression: 'trigger.body.contains("<tier>enterprise</tier>")'
      action:
        type: http
        http:
          url: "https://orders.internal/api/priority"
          method: POST
          headers:
            Content-Type: "application/xml"
          body: "$(trigger.body)"

    - name: route-standard
      description: "Route everything else to normal queue"
      when:
        - expression: '!trigger.body.contains("<tier>enterprise</tier>")'
      action:
        type: http
        http:
          url: "https://orders.internal/api/standard"
          method: POST
          headers:
            Content-Type: "application/xml"
          body: "$(trigger.body)"
```

Note this is substring matching on raw XML text, not structured field access — it is fragile to whitespace/attribute-order changes and offered only as a fallback. Extracting individual fields (`orderId`, `customer.name`, etc.) out of an incoming XML body into a transformed JSON request is not possible today without pre-processing upstream of KubeZap.

**Calling an XML-response API and extracting fields with XPath:**

This is a separate, fully-supported capability: `resultMappings` on an HTTP step auto-detects XPath syntax (`/`-prefixed) for XML *responses*, regardless of what content type triggered the Flow.

```yaml
steps:
  - name: fetch-from-xml-api
    action:
      type: http
      http:
        url: "https://legacy-system.internal/api/orders/$(params.orderId)"
        method: GET
        resultMappings:
          orderId: "/order/id"
          status: "/order/@status"
          customerName: "/order/customer/name"
    results:
      - name: orderId
      - name: status
      - name: customerName
```

---

### Example 7: Incident Escalation with Wait

Page on-call and trigger automated mitigation in parallel, wait 10 minutes for
the issue to resolve, then check system health and escalate to PagerDuty only
if the system is still unhealthy.

`trigger-mitigation` has no `runAfter` dependency, so it runs in parallel with
`page-oncall`. Both steps make fire-and-forget HTTP calls (expecting a 202
Accepted) and complete before `wait-for-resolution` begins. This is the
recommended pattern when you need parallel side effects ahead of a wait — see
the [WaitAction note](#waitaction) for details.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: incident-escalation
  namespace: ops
spec:
  description: "Page on-call, trigger mitigation, wait, then escalate if still unhealthy"
  timeout: "30m"
  steps:
    - name: page-oncall
      description: "Post an alert to the on-call Slack channel"
      action:
        type: http
        http:
          url: "https://hooks.slack.com/services/$(secrets.slack-webhook.path)"
          method: POST
          headers:
            Content-Type: "application/json"
          body: |
            {
              "text": ":fire: Incident detected: $(trigger.body.alert.name) in $(trigger.body.alert.environment)"
            }
          timeoutSeconds: 10

    - name: trigger-mitigation
      description: "Fire auto-remediation — fire-and-forget, expects 202 Accepted"
      action:
        type: http
        http:
          url: "https://remediation.internal/api/trigger"
          method: POST
          headers:
            Content-Type: "application/json"
            Authorization: "Bearer $(secrets.remediation-api.token)"
          body: |
            {
              "alert": "$(trigger.body.alert.name)",
              "environment": "$(trigger.body.alert.environment)"
            }
          timeoutSeconds: 10

    - name: wait-for-resolution
      description: "Pause 10 minutes to allow auto-remediation to take effect"
      runAfter:
        - page-oncall
        - trigger-mitigation
      action:
        type: wait
        wait:
          duration: "10m"

    - name: check-health
      description: "Verify the service has recovered"
      runAfter:
        - wait-for-resolution
      action:
        type: http
        http:
          url: "https://healthcheck.internal/api/services/$(trigger.body.alert.service)"
          method: GET
          headers:
            Authorization: "Bearer $(secrets.healthcheck-api.token)"
          resultMappings:
            status: "$.status"
          timeoutSeconds: 15
      results:
        - name: status
          description: "Service health status: healthy or degraded"

    - name: escalate
      description: "Create a PagerDuty incident if the service is still unhealthy"
      runAfter:
        - check-health
      when:
        - expression: 'steps.check_health.results.status != "healthy"'
      action:
        type: http
        http:
          url: "https://events.pagerduty.com/v2/enqueue"
          method: POST
          headers:
            Content-Type: "application/json"
            Authorization: "Token token=$(secrets.pagerduty-api.token)"
          body: |
            {
              "routing_key": "$(secrets.pagerduty-api.routingKey)",
              "event_action": "trigger",
              "payload": {
                "summary": "Auto-remediation failed: $(trigger.body.alert.name)",
                "severity": "critical",
                "source": "$(trigger.body.alert.environment)",
                "custom_details": {
                  "health_status": "$(steps.check_health.results.status)",
                  "alert": "$(trigger.body.alert.name)"
                }
              }
            }
          timeoutSeconds: 10
```

---

## Step Action Types

| Type             | Description                                              | Status    |
| ---------------- | -------------------------------------------------------- | --------- |
| `http`           | Make an HTTP/HTTPS request to any URL                    | Available |
| `transform`      | Reshape data between steps using `$(...)` interpolation  | Available |
| `publish`        | Publish a message through an Integration (Kafka topic, AMQP queue, NATS subject) | Available |
| `wait`           | Pause the FlowRun for a fixed duration before continuing | Available |
| `kubernetes-job` | Run a Kubernetes Job and wait for completion             | Planned   |
| `plugin`         | Call an external KubeZap plugin service via webhook      | Planned   |

---

## Expression Reference

### `$(...)` Interpolation Quick Reference

```
$(trigger.body)                          → raw trigger event body
$(trigger.body.<field>)                  → field from the trigger body (JSON: full dot-path; form-urlencoded: flat top-level only)
$(trigger.headers.<name>)                → HTTP header from the triggering request (case-insensitive)
$(trigger.topic) / .partition / .offset  → Kafka coordinates (empty for non-Kafka triggers)
$(trigger.scheduledTime)                 → scheduled fire time, RFC3339 (cron triggers only)
$(steps.<step-name>.results.<result>)    → result from a completed step
$(secrets.<secret-name>.<key>)           → value from a Kubernetes Secret
$(params.<name>)                         → resolved Flow parameter (see ParamDeclaration)
```

> In expressions, step names use underscores: `fetch-user` → `steps.fetch_user`.
>
> **Not implemented**: `$(trigger.name)`, `$(trigger.namespace)`, `$(trigger.type)`, `$(configmaps.<name>.<key>)`, and `$(env.<VAR_NAME>)` are not supported. `$(secrets.<name>.<key>)` is the only supported way to pull config into a step today; for non-secret config, inline the value directly in the Flow.

### CEL Quick Reference

```
# String comparison
steps.extract_fields.results.action == "deploy"

# Numeric comparison (cast strings to numbers)
double(steps.extract_fields.results.price) > 100.0
int(steps.fetch.results.statusCode) < 400

# Logical operators
steps.auth.status == "Succeeded" && steps.extract_fields.results.env == "production"
steps.check.status == "Failed" || steps.extract_fields.results.force == "true"

# String contains (trigger.body is always the raw string)
trigger.body.contains("created")

# Nested trigger body field access (JSON/form-urlencoded only — natively typed, no casts needed)
trigger.bodyFields.order.customer.tier == "enterprise"
trigger.bodyFields.items[0].id == "a"
trigger.bodyFields.total >= 1000.0

# Null/empty checks
steps.fetch.results.userId != ""
has(steps.fetch.results.userId)
has(trigger.bodyFields.order)
```

---

## Limitations

- **No loops**: Flows do not currently support iterating over arrays. Use a plugin service or Kubernetes Job for fan-out patterns.
- **No sub-flows**: A Flow cannot reference another Flow as a step. This is planned for a future release.
- **Step name characters**: Step names must match `^[a-z][a-z0-9-]*$`. When referenced in expressions, hyphens become underscores.
- **Result values are strings**: All step results are stored as strings. Numeric and boolean values must be cast in CEL conditions using `int()`, `double()`, or `bool()`.
- **`trigger.body` in CEL is always the raw string**, even for JSON payloads — only string methods (`.contains()`, etc.) work on it directly. Use `trigger.bodyFields` for typed, nested field access into JSON/form-urlencoded bodies; see [Using Trigger Data in CEL Conditions](#using-trigger-data-in-cel-conditions). XML and other unparsed content types leave `trigger.bodyFields` as an empty map — raw-string matching is the only option there.
- **Secret resolution**: `$(secrets.name.key)` values are resolved at step execution time and are never stored in the Flow spec or status.
- **Execution history**: Every execution creates a `FlowRun` CRD with full trigger metadata, step results, and timing. See [FlowRun CRD](flowrun.md). Summary statistics (`lastResult`, `executionCount`) are also recorded on the `Flow` status.
