# Cron Triggers

A cron trigger fires a Flow on a schedule using standard cron syntax. Common use cases:

- Nightly reports and summaries
- Periodic data sync jobs
- Scheduled health checks and alerting
- Cleanup jobs and TTL enforcement

---

## Prerequisites

- KubeZap operator running in your cluster
- A `Flow` that describes the work to do

---

## Basic Example

This example calls a reporting API every night at 2 AM UTC and posts a summary to a Slack webhook.

**Flow:**

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: nightly-report
  namespace: automation
spec:
  timeout: 5m
  steps:
    - name: fetch-report
      action:
        type: http
        http:
          url: "https://reports.internal/api/daily-summary"
          method: GET
          headers:
            Authorization: "Bearer $(secrets.reporting-api-creds.token)"
          timeoutSeconds: 60
          resultMappings:
            summary: "$.summary"
            recordCount: "$.recordCount"

    - name: post-to-slack
      runAfter: [fetch-report]
      action:
        type: http
        http:
          url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
          method: POST
          body: |
            {"text": "Nightly report: $(steps.fetch_report.results.summary) — $(steps.fetch_report.results.recordCount) records"}
          headers:
            Content-Type: application/json
```

**Trigger:**

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: nightly-report
  namespace: automation
spec:
  type: cron
  cron:
    schedule: "0 2 * * *"    # 2:00 AM UTC every day
  flowRef:
    name: nightly-report
```

```bash
kubectl apply -f flow.yaml trigger.yaml

# Confirm the trigger is accepted
kubectl get trigger nightly-report -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True
```

---

## Cron Schedule Syntax

