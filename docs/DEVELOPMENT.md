# Development Guide

## 1. Repository model

The project uses a simple two-stage branch model:

- `dev` — integration/development branch;
- `main` — release-ready branch.

Feature/fix/documentation work should be developed on a short-lived branch and reviewed through a pull request. Do not develop directly on `main`.

## 2. Repository layout

```text
.
├── android/                  Native Android tracker
│   └── app/src/main/
├── cmd/
│   ├── server/               Go API/realtime entrypoint
│   └── adminctl/             Internal administrative CLI
├── internal/
│   ├── app/                  HTTP/WebSocket handlers and integration tests
│   └── store/                PostgreSQL persistence/schema
├── web/                      React/Vite dispatcher
├── docs/                     Engineering/operations documentation
├── scripts/                  Deployment/backup/health/rotation helpers
├── Caddyfile                 Private reverse-proxy routing/security headers
├── docker-compose.yml        Base production services/networks
├── docker-compose.cloudflare.yml
├── docker-compose.local.yml
└── .github/workflows/        CI and Android release workflows
```

## 3. Toolchain

The CI configuration is the source of truth for supported build tooling.

| Area | CI toolchain |
| --- | --- |
| Backend | Go version from `go.mod` (currently Go 1.23) |
| Database tests | PostgreSQL/PostGIS `postgis/postgis:17-3.5-alpine` |
| Web | Node.js 22 + npm |
| Android | Temurin Java 17, Android SDK, Gradle 8.9 |
| Deployment validation | Docker + Docker Compose |

The Android project currently does not commit a Gradle wrapper; CI provisions Gradle 8.9 explicitly. Adding a wrapper is a recommended reproducibility improvement.

## 4. Local full-stack development

Create local environment configuration:

```bash
cp .env.example .env
```

Start the stack:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build
```

Open:

```text
http://localhost:8080
```

The local overlay publishes Caddy only. PostgreSQL remains on the internal Docker network.

Stop without deleting data:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml down
```

Do not add `-v` unless intentionally destroying the local PostgreSQL volume.

## 5. Backend development

Useful commands:

```bash
go mod tidy
gofmt -w .
go vet ./...
go test ./... -count=1
go build ./cmd/server
```

Integration tests require a PostgreSQL/PostGIS database and the `TEST_DATABASE_URL` environment variable.

Example local PostGIS test service:

```bash
docker run --rm -d \
  --name ambulance-test-postgis \
  -e POSTGRES_DB=ambulance \
  -e POSTGRES_USER=ambulance \
  -e POSTGRES_PASSWORD=integration-test \
  -p 5432:5432 \
  postgis/postgis:17-3.5-alpine

export TEST_DATABASE_URL='postgres://ambulance:integration-test@localhost:5432/ambulance?sslmode=disable'
go test ./... -count=1
```

Remove the test container when finished:

```bash
docker stop ambulance-test-postgis
```

### Backend design rules

When changing telemetry or realtime behavior:

- preserve enqueue/retry idempotency around `(device_id, tracking_session_id, sequence_number)`;
- persist a new location before broadcasting it;
- do not add patient/clinical fields to the tracker payload casually;
- keep request/message limits bounded;
- add an integration test for authentication, persistence, replay, or session-boundary changes;
- keep dispatcher history scoped to a coherent tracking session unless the product requirement explicitly changes.

## 6. Web development

Install dependencies:

```bash
cd web
npm install --no-audit --no-fund
```

CI-equivalent checks:

```bash
npm run typecheck
npm run build
```

Run the Vite development server using the scripts defined in `web/package.json` when working on UI-only changes.

### UI design principles

The current dispatcher intentionally favors operational clarity over decorative dashboard content:

- real telemetry rather than invented analytics;
- flat dark surfaces rather than gradient-heavy styling;
- restrained status colors;
- clear fleet freshness states;
- map-first operational context;
- no fake incidents/assignments/reports without a backing domain model.

