# AMQP Gateway Setup

<!-- TODO: write this guide -->

This guide will cover setting up the KubeZap AMQP gateway for RabbitMQ (AMQP 0-9-1) and ActiveMQ Artemis (AMQP 1.0).

## Planned sections

- Prerequisites (broker, KubeZap installed)
- Creating an AMQP Integration CRD
- Creating an AMQP Trigger
- Verifying the gateway Deployment is created
- Producing a test message and watching the FlowRun
- TLS and authentication options
- AMQP 0-9-1 vs AMQP 1.0 differences
- Troubleshooting

See [`docs/api/integration.md`](../api/integration.md) for the Integration spec reference (AMQP section) and [`docs/api/trigger.md`](../api/trigger.md) for the PubSubTrigger spec.
