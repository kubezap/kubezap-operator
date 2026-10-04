# LocalStack integration tests

Tests for the AWS messaging plugin (`internal/plugin/awsmessaging`) that run
against [LocalStack](https://github.com/localstack/localstack), a local
emulator for the AWS SQS and SNS APIs. No AWS account is needed and nothing
costs money. The unit tests in `internal/plugin/awsmessaging` use fake AWS
clients. This suite runs the **real** subscriber and publisher code with the
real AWS SDK against LocalStack, and runs the Kubernetes side against an
[envtest](https://book.kubebuilder.io/reference/envtest) API server that loads
the real CRDs from `config/crd/bases`.

| Spec | What it proves |
| --- | --- |
| `creates exactly one FlowRun per SQS message and deletes the message` | The real `Subscriber` runs with the same wiring as `cmd/aws-messaging-plugin` (a namespace-scoped Trigger informer feeding `Subscriber.EventHandler`). A message sent to the queue becomes exactly one FlowRun named `<trigger>-msg-<MessageId>`, with the expected labels, `triggerRef`, `triggerData` (body and headers) and `kubezap.io/traceparent` annotation. The SQS message is then deleted: the queue shows no visible and no in-flight messages. |
| `treats a visibility-timeout redelivery of the same MessageId as 409-is-success` | The test plays a consumer that received the message, created its FlowRun and crashed before `DeleteMessage`. It uses a 2s visibility timeout, so SQS redelivers the same `MessageId` to the real subscriber. The subscriber gets `409 AlreadyExists` and treats it as success. Afterwards there is still exactly one FlowRun (same UID, so the original was not replaced) and the message has been deleted. |
| `publishes to a standard topic ...` | The real `POST /publish` handler (`Publisher.Mux()` behind `httptest`) publishes to a LocalStack SNS topic. A subscribed SQS queue (raw message delivery) receives the body, the request headers as message attributes, and `traceparent`. |
| `publishes to a .fifo topic and deduplicates ...` | The same flow for a FIFO topic and FIFO queue. Two publishes with the same `idempotencyKey` (sent as `MessageDeduplicationId`) are followed by a sentinel with a different key. The queue receives exactly `first, sentinel`, so dedup works end to end. |

## Build tag and skipping

Every file carries `//go:build localstack`, so `make test` and `go test ./...`
never compile this suite. Run it with `-tags localstack`. If
`AWS_ENDPOINT_URL` is unset, the suite calls `t.Skip`, so a tagged run can
never reach real AWS by accident.

## Running locally

1. Start LocalStack. The image is pinned on purpose (see the next section):

   ```bash
   docker run -d --rm --name kubezap-localstack -p 4566:4566 \
     -e SERVICES=sqs,sns -e SQS_ENDPOINT_STRATEGY=path \
     -e LOCALSTACK_HOST=localhost:4566 \
     localstack/localstack:4.14.0
   curl -s http://localhost:4566/_localstack/health   # sqs/sns: "available" or "running"
   ```

2. Run the suite. The target installs envtest binaries into `bin/` and sets
   `AWS_ENDPOINT_URL=http://localhost:4566` unless you have already set it:

   ```bash
   make test-localstack
   ```

   You can also run it by hand (for example from an IDE):

   ```bash
   make setup-envtest
   export AWS_ENDPOINT_URL=http://localhost:4566
   export KUBEBUILDER_ASSETS="$(bin/setup-envtest use -p path --bin-dir bin)"
   go test -tags localstack -count=1 -v ./test/localstack/...
   ```

   If `KUBEBUILDER_ASSETS` is unset, the suite falls back to the first
   directory under `bin/k8s/`.

3. Stop LocalStack: `docker stop kubezap-localstack`.

The suite uses LocalStack's default credentials (`test`/`test`) and region
`us-east-1`. You can override them with `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY` and `AWS_REGION`. Every spec creates its own queue,
topic and namespace with unique names and deletes the AWS resources when it
finishes, so you can reuse one LocalStack container for repeated runs.

## Why LocalStack is pinned to 4.14.0

Starting with the calendar-versioned releases (`2026.x`, and `latest`), the
`localstack/localstack` image requires a `LOCALSTACK_AUTH_TOKEN`. Without one
it exits at startup with code 55 ("License activation failed"). `4.14.0`
(2026-02-26) is the last tag that runs the community edition without a token.
Its health endpoint reports `"edition": "community"`, and SQS and SNS
(including FIFO topics and queues) work. CI pins this tag by digest. Do not
upgrade to a `2026.x` tag unless the project decides to provision a LocalStack
auth token as a CI secret.

`SQS_ENDPOINT_STRATEGY=path` together with `LOCALSTACK_HOST=localhost:4566`
makes LocalStack return queue URLs such as
`http://localhost:4566/queue/us-east-1/000000000000/<name>`. Without these
settings it returns `localhost.localstack.cloud` hostnames, which only resolve
through public DNS. With them, CI does not depend on external DNS.

## CI

The `localstack-tests` job in `.github/workflows/ci.yml` starts the same image
as a GitHub Actions service container and waits for SQS and SNS to report
`available` or `running`. It then vets and lints the tagged package and runs
`make test-localstack`. It is a separate job, so the fast `Unit Tests` job
never waits for it.
