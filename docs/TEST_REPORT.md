# Test and Validation Report

**Project:** Ambulance Tracking  
**Report date:** 2026-09-11  
**Code under report:** `feat/production-hardening` at `9a693f819a78e66352bc3d2e024e3a68d782b4a1`  
**GitHub Actions run:** `ci` run #65 (`34650440923`)  
**Result:** **PASS**

This report separates three kinds of evidence:

1. automated CI/build validation;
2. backend integration-test coverage against a real PostGIS service;
3. first real-device functional validation already completed during development.

It does **not** convert unfinished field acceptance items into passes. Those remain listed at the end.

---

## 1. CI summary

All four CI jobs completed successfully for the reported commit.

| CI lane | Result | What passed |
| --- | --- | --- |
| Backend | PASS | dependency normalization, formatting check, `go vet`, full Go test suite, server build |
| Web | PASS | npm dependency install, strict TypeScript typecheck, production Vite build |
| Android | PASS | Java/Android environment setup, Gradle 8.9 debug APK build, debug APK artifact upload |
| Deployment config | PASS | syntax validation for every shell script, production Cloudflare Compose config, local Compose config |

Run URL:

```text
https://github.com/Jstarzz/ambulance-tracking/actions/runs/34650440923
```

### Backend lane

Environment:

- Ubuntu GitHub Actions runner;
- Go version from `go.mod`;
- PostGIS service image `postgis/postgis:17-3.5-alpine`;
- `TEST_DATABASE_URL` pointed at the ephemeral CI PostGIS service.

Successful steps:

```text
go mod tidy
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go build ./cmd/server
```

### Web lane

Environment:

- Node.js 22;
- npm;
- `web/` working directory.

Successful steps:

```text
npm install --no-audit --no-fund
npm run typecheck
npm run build
```

### Android lane

Environment:

- Temurin Java 17;
- Android SDK;
- Gradle 8.9.

Successful build:

```text
gradle -p android :app:assembleDebug
```

The workflow then successfully uploaded:

```text
ambulance-tracker-debug
```

as a GitHub Actions artifact.

### Deployment configuration lane

Every file matching:

```text
scripts/*.sh
```

passed `bash -n` syntax validation.

Both Compose configurations rendered successfully:

```bash
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml config
docker compose -f docker-compose.yml -f docker-compose.local.yml config
```

---

## 2. End-to-end backend integration test

`TestTrackerToDispatcherEndToEnd` exercises the core data path with the real PostGIS-backed store and an HTTP/WebSocket test server.

Validated sequence:

1. migrate the real test database schema;
2. bootstrap a dispatcher and tracker device;
3. authenticate the dispatcher;
4. authenticate the tracker and obtain a short-lived bearer token;
5. open dispatcher WebSocket;
6. open tracker WebSocket;
7. observe `presence connected=true` on the dispatcher channel;
8. send a location event from the tracker;
9. receive a non-duplicate ACK for the tracker sequence;
10. receive the same vehicle/location over the dispatcher WebSocket;
11. read the persisted fleet snapshot through `GET /api/v1/vehicles`;
12. read the persisted location through the history endpoint;
13. resend the same immutable location identity;
14. verify the server ACKs it as a duplicate rather than inserting it again.

**Result:** PASS through the full CI suite.

This validates the primary path:

```text
tracker auth
  -> tracker WebSocket
  -> PostgreSQL/PostGIS persistence
  -> ACK
  -> dispatcher WebSocket
  -> fleet/history reads
```

and explicitly validates duplicate idempotency.

---

## 3. Multiple-tracker and playback-isolation integration test

`TestMultipleTrackersAndPlaybackIsolation` validates two tracker devices connected at the same time.

Test setup:

- vehicle `AMB-MULTI-A`;
- vehicle `AMB-MULTI-B`;
- independent device credentials;
- one dispatcher WebSocket;
- two tracker WebSockets.

Validated behavior:

1. both trackers authenticate independently;
2. both tracker WebSockets connect;
3. dispatcher receives distinct connected-presence events;
4. both vehicles send telemetry and receive ACKs;
5. dispatcher receives live location events tagged with the correct vehicle;
6. fleet snapshot contains the latest independent state for both vehicles.

### Deliberate route-isolation test

Vehicle A is given two separate tracking sessions in the same requested history window:

- an older session with coordinates in San Francisco (`37.7749, -122.4194`);
- a newer session with coordinates in Saint Kitts (`17.3029, -62.7178`).

The history API is then queried for vehicle A.

Expected behavior:

- only the most recent tracking session is returned;
- the older San Francisco test point must not be joined to the real/current route.

Observed automated assertion:

- history contains only the new session;
- the returned session ID matches the newer tracker session;
- the returned location matches the newer Saint Kitts fix.

**Result:** PASS through the full CI suite.

This specifically guards against false route lines caused by simulator data, app restarts, previous device sessions, or independent tracker runs.

---

## 4. Dispatcher credential-rotation integration test

`TestDispatcherPasswordRotationRevokesSessions` validates administrative password rotation against the real database schema.