KubeZap uses standard [Quartz/cron](https://en.wikipedia.org/wiki/Cron#CRON_expression) syntax with five fields:

```
┌─────────── minute  (0-59)
│ ┌───────── hour    (0-23)
│ │ ┌─────── day     (1-31)
│ │ │ ┌───── month   (1-12)
│ │ │ │ ┌─── weekday (0-7, 0=Sun, 7=Sun)
│ │ │ │ │
* * * * *
```

Common examples:

| Schedule       | When                               |
| -------------- | ---------------------------------- |
| `* * * * *`    | Every minute                       |
| `*/15 * * * *` | Every 15 minutes                   |
| `0 * * * *`    | Top of every hour                  |
| `0 2 * * *`    | 2:00 AM daily                      |
| `0 2 * * 1`    | 2:00 AM every Monday               |
| `0 0 1 * *`    | Midnight on the 1st of every month |
| `30 8 * * 1-5` | 8:30 AM Mon–Fri                    |

Use [crontab.guru](https://crontab.guru) to interactively build and verify expressions.

---

## Timezones

By default, cron schedules run in UTC. To run in a specific timezone, set `spec.cron.timezone`:

```yaml
spec:
  type: cron
  cron:
    schedule: "0 9 * * 1-5"   # 9:00 AM in the local timezone
    timezone: "America/New_York"
```

The timezone value must be a valid [IANA timezone name](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones).

Common timezones:

| Timezone              | UTC offset (winter) |
| --------------------- | ------------------- |
| `America/New_York`    | UTC-5               |
| `America/Chicago`     | UTC-6               |
| `America/Los_Angeles` | UTC-8               |
| `Europe/London`       | UTC+0               |
| `Europe/Berlin`       | UTC+1               |
| `Asia/Tokyo`          | UTC+9               |
| `Australia/Sydney`    | UTC+11              |

---

## FlowRun Naming

Cron triggers name their FlowRuns `<trigger-name>-<scheduled-time>`:

```
nightly-report-2026-03-18T020000Z
nightly-report-2026-03-19T020000Z
```

The timestamp is the scheduled fire time (not the actual execution time). This is the dedup key — replaying a trigger on the same schedule tick does not create duplicate FlowRuns.

---

## Accessing Trigger Metadata in the Flow

Cron triggers do not have an incoming payload, but the scheduled time is available:

| Expression                 | Value                                         |
| -------------------------- | --------------------------------------------- |
| `$(trigger.name)`          | Name of the Trigger CRD                       |
| `$(trigger.namespace)`     | Namespace                                     |
| `$(trigger.type)`          | `cron`                                        |
| `$(trigger.scheduledTime)` | ISO 8601 timestamp of the scheduled fire time |

Example — include the scheduled time in the report request:

```yaml
url: "https://reports.internal/api/summary?date=$(trigger.scheduledTime)"
```

---

## Rate Limiting (Cooldown)

Prevent accidental rapid re-execution with a cooldown policy:

```yaml
spec:
  type: cron
  cron:
    schedule: "*/5 * * * *"   # every 5 minutes
  cooldown:
    maxInvocations: 1
    window: "5m"
  flowRef:
    name: my-flow
```

This ensures at most 1 execution per 5-minute window, even if the controller restarts and the cron fires multiple times in quick succession.

---

## Managing FlowRun History

Cron triggers can accumulate many FlowRuns over time. Configure garbage collection to keep the history manageable:

```yaml
spec:
  type: cron
  cron:
    schedule: "0 2 * * *"
  flowRef:
    name: nightly-report
  flowRunGC:
    maxSucceeded: 30    # keep the 30 most recent successful runs
    maxFailed: 10       # keep the 10 most recent failed runs
    ttlAfterSucceeded: 168h  # also delete succeeded runs after 7 days
    ttlAfterFailed: 720h     # keep failed runs for 30 days for investigation
```

To keep a specific FlowRun indefinitely (e.g., for audit), annotate it:

```bash
kubectl annotate flowrun nightly-report-2026-03-01T020000Z kubezap.io/retain=true
```

---

## Monitoring Cron Execution

### Check the last run

```bash
kubectl get trigger nightly-report -o jsonpath='{.status.lastTriggeredTime}'
```

### View recent FlowRuns

```bash
kubezap history --trigger nightly-report --since 7d
```

### Check if a scheduled run happened

```bash
# Did the nightly job run this morning?
kubezap history --trigger nightly-report --since 24h
```

### Prometheus alert for missed runs

```yaml
# Alert if no FlowRun has been created in the last 25 hours (expected every 24h)
- alert: CronJobMissed
  expr: |
    (time() - kubezap_trigger_last_fired_timestamp{trigger="nightly-report"}) > 90000
  labels:
    severity: warning
  annotations:
    summary: "Cron trigger nightly-report has not fired in 25+ hours"
```

See [Observability](observability.md) for the full metrics reference.

---

## Temporarily Pausing a Cron Trigger

Set `spec.enabled: false` to pause without deleting:

```bash
kubectl patch trigger nightly-report --type=merge -p '{"spec":{"enabled":false}}'
```

Re-enable when ready:

```bash
kubectl patch trigger nightly-report --type=merge -p '{"spec":{"enabled":true}}'
```

The next scheduled tick after re-enabling will create a FlowRun normally.

---

## Designing Flows for Cron

A few patterns specific to cron-triggered flows:

**Make flows idempotent** — cron triggers may fire twice in edge cases (controller restart during a scheduled second). Design your Flow so that running it twice with the same `scheduledTime` produces the same outcome: use the scheduled time as an idempotency key in external API calls when possible.

**Keep flows bounded** — use `spec.timeout` on the Flow and per-step `timeoutSeconds` to ensure a misbehaving step does not block the next scheduled run. A nightly flow that runs for 25 hours will interfere with the next day's run.

**Use params for the scheduled window** — pass the scheduled time as a param to make the Flow reusable and testable:

```yaml
spec:
  params:
    - name: reportDate
      required: false
      default: "$(trigger.scheduledTime)"
  steps:
    - name: fetch-data
      action:
        type: http
        http:
          url: "https://reports.internal/api/summary?date=$(params.reportDate)"
```

You can then test the flow manually by creating a FlowRun with a specific date:

```bash
kubectl apply -f - <<EOF
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: nightly-report-manual-test
  namespace: automation
spec:
  flowRef:
    name: nightly-report
  params:
    - name: reportDate
      value: "2026-03-15T02:00:00Z"
  triggerRef:
    name: nightly-report
    type: cron
EOF
```

---

## Next Steps

- [Webhook Security](webhook-security.md) — secure external triggers (not needed for cron, but useful if the same Flow is called by both triggers)
- [Observability](observability.md) — metrics and alerts for scheduled jobs
- [Troubleshooting](troubleshooting.md) — diagnose missed or stuck cron runs
- [Using the CLI](using-the-cli.md) — `kubezap history --trigger nightly-report` for quick inspection
