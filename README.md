# Ambulance Tracking

Real-time ambulance/device tracking prototype for Saint Kitts and Nevis.

## Stack

- **Tracker:** native Android/Kotlin, foreground GNSS service, app-private SQLite offline queue, MapLibre client map
- **API/realtime:** Go, HTTP + WebSockets
- **Data:** PostgreSQL 17 + PostGIS
- **Dispatcher:** React 19 + strict TypeScript + MapLibre
- **Maps:** OpenFreeMap dark vector style backed by OpenStreetMap data
- **Edge:** Caddy on the private Docker network + Cloudflare Tunnel for public ingress
- **Deployment:** Docker Compose on a single on-island server

The tracker captures location locally before transmission. A lost Wi-Fi/cellular connection therefore does not stop GNSS collection: records remain queued and are replayed idempotently after connectivity returns.

Both the Android tracker and dispatcher show live position updates. The dispatcher centers on a fleet map of provisioned vehicles, receives explicit WebSocket node-presence events, and supports bounded historical route playback for the previous 1, 6, or 24 hours.

## Repository workflow

Development happens through pull requests into `dev`; `main` is reserved for release-ready code.

## Production deployment: no public IP required

The preferred deployment uses **Cloudflare Tunnel**. The St. Kitts server only needs outbound Internet access; the application publishes no host HTTP/HTTPS ports.

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

See [`docs/CLOUDFLARE.md`](docs/CLOUDFLARE.md) for the exact Cloudflare dashboard, firewall, verification, and local-development steps.

## Local development

```bash
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build
```

Open `http://localhost:8080`.

PostgreSQL is never published by the supplied Compose topology.

## Android tracker

Build a debug APK with Android Studio or Gradle.

On the phone:

1. Enter the HTTPS server URL, vehicle code, and provisioned device key.
2. Grant precise location permission.
3. Tap **Start tracking** while the app is visible. Android starts a location foreground service and displays the required ongoing notification.
4. The client map follows the current GNSS fix and draws the recent on-device trail while tracking is active.
5. Wi-Fi is not required. GNSS continues without internet access; live transmission can use cellular data. If all internet connectivity disappears, fixes remain in the local SQLite queue and replay when the server becomes reachable again.

Device secrets are encrypted with Android Keystore. Tracker payloads contain vehicle/location telemetry only; there are no patient or clinical fields.

## Wire protocol

A tracker first exchanges its long-lived per-device credential for a short-lived session:

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

## HIPAA boundary

This code implements technical safeguards intended to support a HIPAA-regulated deployment: unique user/device identities, encrypted transport assumptions, secure session handling, audit events, least-exposed networking, replay-safe telemetry, and deliberate exclusion of patient data.

**Code alone cannot make an organization or deployment HIPAA compliant.** Before any ePHI is introduced, complete the operational requirements in [`docs/HIPAA.md`](docs/HIPAA.md), including risk analysis, BAAs, access procedures, backups, incident response, device management, production MFA/SSO, and encryption/backup controls.

If Cloudflare will create, receive, maintain, or transmit ePHI, do not put a normal self-serve Cloudflare plan in that path and call it compliant. Cloudflare states that it only enters HIPAA BAAs with Enterprise customers.

## Prototype mapping note

The current client and dispatcher use OpenFreeMap-hosted vector tiles/styles with OpenStreetMap data. This is appropriate for the prototype, but a production EMS deployment should not make emergency operations depend on a free public tile service. For production, use a contracted provider with suitable availability terms or self-host the small Saint Kitts and Nevis vector-tile dataset.
