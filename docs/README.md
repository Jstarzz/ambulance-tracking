# Documentation

This directory contains the operational and engineering documentation for the ambulance tracking system.

## Start here

- [Architecture](ARCHITECTURE.md) — system boundaries, components, data flows, storage model, failure behavior, security boundaries, enrollment/MFA/crash/ETA flows, and scaling constraints.
- [API reference](API.md) — REST and WebSocket protocol, authentication, schemas, limits, ACK/NACK semantics, administrative provisioning, events, ETA, and machine access.
- [User guide](USER_GUIDE.md) — dispatcher and Android tracker usage, including one-code device enrollment.
- [Operations](OPERATIONS.md) — deployment, configuration, health checks, backups, restore drills, credential rotation, upgrades, and signed Android releases.
- [Performance and scaling](PERFORMANCE.md) — live/replay network efficiency, gzip replay, database/query strategy, UI rendering, ETA caching, Redis decision, and scale-out roadmap.
- [AI integration](AI_INTEGRATION.md) — read-only AI boundary, scoped tokens, context endpoints, safe tool design, availability isolation, and future use cases.
- [Development](DEVELOPMENT.md) — repository layout, prerequisites, local development, CI-equivalent commands, and contribution workflow.
- [Test report](TEST_REPORT.md) — automated CI evidence, integration coverage, first real-device validation, and remaining field acceptance tests.
- [OpenAPI specification](openapi.yaml) — machine-readable REST API contract.
- [Security model](SECURITY.md) — trust boundaries, TOTP MFA, machine identities, secret handling, auditability, network exposure, and known production gaps.
- [Cloudflare deployment](CLOUDFLARE.md) — tunnel-specific setup and edge configuration.
- [HIPAA deployment boundary](HIPAA.md) — technical safeguards versus organizational compliance responsibilities.
- [Production acceptance checklist](PRODUCTION_CHECKLIST.md) — release gate for field validation and operations readiness.
- [Project changelog](../CHANGELOG.md) — chronological capability changes from the initial tracker through the current next-generation operations pass.

## Documentation principles

The documentation distinguishes three things that are easy to accidentally blur together:

1. **Implemented behavior** — code that exists in the repository and is covered by build/test paths.
2. **Field-validated behavior** — behavior actually exercised on physical hardware/deployment.
3. **Planned/conditional architecture** — designs such as horizontal API replication or shared pub/sub that are documented for scale but intentionally not deployed yet.

The project does not claim that source code alone makes a deployment HIPAA compliant, that an advisory phone-sensor alert is a confirmed crash, or that an approximate ETA is a road-routing result. Those boundaries are kept explicit in both UI and documentation.
