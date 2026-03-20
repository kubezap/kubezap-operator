# Getting Started with KubeZap

The getting-started guide has moved to the examples directory:

**[examples/order-router/README.md](../../examples/order-router/README.md)**

The order-router example demonstrates the core KubeZap feature set:
webhook trigger → transform step → conditional branching → MockEndpoint capture.

```bash
# Apply the example
kubectl apply -k examples/order-router/

# Send a test request
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
curl -X POST http://localhost:8080/hooks/order-placed \
  -H "Content-Type: application/json" \
  -H "X-Order-Id: ord-001" \
  -d '{"event":"order.placed","orderId":"ord-001"}'
```

See [examples/](../../examples/) for all available examples.
