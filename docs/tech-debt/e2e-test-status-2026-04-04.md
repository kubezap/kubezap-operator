# E2E Test Run Status — 2026-04-04

## Run summary

- **Date**: 2026-04-04
- **Kind cluster**: `kubezap-test-e2e` (k8s 1.29.2)
- **Suite**: `test/e2e/` — 31 specs to run, 14 pending/Kafka-only (skipped)
- **Overall result**: FAIL
- **Exit**: suite timed out at 600 seconds

---

## Root Cause

**WATCH_NAMESPACES not set in e2e test deployment** — the controller is deployed in
OwnNamespace mode by default (watching only `kubezap-system`). E2E test namespaces
(`kubezap-e2e`, `kubezap-e2e-executor`, `kubezap-e2e-webhook`) are never watched,
so all triggers created in those namespaces are never reconciled and never reach
`Accepted = True`.

**Fix applied (2026-04-04)**: Added `kubectl set env deployment/kubezap-controller-manager WATCH_NAMESPACES=*`
to `test/e2e/e2e_suite_test.go` BeforeSuite, after `make deploy`.

---

## Failures by test file

### executor_test.go
- **FAILED**: `HTTP executor > HTTP step via executor > routes an HTTP step through the executor and returns 200`
  - Root cause: `executor-http-trigger` never accepted (WATCH_NAMESPACES issue)
  - Error: `Timed out after 120.001s. Trigger not yet Accepted`
  - Fix: WATCH_NAMESPACES fix above

- **SKIPPED**: `HTTP executor > HTTP step via executor > fails an HTTP step that targets an SSRF-blocked IP`
  - Cause: cascaded from previous failure

### webhook_test.go
- **FAILED**: `Webhook Trigger -> Transform -> HTTP -> Mockoon > should mark the Trigger as Accepted`
  - Root cause: `test-webhook` trigger in `kubezap-e2e-webhook` never accepted
  - Error: `Timed out after 120.001s. Trigger not yet Accepted`
  - Fix: WATCH_NAMESPACES fix above

- **SKIPPED**: Remaining webhook test specs (cascaded failure)

### feature_matrix_test.go
- **FAILED**: `Feature Matrix [BeforeAll] step types http step succeeds and maps result`
  - Root cause: `kubezap-e2e` namespace deleted by kubezap_e2e_test.go's cascading AfterAll
  - Error: `namespaces "kubezap-e2e" not found`
  - Fix: WATCH_NAMESPACES fix will prevent cascading namespace deletion

- **SKIPPED**: All feature matrix specs (cascaded)

### kubezap_e2e_test.go
- **TIMED OUT**: Suite hit 600s global timeout while waiting for `e2e-webhook` trigger
  - Root cause: `e2e-webhook` trigger in `kubezap-e2e` never accepted
  - Fix: WATCH_NAMESPACES fix above

---

## Unit tests: ALL PASS

All unit tests pass cleanly (`make test`):
- `internal/controller`: 60.8% coverage
- `internal/executor/http`: 63.3% coverage
- `internal/gateway/webhook`: 37.6%
- `internal/gateway/kafka`: 7.1%
- `internal/gateway/nats`: 7.8%
- `internal/gateway/amqp`: 11.3%
- `internal/gateway/redact`: 100%
- `internal/ui`: 61.5%
- `cmd/kubezap`: passes

---

## Fix Status

- [x] Root cause identified: WATCH_NAMESPACES not set in e2e BeforeSuite
- [x] Fix applied: `test/e2e/e2e_suite_test.go` BeforeSuite now sets `WATCH_NAMESPACES=*`
- [ ] Fix verified: re-run `make test-e2e` to confirm all tests pass after fix
  - See schedule §18 / §22 for re-run task

---

## Notes for Future Agents

- The fix is in `test/e2e/e2e_suite_test.go:129-134` (BeforeSuite)
- After the WATCH_NAMESPACES fix, the gateway image pull might also be a concern:
  the controller uses `ghcr.io/kubezap/webhook-gateway:latest` with `PullIfNotPresent`
  — BeforeSuite loads this image into Kind via `kind load docker-image`, which should work
- The http-executor image (`ghcr.io/kubezap/http-executor:latest`) is NOT loaded in BeforeSuite;
  if the executor test requires it, a similar `kind load` step may be needed
- Test namespaces use `pod-security.kubernetes.io/enforce=restricted` — gateway pods must
  comply (they do; already verified by OwnNamespace mode deployments)
