# Ambulance Tracking

Real-time ambulance/device tracking system for Saint Kitts and Nevis, built around a native Android tracker, Go API/realtime service, PostgreSQL/PostGIS, and a browser-based dispatcher map.

The system is designed to keep collecting vehicle telemetry when Internet connectivity disappears: accepted fixes are written to an app-private SQLite queue before transmission, then replayed idempotently after reconnect.

> **Project status:** the core tracker → server → database → dispatcher path has passed automated integration testing and a first real Android-device validation. Production-hardening work is in progress; the remaining field acceptance tests and organizational controls are documented explicitly rather than treated as complete.

## Core capabilities

- Native Android/Kotlin foreground GNSS tracker.
- Enqueue-before-send SQLite offline queue with replay after reconnect.
- Per-device credentials exchanged for short-lived bearer sessions.
- Go HTTP/WebSocket API with realtime dispatcher broadcasts.
- PostgreSQL 17 + PostGIS persistence and geospatial indexing.
- React 19 + strict TypeScript dispatcher dashboard.
- MapLibre/OpenFreeMap live maps on Android and web.
- Explicit tracker presence plus LIVE / DELAYED / STALE / OFFLINE freshness states.
- 1h / 6h / 24h route playback scoped to the newest tracking session.
- Stationary GNSS-wander filtering and implausible-jump rejection.
- Cloudflare Tunnel deployment with no public origin IP or host HTTP/HTTPS ports required.
- Audit events, bounded input sizes, auth rate limiting, session revocation, backup/restore tooling, and production Android signing workflow.

## Architecture at a glance

```text
Android tracker ── HTTPS/WSS ──> Cloudflare Tunnel ──> Caddy ──> Go API ──> PostgreSQL/PostGIS
                                                  │             │
Dispatcher browser <──── HTTPS/WSS ───────────────┘             └── realtime fleet broadcasts
                                                  │
                                                  └──> React dispatcher
```

The supplied production Compose topology keeps PostgreSQL on an isolated Docker network. Caddy, API, web, and the tunnel connector communicate only on private Docker networks; no database or application service port is published directly on the host.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for component boundaries, data flows, failure behavior, session design, and scaling constraints.

## Technology stack

| Layer | Technology |
| --- | --- |
| Tracker | Native Android/Kotlin, foreground location service, SQLite, OkHttp, MapLibre |
| API/realtime | Go 1.23, HTTP + WebSockets |
| Data | PostgreSQL 17 + PostGIS |
| Dispatcher | React 19, strict TypeScript, Vite, MapLibre GL |
| Edge | Caddy + Cloudflare Tunnel |
| Deployment | Docker Compose |
| CI | GitHub Actions |

## Documentation

Professional project documentation lives under [`docs/`](docs/README.md):

- [Architecture](docs/ARCHITECTURE.md)
- [API + WebSocket reference](docs/API.md)
- [OpenAPI specification](docs/openapi.yaml)
- [Dispatcher and Android user guide](docs/USER_GUIDE.md)
- [Operations runbook](docs/OPERATIONS.md)
- [Development guide](docs/DEVELOPMENT.md)
- [Test and validation report](docs/TEST_REPORT.md)
- [Security model](docs/SECURITY.md)
- [Cloudflare deployment](docs/CLOUDFLARE.md)
- [HIPAA deployment boundary](docs/HIPAA.md)
- [Production acceptance checklist](docs/PRODUCTION_CHECKLIST.md)

## Local development

```bash
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build
```

Open:

```text
http://localhost:8080
```

The supplied local topology publishes only Caddy. PostgreSQL remains internal to Docker.

Backend CI-equivalent checks:

```bash
go mod tidy
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go build ./cmd/server
```

Web checks:

```bash
cd web
npm install --no-audit --no-fund
npm run typecheck
npm run build
```

Android debug build:

```bash
gradle -p android :app:assembleDebug
```

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for prerequisites and integration-test setup.

## Production deployment

The preferred deployment uses a remotely managed Cloudflare Tunnel. The origin host only needs outbound Internet connectivity.

Example published application:

```text
Hostname:    tracking.example.kn
Service URL: http://caddy:80
```

Server setup:

```bash
cp .env.cloudflare.example .env
# Replace every placeholder with production values.
chmod 600 .env
chmod +x scripts/*.sh
./scripts/deploy-cloudflare.sh
./scripts/healthcheck.sh
```

