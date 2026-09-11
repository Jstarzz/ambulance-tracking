# Ambulance Tracking

Real-time ambulance/device tracking prototype for Saint Kitts and Nevis.

## Stack

- **Tracker:** native Android/Kotlin, foreground GNSS service, app-private SQLite offline queue
- **API/realtime:** Go, HTTP + WebSockets
- **Data:** PostgreSQL 17 + PostGIS
- **Dispatcher:** React 19 + strict TypeScript + MapLibre
- **Edge:** Caddy/TLS; optional Cloudflare in front only when the deployment's compliance/vendor agreements allow it
- **Deployment:** Docker Compose on a single on-island server

The tracker captures location locally before transmission. A lost Wi-Fi/cellular connection therefore does not stop GNSS collection: records remain queued and are replayed idempotently after connectivity returns.

## Repository workflow

Development happens through pull requests into `dev`; `main` is reserved for release-ready code.

## Run the server/dashboard

```bash
cp .env.example .env
# Replace every example password/key before exposing the host.
docker compose up --build
```

For local development the sample config uses `localhost` and non-secure browser cookies. For a public deployment set at least:

```text
APP_HOST=tracking.example.kn
PUBLIC_ORIGIN=tracking.example.kn
SECURE_COOKIES=true
POSTGRES_PASSWORD=<random secret>
DATABASE_URL=postgres://ambulance:<same secret>@db:5432/ambulance?sslmode=disable
BOOTSTRAP_ADMIN_USERNAME=<initial admin>
BOOTSTRAP_ADMIN_PASSWORD=<strong initial password>
DEMO_VEHICLE_CODE=AMB-01
DEMO_DEVICE_KEY=<random per-device secret>
```

Do not publish the PostgreSQL port. The supplied Compose topology keeps it on an internal Docker network.

## Android tracker

Build a debug APK with:

```bash
gradle -p android :app:assembleDebug
```

On the phone:

1. Enter the HTTPS server URL, vehicle code, and provisioned device key.
2. Grant precise location permission.
3. Tap **Start tracking** while the app is visible. Android starts a location foreground service and displays the required ongoing notification.
4. Wi-Fi is not required. GNSS continues without internet access; live transmission can use cellular data. If all internet connectivity disappears, fixes remain in the local SQLite queue and replay when the server becomes reachable again.

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

## HIPAA boundary

This code implements technical safeguards intended to support a HIPAA-regulated deployment: unique user/device identities, encrypted transport assumptions, secure session handling, audit events, least-exposed networking, replay-safe telemetry, and deliberate exclusion of patient data.

**Code alone cannot make an organization or deployment HIPAA compliant.** Before any ePHI is introduced, complete the operational requirements in [`docs/HIPAA.md`](docs/HIPAA.md), including risk analysis, BAAs, access procedures, backups, incident response, device management, production MFA/SSO, and encryption/backup controls.

If Cloudflare will create, receive, maintain, or transmit ePHI, do not put a normal self-serve Cloudflare plan in that path and call it compliant; use a service/plan covered by an executed BAA or keep that traffic off Cloudflare.

## Prototype mapping note

The dispatcher currently uses OpenStreetMap's public raster tile endpoint for light prototype use. Do not treat the public OSM tile service as emergency-grade/offline infrastructure. A production EMS deployment should use an appropriate tile provider or self-host the small Saint Kitts and Nevis map dataset.
