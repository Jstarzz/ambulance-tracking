# Ambulance Tracking Production Architecture

## Purpose

The Ambulance Tracking platform provides live and offline-capable ambulance/device location tracking for Saint Kitts and Nevis. A native Android tracker collects GNSS locations, the Go service persists telemetry and broadcasts updates, and a React dispatcher dashboard displays current fleet state and historical routes.

The deployed public URL is:

```text
https://tracking.itsjosiahdavis.dev
```

## Physical deployment

The current production/staging instance runs in a dedicated VM on the existing Proxmox host:

```text
Proxmox host
  `- VM 102: ambulance-tracking
       |- 4 vCPU
       |- 4 GiB RAM
       |- 50 GiB virtual disk
       `- Docker Compose
```

The virtual disk is allocated on the existing `bigstorage` HDD-backed Proxmox datastore rather than the small Proxmox root storage.

The VM uses the existing LAN bridge and does not require a public IP.

## Network topology

```text
Android tracker / Dispatcher browser
              |
              | HTTPS / WSS
              v
        Cloudflare edge
              |
              | outbound-established tunnel
              v
      cloudflared container
              |
              v
         Caddy :80
          /       \
         /         \
        v           v
   React web     Go API :8080
                    |
                    v
             PostgreSQL/PostGIS
```

The Docker topology intentionally publishes no database or API origin port to the public Internet. `cloudflared` reaches Caddy over Docker's private `edge` network at:

```text
http://caddy:80
```

Caddy routes `/api/*` and `/healthz` to the Go API and all other application requests to the web container.

## Docker networks

The Compose stack separates data-plane access:

```text
internal network:
  db <-> api

edge network:
  cloudflared <-> caddy <-> api/web
```

PostgreSQL is attached only to the internal network and has no published host port.

The API participates in both networks because it needs PostgreSQL internally and must receive requests from Caddy.

## Tracker data path

### Online path

```text
GNSS fix
  -> Android foreground tracking service
  -> short-lived device session
  -> WSS /api/v1/tracker/ws
  -> validation
  -> PostgreSQL insert
  -> ACK to phone
  -> realtime broadcast
  -> dispatcher WebSocket
  -> fleet map
```

A persisted location is identified by:

```text
(device_id, tracking_session_id, sequence_number)
```

The database treats that identity idempotently. If a phone retransmits an already accepted location, the server does not duplicate the row and returns an ACK marking it as a duplicate.

### Offline path

```text
GNSS fix
  -> app-private SQLite queue
  -> connectivity unavailable
  -> records retained locally
  -> connectivity restored
  -> POST /api/v1/tracker/history in bounded batches
  -> server validates/persists idempotently
  -> accepted/duplicate counts returned
  -> confirmed records removed from device queue
```

GNSS collection therefore does not depend on continuous cellular/Wi-Fi service.

## Authentication architecture

### Dispatcher

```text
username/password
  -> bcrypt verification
  -> random server session token
  -> session token hash stored in PostgreSQL
  -> HttpOnly + Secure + SameSite=Strict cookie
  -> CSRF token/hash for state-changing dispatcher actions
```

The bootstrap admin password is bcrypt-hashed with cost 12.

### Tracker device

```text
vehicle code + long-lived device key
  -> SHA-256/constant-time credential verification
  -> random short-lived session token
  -> session-token hash stored in PostgreSQL
  -> Bearer token for tracker WebSocket/history replay
```

The long-lived key is not sent on each telemetry message after session establishment.

## Realtime architecture

The Go service maintains dispatcher WebSocket connections in an in-memory hub.

Tracker updates are:

1. validated;
2. persisted first;
3. enriched with trusted device/vehicle identity; and
4. broadcast to connected dispatchers.

Dispatcher WebSocket sessions are periodically revalidated. Tracker sessions are also revalidated periodically while connected.

This design ensures the dispatcher does not receive a location as accepted before database persistence succeeds.

## Location validation

The server rejects telemetry that does not satisfy the contract, including:

- missing tracking-session ID;
- negative sequence number;
- zero timestamp;
- latitude outside -90..90;
- longitude outside -180..180;
- timestamp more than 5 minutes in the future; or
- timestamp more than 7 days old.

## Historical playback

Dispatcher playback supports 1-24 hour windows.

The database intentionally selects the most recent `tracking_session_id` within the requested window before building a route. This prevents separate journeys/app restarts/test sessions from being connected into a false route.

Large histories are deterministically down-sampled in PostgreSQL to a maximum of 5,000 returned points.

## Database

PostgreSQL 17 with PostGIS is the system of record for:

- users;
- user sessions;
- vehicles;
- devices;
- device sessions;
- telemetry/location events; and
- application audit events.

The database resides in the `postgres_data` Docker named volume and is intentionally preserved across application redeployments.

Ordinary updates must not run `docker compose down -v`.

## Audit trail

Security/operational actions are recorded in PostgreSQL with fields for:

- actor type/ID;
- action;
- resource type/ID;
- source IP;
- outcome; and
- structured metadata.

Examples include login success/denial/rate limiting, device-session creation, tracker connection, replay batches, fleet reads, playback reads, and dispatcher connections.

## Cloudflare boundary

Cloudflare Tunnel is the only intended Internet ingress. The VM makes outbound tunnel connections; no WAN NAT/port-forward rule is necessary.

This provides origin concealment and removes direct public exposure of Caddy/API/PostgreSQL, but Cloudflare is still an external dependency in the public request path.

Cloudflare Access browser authentication is not placed over the entire hostname because the Android tracker must make direct authenticated API/WebSocket connections.

## Mapping boundary

The current clients use OpenFreeMap-hosted vector styles/tiles based on OpenStreetMap data. That is appropriate for the prototype but is an external availability dependency.

A production emergency-service deployment should use a contracted map provider with suitable availability terms or self-host the small Saint Kitts and Nevis vector-tile dataset.

## Availability model

The deployed instance is intentionally simple and single-node:

```text
one Proxmox host
one VM
one Docker engine
one Go API container
one PostgreSQL instance
one Cloudflare connector container
```

Container restart policies provide process recovery, but this is not hardware high availability. Host failure, VM failure, local power/network loss, or database-volume loss can interrupt service.

The Android offline queue mitigates temporary server/network outages for telemetry collection: location fixes continue to be captured locally and can replay after the service returns.

## Update flow

The current deployment branch is `dev`.

Safe application update:

```bash
git fetch
git checkout dev
git pull --ff-only
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml config
./scripts/deploy-cloudflare.sh
```

Do not destroy the PostgreSQL volume as part of a routine deploy.
