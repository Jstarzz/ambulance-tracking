# Ambulance Tracking Deployment Validation and Capacity Status

## Deployment under test

The current production/staging deployment was validated on:

```text
Proxmox VM ID:   102
VM name:         ambulance-tracking
VM IP:           172.16.0.69
vCPU:            4
RAM:             4 GiB
VM disk:         50 GiB
Datastore:       bigstorage (HDD-backed, ~5.3 TB pool)
Public URL:      https://tracking.itsjosiahdavis.dev
```

The VM disk is thin/sparse on the large datastore. At deployment time the datastore still had roughly 4.8 TB free, so the ambulance VM did not consume the Proxmox root disk.

Docker versions observed during deployment:

```text
Docker Engine 29.8.0
Docker Compose 5.5.1
```

## End-to-end validation completed

The deployed stack was verified beyond container startup.

### Infrastructure

- Proxmox guest agent running.
- VM reachable on the LAN.
- Docker engine healthy.
- PostgreSQL/PostGIS container healthy.
- API container running.
- React web container running.
- Caddy container running.
- cloudflared container running.
- No host-published PostgreSQL/API/Caddy ports required for public service.

### Cloudflare Tunnel

The `ndhis-ambulance-prod` connector established multiple outbound QUIC connections to Cloudflare.

Public health check:

```text
GET https://tracking.itsjosiahdavis.dev/healthz
-> HTTP 200
-> {"status":"ok"}
```

The public dashboard also returned HTTP 200 through the Cloudflare path.

### Authentication

Verified:

- dispatcher login succeeds with provisioned credentials;
- a dispatcher session cookie is issued;
- device session exchange succeeds for the provisioned ambulance tracker; and
- authenticated WebSocket endpoints accept valid sessions.

### Realtime tracking

Verified:

- dispatcher WebSocket connection established;
- tracker WebSocket connection established;
- simulated location telemetry persisted to PostgreSQL;
- the server ACKed the telemetry; and
- the same update was broadcast to the dispatcher path.

This proves the server-side path:

```text
tracker authentication
 -> WebSocket
 -> validation
 -> PostgreSQL
 -> ACK
 -> dispatcher WebSocket
```

The physical Android phone/cellular field test remains the appropriate final validation for real GNSS/mobile-network behavior whenever a new build/device is provisioned.

### Offline/replay design

The code implements an app-private SQLite queue and an idempotent server identity of `(device_id, tracking_session_id, sequence_number)`. The server's bounded replay endpoint returns accepted/duplicate counts.

A release test should still explicitly exercise airplane-mode/network-loss recovery on the physical Android device before an operational rollout.

## Current capacity statement

A formal concurrent-device/RPS load benchmark has **not yet been run on this deployed ambulance VM**.

Therefore, no professional capacity document should claim a measured maximum number of ambulances, dispatcher browsers, WebSocket sessions, or HTTP requests per second.

What has been proven is functional capacity for the current prototype workflow on 4 vCPU / 4 GiB RAM:

- PostgreSQL/PostGIS + API + React + Caddy + cloudflared run concurrently;
- at least one authenticated tracker and dispatcher can maintain the realtime path end-to-end; and
- the persistence/realtime pipeline operates successfully through the public Cloudflare hostname.

This is an end-to-end functional result, not a scale ceiling.

## Recommended production capacity benchmark

Before assigning a fleet-size SLO, run a load test from a machine separate from VM 102.

The workload should model the real protocol rather than only `GET /healthz`.

### Tracker simulation

Simulate increasing concurrent tracker WebSockets, for example:

```text
10
25
50
100
250
500
```

At a realistic telemetry cadence such as one location every 1-5 seconds per active vehicle, record:

- connected tracker count;
- accepted locations/second;
- WebSocket disconnect/reconnect rate;
- ACK latency p50/p95/p99;
- PostgreSQL insert latency;
- database CPU/RAM/connections;
- Go API CPU/RAM;
- VM CPU/RAM/load;
- network throughput; and
- database-volume disk latency/utilization.

### Dispatcher simulation

Test increasing concurrent dispatcher WebSockets while trackers publish locations. Measure:

- connected dispatcher count;
- broadcast latency;
- slow-client behavior;
- API memory growth;
- failed WebSocket writes; and
- reconnect behavior.

The current in-process broadcast hub writes to each dispatcher with a two-second timeout. Large dispatcher fan-out therefore deserves explicit measurement before claiming high concurrency.

### Offline replay

Benchmark bounded replay batches up to the protocol maximum of 500 records while live tracking continues. Confirm replay does not starve live telemetry and that duplicate submissions remain idempotent.

### History playback

Measure 1-, 6-, and 24-hour playback queries with a realistically populated telemetry table. Confirm deterministic down-sampling and database indexes keep response time acceptable as history grows.

## Suggested acceptance criteria

Define operational requirements before testing. A reasonable prototype-to-production gate could include targets such as:

- zero lost acknowledged telemetry;
- no duplicate persisted event for the same event identity;
- tracker ACK p95 below an agreed threshold;
- dispatcher broadcast p95 below an agreed threshold;
- stable database connection count;
- bounded memory usage during long-lived WebSockets;
- successful replay after service/network interruption; and
- successful backup/restore of the PostgreSQL volume.

The numerical latency/fleet targets should be agreed with the operational owner rather than invented after the benchmark.

## Failure/recovery checks for release

Each release should validate:

```text
[ ] API restart while database remains available
[ ] cloudflared restart/reconnect
[ ] Caddy restart
[ ] PostgreSQL health failure is reflected by /healthz
[ ] tracker reconnect after short server outage
[ ] dispatcher reconnect after short server outage
[ ] Android offline queue replays after connectivity restoration
[ ] duplicate replay is ACKed without duplicate database row
[ ] PostgreSQL backup can be restored into a clean environment
```

## Interpretation

The current deployment is a successfully validated end-to-end prototype/staging system. Its architecture is suitable for measuring real fleet capacity, but that capacity has not yet been empirically established. Until the protocol-level load test is complete, report the system as **functionally validated, scale benchmark pending**.