## 7. Android development

CI build command:

```bash
gradle -p android :app:assembleDebug
```

The debug APK is written under:

```text
android/app/build/outputs/apk/debug/app-debug.apk
```

Important Android requirements:

- min SDK 28;
- target/compile SDK 35;
- Java/Kotlin target 17;
- production tracker URLs must be HTTPS;
- foreground location permission/service behavior must remain valid for current Android platform rules.

When changing tracker transport or queue behavior, verify:

- the location is inserted into SQLite before network transmission;
- ACK deletion only removes the intended event;
- duplicates remain safe;
- reconnects do not discard queued records;
- tracker session IDs are not reused across independent service/app runs;
- user interaction with the map does not interfere with ongoing foreground tracking.

## 8. CI

`.github/workflows/ci.yml` runs on pull requests and on pushes to `dev`/`main`.

Current jobs:

### Backend

- start PostGIS 17 service;
- `go mod tidy`;
- fail if `gofmt` would change files;
- `go vet ./...`;
- `go test ./... -count=1`;
- `go build ./cmd/server`.

### Web

- Node.js 22;
- install dependencies;
- TypeScript typecheck;
- production build.

### Android

- Java 17 / Android SDK;
- Gradle 8.9;
- `:app:assembleDebug`;
- upload debug APK artifact for seven days.

### Deployment configuration

- syntax-check every `scripts/*.sh` file;
- validate Cloudflare production Compose configuration;
- validate local Compose configuration.

A pull request should not be treated as ready while any required CI lane is failing.

## 9. Integration tests

The backend integration suite currently covers more than handler-level unit behavior. It exercises the real PostgreSQL schema and HTTP/WebSocket server.

Key scenarios include:

- dispatcher login and session cookie creation;
- device credential exchange;
- dispatcher and tracker WebSocket connection;
- tracker presence event;
- location persistence;
- tracker ACK behavior;
- dispatcher live location broadcast;
- fleet snapshot persistence;
- route-history retrieval;
- duplicate telemetry idempotency;
- two simultaneous tracker connections;
- independent fleet state for multiple vehicles;
- route playback isolation to the newest tracker session;
- dispatcher password rotation and session revocation.

See [TEST_REPORT.md](TEST_REPORT.md) for the latest recorded evidence.

## 10. Database changes

The current schema is embedded from `internal/store/schema.sql` and applied idempotently at startup/testing.

Rules for current changes:

- prefer additive changes while the project remains in prototype/initial-deployment phase;
- add indexes deliberately for query patterns;
- preserve referential integrity between vehicles, devices, sessions, and events;
- ensure changes work on a clean database and an existing database.

Before introducing destructive migrations, table rewrites, or production data transformations, move to an explicit versioned migration framework rather than expanding the current `CREATE IF NOT EXISTS` approach indefinitely.

## 11. API changes

When an endpoint/message changes:

1. update implementation and tests;
2. update [API.md](API.md);
3. update [`openapi.yaml`](openapi.yaml) for REST changes;
4. update Android/web clients in the same PR when compatibility would otherwise break;
5. document any rollout ordering requirement.

`/api/v1` should remain backwards-compatible for deployed clients unless a coordinated breaking migration is explicitly planned.

## 12. Documentation changes

Documentation is part of the release surface. Update it in the same PR when changing:

- environment variables;
- deployment commands;
- API fields/limits;
- operational scripts;
- authentication behavior;
- database retention;
- Android permissions/provisioning;
- release/signing procedures;
- known production limitations.

## 13. Pull-request readiness checklist

Before requesting merge:

- code compiles/builds locally or in CI;
- tests relevant to the change exist and pass;
- no real secret/keystore/private key is committed;
- sample configuration contains placeholders only;
- API/docs are consistent with implementation;
- deployment changes validate under both Compose overlays;
- no new public port exposure is introduced accidentally;
- user-facing behavior has been tested on realistic viewport/device sizes where applicable.
