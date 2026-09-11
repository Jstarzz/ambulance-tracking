# Ambulance Tracking Documentation

Production/prototype handoff documentation:

- [API and Realtime Protocol](API.md) — dispatcher/device authentication, HTTP endpoints, WebSocket contracts, telemetry validation, ACK/replay behavior, and error handling.
- [Production Architecture](ARCHITECTURE.md) — Proxmox/Docker/Cloudflare topology, data flow, offline queue, authentication, database, and availability boundaries.
- [Deployment Validation and Capacity Status](DEPLOYMENT_VALIDATION.md) — verified end-to-end deployment results, current server specification, what has and has not been load-tested, and a protocol-level capacity test plan.
- [Internal Security Audit](SECURITY_AUDIT.md) — implemented controls, findings, recommendations, and release checklist.
- [Cloudflare Deployment](CLOUDFLARE.md) — tunnel/deployment operating notes.
- [HIPAA Boundary](HIPAA.md) — operational/compliance considerations if regulated data is introduced.

The current deployment is functionally validated end-to-end. A formal concurrent-fleet/load ceiling has not yet been measured, so the documentation intentionally does not invent one.