Validated behavior:

1. authenticate with the original dispatcher password;
2. create an active dispatcher session;
3. rotate the password through the store administrative operation;
4. verify the original password no longer authenticates;
5. verify the replacement password authenticates;
6. verify the pre-rotation dispatcher session has been revoked.

**Result:** PASS through the full CI suite.

This provides automated evidence for the password-rotation behavior used by `scripts/rotate-dispatcher-password.sh` and `cmd/adminctl`.

---

## 5. Device-session rotation behavior

The production-hardening device-key rotation helper updates the stored device-key hash and revokes active device sessions for the affected device in the same database operation path used by the operational script.

The script additionally:

- requires a replacement of at least 32 characters;
- does not echo the secret;
- hashes it before database storage;
- refuses to report success unless exactly one active device is changed.

CI validates script syntax and the Compose environment in which it runs. A production rotation still requires a controlled field execution because the corresponding physical phone must be updated with the new secret.

---

## 6. First real-device functional validation

The first physical Android-device test established the critical end-to-end product path outside CI.

Observed working behavior during development:

| Capability | Result |
| --- | --- |
| Android app installation/start | PASS |
| GNSS acquisition on real phone | PASS |
| Tracker authentication to deployed server | PASS |
| Live WebSocket telemetry through Cloudflare/Caddy | PASS |
| PostgreSQL/PostGIS location persistence | PASS |
| Dispatcher live update from phone telemetry | PASS |
| Android local map/position display | PASS |
| Loss-of-connectivity buffering and later replay | PASS |
| Server route playback from persisted telemetry | PASS |

Two issues were exposed by real-device usage and then corrected in follow-up code:

### Mobile map interaction

Initial behavior made the Android map difficult to pan/zoom because:

- the map was nested in a scrolling parent;
- every GPS fix recentered the camera.

The fix now:

- prevents the parent scroll view from stealing map gestures while the map is touched;
- leaves MapLibre pan/zoom enabled;
- switches out of follow mode when the user explores the map;
- provides a **Center** control to resume following without forcing a new zoom level.

### Stationary GPS route noise

Initial physical testing also exposed normal GNSS wander while the phone was stationary.

The tracker was hardened to:

- reject poor reported accuracy over 50 m;
- prefer GPS fixes over network-provider fixes when GNSS is available;
- reject implausible one-off teleports;
- suppress low-speed stationary wander inside an accuracy-derived radius;
- emit a stable-coordinate heartbeat every 15 seconds while stationary.

The server history query was also changed to isolate playback to the latest tracker session.

These fixes are represented in the automated tests and current implementation, but a second extended drive should still be used to quantify real-world behavior over time.

---

## 7. What this report proves

The available evidence supports the following statements:

- the project builds successfully across backend, web, and Android lanes;
- the Go code passes formatting, vet, unit/integration tests, and compilation;
- the dispatcher passes strict TypeScript checking and production bundling;
- the Android debug APK builds in CI;
- deployment scripts parse and both Compose topologies render;
- one and two tracker WebSocket flows have automated end-to-end coverage;
- location persistence and realtime dispatcher broadcast work together;
- duplicate telemetry is idempotent;
- latest-session playback isolation is tested;
- dispatcher password rotation invalidates prior sessions;
- the core tracker/server/dispatcher path has succeeded on a real Android phone.

## 8. What this report does **not** prove

The following have not yet been recorded as completed production acceptance tests and must remain open:

| Required field test | Status |
| --- | --- |
| Cellular-only drive of at least 20 minutes | NOT YET RECORDED |
| Intentional full data outage while moving, followed by replay validation | PARTIALLY PROVEN; formal field run still required |
| Screen locked for at least 20 minutes while driving | NOT YET RECORDED |
| Battery-saver drive test | NOT YET RECORDED |
| Force-stop/reboot/new-session field test | NOT YET RECORDED |
| Two physical phones tracking simultaneously | NOT YET RECORDED |
| Poor-GPS/covered-location field test | NOT YET RECORDED |
| Four-hour continuous run | NOT YET RECORDED |
| Backup creation plus restore drill on deployed infrastructure | NOT YET RECORDED |
| Production signed APK update-in-place test | NOT YET RECORDED |
| MDM-managed deployment test | NOT YET RECORDED |

The authoritative release gate remains [PRODUCTION_CHECKLIST.md](PRODUCTION_CHECKLIST.md).

## 9. Recommended next validation sequence

1. Merge/deploy the production-hardening work.
2. Take a real database backup and run `verify-backup.sh` against it.
3. Build/install a production-signed APK using the stable signing workflow.
4. Run a 20-minute cellular-only drive with screen locked for part of the route.
5. During the drive, intentionally remove data connectivity for a defined period and verify replay after reconnect.
6. Repeat with battery saver enabled.
7. Run two physical tracker phones concurrently.
8. Complete a four-hour soak run and record battery use, queue behavior, reconnects, map freshness, database growth, and any GPS anomalies.

Record exact phone model, Android version, application version/commit, test start/end times, network conditions, and pass/fail evidence for each field test.
