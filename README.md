# Ambulance Tracking

Real-time ambulance tracking prototype for Saint Kitts and Nevis, built around unreliable mobile connectivity and a map-first dispatcher workflow.

## Stack

- **Tracker:** native Android/Kotlin, foreground GNSS service, Android Keystore-backed configuration, app-private SQLite offline queue, MapLibre map
- **API/realtime:** Go, HTTP + WebSockets
- **Data:** PostgreSQL 17 + PostGIS
- **Dispatcher:** React 19 + strict TypeScript + MapLibre
- **Maps:** OpenFreeMap vector maps backed by OpenStreetMap data
- **Routing:** optional OSRM-compatible road router with explicit approximate ETA fallback
- **Edge:** Caddy on the private Docker network + Cloudflare Tunnel for public ingress
- **Deployment:** Docker Compose on a single server/VM

The tracker captures accepted GNSS locations locally before transmission. Losing Wi-Fi/cellular service therefore does not stop collection: records remain queued and replay idempotently after connectivity returns.

The dispatcher provides live fleet position, explicit tracker presence, route history, map-click route planning, and continuously refreshed ETA for a selected ambulance.

## Repository workflow

Development happens through pull requests into `dev`; `main` is reserved for release-ready code.

See [`CHANGELOG.md`](CHANGELOG.md) for notable behavior changes and [`docs/README.md`](docs/README.md) for the engineering/operations documentation set.

## Production deployment: no public IP required

The preferred deployment uses **Cloudflare Tunnel**. The server only needs outbound Internet access; the application publishes no host HTTP/HTTPS or PostgreSQL ports in the supplied production topology.

In Cloudflare, create a remotely-managed tunnel and add a published application such as:

```text
Hostname:    tracking.example.kn
Service URL: http://caddy:80
```

Then on the server:

```bash
cp .env.cloudflare.example .env
# Replace every placeholder, including the Cloudflare tunnel token.
chmod +x scripts/deploy-cloudflare.sh
./scripts/deploy-cloudflare.sh
```

See [`docs/CLOUDFLARE.md`](docs/CLOUDFLARE.md) for the deployment details.

## Local development

```bash
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build
```

Open `http://localhost:8080`.

PostgreSQL is never published by the supplied Compose topology.

## Android tracker

Build a debug APK with Android Studio or Gradle, or use the Android artifact produced by CI.

Normal provisioning is intentionally simple:

1. A dispatcher opens **Register tracker** and selects the ambulance.
2. The dashboard generates a short-lived, one-time 8-character code.
3. Enter that code on the ambulance phone.
4. Grant precise location permission and notification permission when Android requests them.
5. Tap **Start tracking** while the app is visible.

The production server origin is part of the Android build configuration and long-lived device credentials are issued/stored behind the enrollment flow; normal ambulance users do not type either value.

While tracking:

- Android runs an ongoing foreground location service;
- GPS/GNSS is preferred over network positioning;
- poor-accuracy fixes, stationary drift, and implausible jumps are filtered;
- the phone map follows the accepted location and draws a recent trail;
- the operational bottom sheet can be collapsed to expose more map;
- closing/reopening the activity restores tracker state immediately and then reconciles with the running service;
- the app reports whether Android battery optimization is active and links to battery settings; and
- if Internet connectivity disappears, accepted fixes remain in the local SQLite queue until the server becomes reachable again.

For Android/dispatcher/server requirements and Battery Saver caveats, see [`docs/SYSTEM_REQUIREMENTS.md`](docs/SYSTEM_REQUIREMENTS.md).

## Dispatcher route planning and ETA

Select an ambulance and press **Route**, then click a destination on the map. The dispatcher requests:

```http
GET /api/v1/vehicles/{vehicleID}/route?lat={latitude}&lon={longitude}
```

If `ROUTER_URL` points at an OSRM-compatible routing service, the response contains road geometry, route distance, duration, and ETA. An active route refreshes on a bounded 20-second cadence from the latest persisted vehicle location rather than calling the router for every 1 Hz telemetry update.

If the road router is absent or temporarily unavailable, the API returns an explicit `approximate: true` fallback. The UI labels it as approximate; it is not presented as turn-by-turn road navigation.

Configuration:

```text
ROUTER_URL=https://your-router.example
ETA_FALLBACK_KPH=35
```

See [`docs/TRACKER_LIFECYCLE_AND_ROUTING.md`](docs/TRACKER_LIFECYCLE_AND_ROUTING.md).

## Wire protocol

A provisioned tracker exchanges its long-lived device credential for a short-lived session:

```http
POST /api/v1/device/session
Content-Type: application/json

{"vehicle_code":"AMB-01","device_key":"..."}
```

It then opens:

```text
wss://<host>/api/v1/tracker/ws
Authorization: Bearer <short-lived token>
```

Each fix has an immutable `(tracking_session_id, sequence_number)` identity. The server ACKs accepted or duplicate records, allowing the phone to delete only confirmed local queue entries. Backlogged records use `POST /api/v1/tracker/history` in bounded batches.

Authenticated dispatchers receive location and tracker-presence events over `/api/v1/dispatch/ws`. Historical playback uses:

```http
GET /api/v1/vehicles/{vehicleID}/history?hours=1
```

`hours` is bounded to 1–24 and large result sets are deterministically down-sampled before they reach the browser.

See [`docs/API.md`](docs/API.md) for the protocol reference.

## Validation status

The public deployment has been functionally validated end-to-end, including authenticated tracker telemetry through persistence/ACK/broadcast and a real Android drive with offline collection/replay. The current validated VM baseline is documented in [`docs/DEPLOYMENT_VALIDATION.md`](docs/DEPLOYMENT_VALIDATION.md).

A formal concurrent-fleet capacity ceiling has **not** been measured, so the project does not claim one.

## HIPAA boundary

This repository contains technical safeguards that can support a regulated deployment, including unique user/device identities, secure-session design, least-exposed networking, replay-safe telemetry, and deliberate exclusion of patient fields.

**Code alone does not make an organization or deployment HIPAA compliant.** Before introducing ePHI, complete the organizational and deployment requirements in [`docs/HIPAA.md`](docs/HIPAA.md): risk analysis, BAAs where required, access procedures, backups, incident response, device management, identity assurance, and other administrative/physical safeguards.

## Mapping/routing dependency note

The current map uses OpenFreeMap-hosted vector resources. The optional route API can use an OSRM-compatible provider. A production EMS deployment should not make emergency operations depend on free public demonstration infrastructure; use contracted services with appropriate availability terms or self-host the small regional map/routing dataset.
