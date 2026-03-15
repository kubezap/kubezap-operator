# Demo: Order Router Getting-Started Scenario

This demo walks through the core KubeZap feature set using a realistic order-routing
scenario: an incoming `POST /hooks/order-placed` webhook triggers a four-step flow that
extracts the order type, enriches the order via a mock API (using `responseSequence` to
alternate between express and standard responses), and then conditionally calls either
`/mock/notify-express` or `/mock/notify-standard` — always skipping one branch — so you
can observe conditional step execution and MockEndpoint request capture without any real
downstream services.

## Apply the sample CRs

```bash
kubectl apply -f config/samples/demo/mockendpoints.yaml
kubectl apply -f config/samples/demo/flow.yaml
kubectl apply -f config/samples/demo/trigger.yaml
```

Or apply the whole directory at once using Kustomize:

```bash
kubectl apply -k config/samples/demo/
```

## Send a test request

Forward the webhook gateway port if it is not already externally accessible:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

Fire the webhook (first call routes to the express path via the `responseSequence`):

```bash
curl -X POST http://localhost:8080/hooks/order-placed \
  -H "Content-Type: application/json" \
  -H "X-Order-Id: ord-001" \
  -d '{"event":"order.placed","orderId":"ord-001"}'
```

Run the same command a second time with a different order ID to exercise the standard path.

## Full instructions

See [docs/guides/getting-started.md](../../../docs/guides/getting-started.md) for the
complete walkthrough including how to inspect FlowRun status, view captured MockEndpoint
requests, and troubleshoot common issues.
