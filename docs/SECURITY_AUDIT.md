# Ambulance Tracking Internal Security Audit

## Scope and status

This document is an internal engineering security review of the current ambulance-tracking `dev` architecture and code. It is not a third-party penetration test, HIPAA certification, legal opinion, or substitute for an organizational risk assessment.

Reviewed areas include:

- dispatcher authentication/session handling;
- tracker-device authentication;
- WebSocket controls;
- telemetry validation/replay safety;
- audit logging;
- Docker/network exposure;
- Cloudflare Tunnel ingress;
- database isolation; and
- operational availability/recovery risks.

## Executive summary

The prototype has a solid technical baseline for its current scope. Positive controls include bcrypt-protected dispatcher credentials, server-side hashed session/device tokens, cryptographically random session material, CSRF protection for state-changing dispatcher actions, per-source authentication rate limiting, strict WebSocket origin checks, periodic session revalidation, bounded HTTP/WebSocket payloads, location validation, replay-safe event identities, Docker network segmentation, no published PostgreSQL port, Cloudflare Tunnel ingress, application audit events, CSP/security headers, and deliberate exclusion of patient/clinical data from the tracking protocol.

No obvious authentication bypass or direct database exposure was identified in this review.

The largest remaining production risks are operational identity maturity (local password login with no MFA/SSO), trust of forwarded source-IP headers, in-memory/non-distributed rate limiting, mutable container image tags, single-VM/database availability, external map-service dependency, and the organizational/vendor controls required if regulated patient data is ever introduced.

## Implemented controls

### Dispatcher password and session handling

- Bootstrap dispatcher passwords are hashed with bcrypt cost 12.
- Successful login creates cryptographically random 32-byte session and CSRF tokens.
- Session/CSRF values are stored server-side as SHA-256 hashes rather than plaintext.
- Production session cookies are `HttpOnly`, `Secure`, and `SameSite=Strict`.
- Logout requires CSRF validation and revokes the stored session.
- Dispatcher WebSockets periodically revalidate the session.

Assessment: **Good prototype baseline**.

### Tracker authentication

- Long-lived device keys are stored as SHA-256 digests.
- Comparisons use constant-time comparison.
- The long-lived key is exchanged for a short-lived random bearer session.
- Device session tokens are stored as hashes.
- Tracker WebSockets periodically revalidate the device session.

Assessment: **Good**.

### Brute-force controls

Login attempts are limited to 10 per minute per observed source IP. Device-session attempts are limited to 20 per minute per observed source IP. Invalid user passwords also incur a short delay.

Assessment: **Useful but prototype-grade; see SEC-02**.

### WebSocket controls

- Dispatcher and tracker WebSocket origins are checked against the configured public origin.
- Tracker messages have an 8-KiB read limit.
- WebSocket compression is disabled.
- Authentication is checked before accepting the session and revalidated during long-lived connections.

Assessment: **Good**.

### Telemetry integrity/replay behavior

Locations are range/time validated before persistence. The persistence identity `(device_id, tracking_session_id, sequence_number)` is idempotent, allowing the Android offline queue to retry safely without creating duplicate location rows.

Assessment: **Strong for intermittent mobile connectivity**.

### HTTP input bounds

- General JSON inputs use a bounded reader and reject unknown JSON fields.
- Tracker replay bodies are capped at 512 KiB.
- Replay batches are limited to 500 locations.
- History playback is bounded to 1-24 hours and 5,000 returned points.

Assessment: **Good**.

### Browser security headers

The API sets a Content Security Policy, `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy`, and frame restrictions. Caddy also sets transport/browser hardening headers.

Assessment: **Good**.

### Network/database isolation

- PostgreSQL has no published host port in the supplied Compose topology.
- The Go API and Caddy do not need public host ports for Cloudflare deployment.
- Cloudflare Tunnel provides public ingress over outbound-established connections.
- Database traffic stays on the private Docker `internal` network.

Assessment: **Good for the current single-host topology**.

### Audit events

The application records structured audit events for authentication, session creation, tracker connection/replay, dispatcher connection, fleet reads, and playback reads. Audit rows include actor/resource/outcome/source-IP metadata.

Assessment: **Good prototype baseline; database durability/retention/monitoring must be operationally managed**.

## Findings and recommendations

### SEC-01 — Forwarded client-IP headers are trusted without an explicit trusted-proxy check

**Severity: Medium**

The API accepts `CF-Connecting-IP` first and then `X-Forwarded-For` when determining the source IP. That is appropriate when every request can only arrive through the trusted Cloudflare/Caddy path, but the application itself does not verify that the immediate peer is a trusted proxy before honoring those headers.

Impact if the API is ever directly reachable: a client could spoof its audit/rate-limit source address.

Recommendation:

- keep the API origin non-public;
- configure a trusted-proxy model and only accept Cloudflare/forwarded headers from known proxy peers;
- otherwise fall back to `RemoteAddr`;
- include a regression test proving direct clients cannot spoof the address used for security decisions.

### SEC-02 — Authentication rate limiting is local process memory

**Severity: Medium**

The fixed-window limiter exists only in Go process memory. It resets on restart and would not coordinate if the API were later scaled to multiple replicas. Device/user identifiers also do not supplement the IP-based limiter.

Recommendation:

