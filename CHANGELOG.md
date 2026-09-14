# Changelog

All notable changes to the ambulance tracking system are recorded here. This project is still pre-1.0; entries describe meaningful behavior and architecture changes rather than assigning semantic-version releases prematurely.

## Unreleased

### Added

- Dispatcher map-click route planning for a selected ambulance.
- `GET /api/v1/vehicles/{vehicleID}/route` dispatcher endpoint.
- Optional OSRM-compatible road routing through `ROUTER_URL`.
- Explicit approximate ETA fallback when a road router is unavailable.
- Route geometry, distance, ETA, routing method, source-position timestamp, and approximation status in the route API response.
- Bounded 20-second dispatcher route refresh while a route is active.
- Android `TrackerStateStore` for lightweight foreground-service UI snapshots.
- Android foreground-service status query used when reopening the activity.
- Collapsible/draggable Android operational bottom sheet.
- Android battery-optimization status and shortcut to system battery settings.
- `docs/SYSTEM_REQUIREMENTS.md`.
- `docs/TRACKER_LIFECYCLE_AND_ROUTING.md` with Mermaid lifecycle/routing diagrams.
- Unit coverage for approximate ETA fallback behavior.

### Fixed

- Reopening the Android activity no longer briefly presents **Start tracking** while the tracking foreground service is already active.
- Tracker state is restored immediately from the most recent service snapshot, then reconciled with the running service.
- Explicit user stop persists `Stopped` state before service teardown.

### Changed

- Dispatcher route planning is based on the newest persisted vehicle fix rather than transient browser-only state.
- Route-provider calls are decoupled from the 1 Hz telemetry stream; routing is requested only when a dispatcher creates/refreshes a route.

## 2026-09-14

### Added

- One-time tracker enrollment workflow from the dispatcher.
- Eight-character registration codes with short expiry and single-use semantics.
- Android enrollment flow that hides deployment URL and long-lived device credentials from normal users.
- Map-first Android tracker interface and dispatcher refinements inspired by conventional navigation/map applications without copying provider branding or assets.

### Changed

- Tracker provisioning moved from manual server URL / vehicle code / device key entry to dispatch-created registration codes.
- Android deployment origin is supplied by build configuration.

## 2026-09-13

### Added

- Production-hardening work including backup/restore tooling, health checks, container health configuration, bounded Docker logs, and credential-rotation helpers.
- Multi-ambulance integration coverage and session-isolation validation.
- Professional architecture, API, operations, security, deployment-validation, and acceptance documentation.

### Fixed

- Dispatcher history is scoped to the latest tracking session so simulated/provisioning locations cannot be connected to a later real drive.
- Android stationary GNSS drift filtering and impossible-jump rejection.
- GPS is preferred over network-provider fixes while GNSS is available.

## 2026-09-11

### Added

- Native Android/Kotlin ambulance tracker.
- Go API and realtime service.
- PostgreSQL 17/PostGIS telemetry persistence.
- React/TypeScript/MapLibre dispatcher dashboard.
- Tracker and dispatcher WebSockets.
- App-private SQLite offline telemetry queue with enqueue-before-send behavior.
- Idempotent telemetry identity using device, tracking session, and sequence number.
- Route-history playback with 1-, 6-, and 24-hour windows.
- Cloudflare Tunnel deployment path through Caddy.
- GitHub Actions CI for backend, web, Android, and deployment configuration.
- Android debug APK CI artifact.

### Validated

- End-to-end tracker authentication → WebSocket → validation → PostgreSQL → ACK → dispatcher broadcast.
- Physical Android tracking with real GNSS data.
- Offline collection and later replay after network restoration.

---

For commit-level detail, use the Git history and pull-request record. This changelog is intended to answer "what behavior changed?" rather than duplicate every formatting or documentation-only commit.