Do not expose PostgreSQL, API, or Caddy directly to the Internet as a shortcut around the tunnel topology.

See [docs/OPERATIONS.md](docs/OPERATIONS.md) and [docs/CLOUDFLARE.md](docs/CLOUDFLARE.md).

## Android tracker flow

On a provisioned phone:

1. Install the approved APK.
2. Enter the HTTPS server URL, vehicle code, and device key.
3. Grant precise location permission.
4. Tap **Start tracking**.
5. Verify the app reaches `Live` and the vehicle appears live on the dispatcher.

The tracker stores the long-lived configuration using Android Keystore-backed encryption.

Every accepted location fix is queued locally before network transmission. The server ACKs newly persisted or already-known duplicate events. Only then does the phone delete the matching queue item.

If all Internet connectivity disappears, GNSS can continue and telemetry remains queued for replay after reconnect.

See [docs/USER_GUIDE.md](docs/USER_GUIDE.md).

## API

REST endpoints:

```text
GET  /healthz
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/vehicles
GET  /api/v1/vehicles/{vehicleID}/history?hours=1..24
POST /api/v1/device/session
POST /api/v1/tracker/history
```

WebSocket endpoints:

```text
GET /api/v1/tracker/ws
GET /api/v1/dispatch/ws
```

Tracker events use immutable `(tracking_session_id, sequence_number)` identities. The database uniqueness key adds `device_id`, making retries idempotent.

Full protocol: [docs/API.md](docs/API.md)  
Machine-readable REST contract: [docs/openapi.yaml](docs/openapi.yaml)

## Testing status

The latest recorded production-hardening CI run in [docs/TEST_REPORT.md](docs/TEST_REPORT.md) passed all four lanes:

- backend formatting/vet/tests/build against PostGIS;
- web typecheck/build;
- Android debug APK build/artifact upload;
- shell/Compose deployment validation.

Backend integration coverage includes:

- dispatcher and tracker authentication;
- tracker and dispatcher WebSockets;
- presence events;
- location persistence;
- ACK/duplicate semantics;
- fleet/history reads;
- two simultaneous trackers;
- playback isolation to the newest tracking session;
- dispatcher password rotation/session revocation.

A first real-phone test also validated the critical Android → public edge → API → PostGIS → dispatcher path and offline replay. Extended field tests such as cellular-only driving, screen-locked operation, battery saver, two physical phones, four-hour soak, backup restore, and production-signed update-in-place remain formal release gates.

## Operations

Operational helpers include:

```text
scripts/deploy-cloudflare.sh
scripts/healthcheck.sh
scripts/backup-db.sh
scripts/verify-backup.sh
scripts/rotate-dispatcher-password.sh
scripts/rotate-device-key.sh
```

The production-hardening Compose stack also bounds Docker JSON logs and adds health checks for the database/web/edge path.

Never use `docker compose down -v` on a production system unless the explicit intent is to destroy named volumes.

## Security and HIPAA boundary

Tracker payloads intentionally contain vehicle/location telemetry only; there are no patient or clinical fields in the tracker schema.

The code includes supporting technical controls such as unique user/device identities, hashed credentials and sessions, secure-cookie assumptions, rate limiting, bounded inputs, audit events, private database networking, replay-safe telemetry, backups, and credential-rotation tooling.

**This does not make the software or deployment “HIPAA certified” or automatically HIPAA compliant.** Organizational compliance still depends on risk analysis, policies, workforce/access controls, incident response, managed devices, backups, BAAs, and other deployment decisions.

If Cloudflare will create, receive, maintain, or transmit ePHI, the deployment must use a Cloudflare offering and executed agreement appropriate to that use. Do not assume a normal self-service configuration is covered.

See [docs/SECURITY.md](docs/SECURITY.md) and [docs/HIPAA.md](docs/HIPAA.md).

## Mapping dependency

The current Android and dispatcher clients use OpenFreeMap-hosted styles/tiles backed by OpenStreetMap data. That is suitable for development and initial validation, but emergency operations should not depend indefinitely on an uncontracted public tile service.

Production should use a provider with appropriate availability terms or self-host the small Saint Kitts and Nevis map dataset.

## Repository workflow

Development flow:

```text
feature/fix/docs branch -> pull request -> dev -> release review -> main
```

`dev` is the integration branch. `main` is reserved for release-ready code.