- retain the current limiter as a local fast guard;
- add edge-level rate limiting or a shared limiter for production;
- consider controls by account/device identifier as well as source IP;
- alert on repeated denied/rate-limited authentication audit events.

### SEC-03 — Dispatcher authentication has no MFA/SSO

**Severity: Medium; higher if production access includes sensitive operations/data**

The current dispatcher uses a local username/password session. This is appropriate for a prototype but is weaker than an organization-managed identity provider.

Recommendation:

- integrate NDHIS/organization SSO when available;
- require MFA for privileged dispatcher/admin access;
- map roles from an authoritative identity source;
- implement account disable/termination procedures and periodic access review.

### SEC-04 — Container images are not all immutable

**Severity: Medium**

The Compose files use version tags and `cloudflare/cloudflared:latest`. Mutable tags reduce deployment reproducibility and increase supply-chain risk.

Recommendation:

- pin production images by digest after validation;
- keep readable version metadata alongside the digest;
- update images through reviewed dependency changes;
- scan built application images in CI.

### SEC-05 — Database transport assumes one private Docker host

**Severity: Low in current topology; Medium if architecture changes**

The application database connection uses `sslmode=disable`. Today the API and PostgreSQL communicate over the private Docker network on one VM, with no published database port.

Recommendation:

- keep that network private if retaining the single-host design;
- if PostgreSQL moves to another host/network/trust boundary, enable TLS and server verification before migration;
- do not expose 5432 to the LAN/Internet as a shortcut.

### SEC-06 — Single VM/PostgreSQL volume is a single failure domain

**Severity: Medium operational risk**

Container restart policies protect against process failures, not VM/host/storage failure. The current PostgreSQL named volume is the system of record.

Recommendation:

- define RPO/RTO with the service owner;
- automate PostgreSQL backups to storage independent of the VM disk;
- encrypt and access-control backup copies;
- test full restore into a clean environment;
- monitor disk usage/database health;
- document Proxmox VM recovery.

The Android offline queue reduces data-loss risk during temporary outages, but it does not replace server backups.

### SEC-07 — External map tiles/styles are an operational dependency

**Severity: Medium for emergency-service production**

The UI currently depends on OpenFreeMap-hosted assets. A free public mapping service should not become a hidden availability dependency for emergency operations.

Recommendation:

- contract a provider with appropriate service terms, or
- self-host the required Saint Kitts and Nevis vector tiles/style assets;
- design the dispatcher so telemetry remains accessible even if map rendering is degraded.

### SEC-08 — Device-key lifecycle/provisioning needs operational controls

**Severity: Medium**

The cryptographic storage/verification of device keys is sound, but production security also requires a lifecycle around issuance, replacement, revocation, lost/stolen phones, and device reassignment.

Recommendation:

- unique key per device;
- record device owner/vehicle/purpose and issue date;
- support immediate server-side deactivation;
- rotate keys after suspected compromise/reprovisioning;
- use Android Keystore-backed storage;
- avoid copying keys through insecure messaging/logs.

### SEC-09 — Audit data requires retention/monitoring policy

**Severity: Low/Medium**

Application audit events exist, but the repository does not itself establish a complete retention, export, tamper-monitoring, or security-review program.

Recommendation:

- define audit retention appropriate to policy;
- protect audit rows/backups from ordinary user modification;
- export/monitor security-significant events where appropriate;
- test that backup/restore includes audit history required by policy.

### SEC-10 — Compliance is outside the code boundary

**Severity: High if ePHI is introduced; informational for current telemetry-only prototype**

The current tracker protocol deliberately contains vehicle/location telemetry and no patient/clinical fields. That is a useful scope reduction.

If patient or other regulated health information is later added, the deployment must undergo a new privacy/security review covering data minimization, organizational access controls, vendor agreements/BAAs where applicable, risk analysis, incident response, retention, device management, backups, and workforce procedures.

Do not call the application or deployment "HIPAA compliant" based on code controls alone.

## Release security checklist

```text
[ ] production SECURE_COOKIES=true
[ ] .env is mode 0600 and not committed
[ ] dispatcher password is strong and not a default
[ ] each physical tracker has a unique device key
[ ] database port 5432 is not published
[ ] API port 8080 is not publicly published
[ ] Caddy origin is not directly Internet-accessible
[ ] Cloudflare Tunnel is the intended public ingress
[ ] invalid dispatcher/device credentials are rejected
[ ] authentication rate limiting works
[ ] logout rejects missing/invalid CSRF
[ ] WebSocket Origin is enforced
[ ] expired/revoked user/device sessions are rejected
[ ] invalid/out-of-window GPS records are rejected
[ ] duplicate telemetry is idempotent
[ ] offline replay succeeds after network loss
[ ] audit events are generated for security-sensitive actions
[ ] PostgreSQL backup succeeds
[ ] restore into a clean environment is tested
[ ] container image versions/digests are reviewed
```

## Residual risk statement

The current system is a credible, hardened prototype/staging implementation for vehicle telemetry with a deliberately narrow data model. Before emergency-service production adoption, the most important next controls are authoritative dispatcher identity/MFA, trusted-proxy handling, shared/edge authentication-rate controls, backup/restore operationalization, immutable image pinning, and removal of free public map infrastructure as a critical dependency.
