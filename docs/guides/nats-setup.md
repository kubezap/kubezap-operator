# NATS Gateway Setup

<!-- TODO: write this guide -->

This guide will cover setting up the KubeZap NATS gateway for NATS Core and NATS JetStream durable consumers.

## Planned sections

- Prerequisites (NATS cluster, KubeZap installed)
- Creating a NATS Integration CRD
- Creating a NATS Trigger (Core subject vs JetStream consumer)
- Verifying the gateway Deployment is created
- Publishing a test message and watching the FlowRun
- JetStream dedup keys and at-least-once delivery
- TLS and NKey/JWT credential options
- Troubleshooting

See [`docs/api/integration.md`](../api/integration.md) for the Integration spec reference (NATS section) and [`docs/api/trigger.md`](../api/trigger.md) for the PubSubTrigger spec.
