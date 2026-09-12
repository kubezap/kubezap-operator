# Project Context

This is a pointer, not a second source of truth. KubeZap doesn't have a SchoolCircle-style product-overview/requirements layer — it's infrastructure software (a Kubernetes operator), not an end-user product with personas and a pricing model, so Epics here cite these instead of a requirements catalog:

- **What KubeZap is and does**: [`docs/overview.md`](../docs/overview.md)
- **Architecture, runtime components, CRDs**: [`docs/architecture.md`](../docs/architecture.md), [`CLAUDE.md`](../CLAUDE.md)
- **Constraints that would be NFRs elsewhere** (security posture, RBAC least-privilege, multi-namespace support, OperatorHub/OLM certification requirements) live directly in `CLAUDE.md`'s Runtime Architecture and Development Philosophy sections — cite those, don't duplicate them here.
- **Technical decisions**: [`docs/design/`](../docs/design/README.md) (indexed) — an Epic's "Related Design Docs" section should link here, not restate the reasoning.
- **Long-term goal**: OperatorHub-published, enterprise-ready, pluggable marketplace of integrations — see `CLAUDE.md`'s Project Overview section.
