# Documentation

This directory contains the operational and engineering documentation for the ambulance tracking system.

## Start here

- [Architecture](ARCHITECTURE.md) — system boundaries, components, data flows, storage model, failure behavior, and scaling constraints.
- [API reference](API.md) — REST and WebSocket protocol, authentication, schemas, limits, ACK/NACK semantics, and examples.
- [User guide](USER_GUIDE.md) — dispatcher and Android tracker usage.
- [Operations](OPERATIONS.md) — deployment, configuration, health checks, backups, restore drills, credential rotation, upgrades, and signed Android releases.
- [Development](DEVELOPMENT.md) — repository layout, prerequisites, local development, CI-equivalent commands, and contribution workflow.
- [Test report](TEST_REPORT.md) — automated CI evidence, integration coverage, first real-device validation, and remaining field acceptance tests.
- [OpenAPI specification](openapi.yaml) — machine-readable REST API contract.
- [Security model](SECURITY.md) — trust boundaries, identities, secret handling, auditability, network exposure, and known production gaps.
- [Cloudflare deployment](CLOUDFLARE.md) — tunnel-specific setup and edge configuration.
- [HIPAA deployment boundary](HIPAA.md) — technical safeguards versus organizational compliance responsibilities.
- [Production acceptance checklist](PRODUCTION_CHECKLIST.md) — release gate for field validation and operations readiness.

## Documentation scope

The documentation describes the code on the `feat/production-hardening` line as of 2026-09-11, including the native Android tracker, Go API/realtime service, PostgreSQL/PostGIS storage, React dispatcher, Caddy edge, Cloudflare Tunnel deployment, and the production-hardening work in PR #10.

Where the implementation is intentionally not yet production-complete, the documents call that out explicitly rather than presenting a future design as current behavior.
