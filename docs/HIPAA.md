# HIPAA deployment boundary

This repository implements technical safeguards that support a HIPAA-regulated deployment. It does **not** by itself make an organization or deployment HIPAA compliant.

## Implemented technical controls

- unique dispatcher accounts and device identities
- bcrypt password hashing with work factor 12
- random opaque user/device sessions; only SHA-256 session-token hashes are persisted
- short-lived 15-minute device sessions and revocable 8-hour dispatcher sessions
- HttpOnly, Secure, SameSite=Strict dispatcher cookies in production
- CSRF validation for state-changing browser requests
- authorization on dispatcher and tracker endpoints
- per-device credentials; no fleet-wide shared API key
- bounded request and WebSocket payload sizes
- TLS termination expected at the reverse proxy; HTTP is not an approved production transport
- audit events for authentication, fleet reads, tracker connections, and offline replay
- immutable location-event identity via `(device_id, tracking_session_id, sequence_number)`
- database network is isolated from the public edge in the supplied Compose topology
- containers use a non-root application image; application container is read-only
- application payloads deliberately contain no patient identifiers or clinical data

## Required before a HIPAA-regulated production launch

1. Perform and document the required organizational risk analysis and risk-management process.
2. Execute BAAs with every service provider that creates, receives, maintains, or transmits ePHI.
3. If Cloudflare terminates/proxies traffic that contains ePHI, use a Cloudflare offering covered by an executed BAA. Do not assume a normal self-serve Cloudflare plan is sufficient.
4. Encrypt database volumes and backups at rest using organization-controlled infrastructure/key management.
5. Define and test backup, disaster recovery, emergency-mode operation, and restore procedures.
6. Centralize append-only audit logs with access controls, integrity protection, monitoring, and an organization-approved retention period.
7. Add MFA/SSO for dispatcher/admin accounts before production use.
8. Add account lockout/rate limiting at the edge and application layer.
9. Establish device enrollment, revocation, loss/theft, MDM, screen-lock, patching, and remote-wipe procedures.
10. Establish breach-response, workforce-access, termination, incident-response, and periodic-access-review procedures.
11. Keep PHI out of this service unless a documented use case and minimum-necessary data model require it. Prefer opaque incident IDs over patient identifiers.
12. Obtain legal/compliance review for the actual entity, jurisdiction, deployment topology, vendors, and data flows.

## Data classification

Vehicle position alone is not automatically PHI. It can become PHI when linked or linkable to an identifiable patient's care. Treat live and historical ambulance location as sensitive operational data regardless, and avoid adding patient names, diagnoses, DOBs, record numbers, or clinical notes to tracker payloads.
