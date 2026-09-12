# User Guide

## 1. Audience

This guide is for two operational roles:

- **Dispatcher** — uses the browser dashboard to monitor provisioned ambulances and review recent route history.
- **Tracker operator / installer** — provisions and starts the Android tracker running in an ambulance.

Administrative server operations such as backups, credential rotation, and deployment are documented in [OPERATIONS.md](OPERATIONS.md).

---

# Dispatcher guide

## 2. Sign in

1. Open the configured tracking hostname in a modern browser.
2. Enter the dispatcher username and password.
3. Submit the login form.
4. After authentication, the application loads the fleet snapshot and opens the dispatcher WebSocket.

If the session expires or is revoked, authenticated API calls and the realtime connection stop working and the operator must sign in again.

## 3. Fleet list

The left fleet panel shows provisioned vehicles and their latest telemetry state.

The UI derives freshness from the tracker connection and last location timestamp:

| State | Meaning |
| --- | --- |
| `LIVE` | Tracker is currently connected, or the latest persisted fix is younger than 5 seconds |
| `DELAYED` | Latest fix is 5–30 seconds old |
| `STALE` | Latest fix is 30–120 seconds old |
| `OFFLINE` | Latest fix is older than 120 seconds |
| `NO DATA` | No persisted location exists for the vehicle |

A tracker may be physically operating while the dispatcher shows stale/offline if its Internet path is unavailable. The Android app is designed to buffer location records locally and replay them when connectivity returns.

## 4. Fleet map

The central map displays one marker per vehicle with location data.

- Select a vehicle from the fleet list or its map marker to inspect it.
- Pan and zoom normally with the MapLibre controls.
- The map uses OpenFreeMap-hosted map data/styles in the current implementation.

Map availability and telemetry availability are separate concerns. A tile-service outage can make the basemap unavailable without stopping the tracker/API/database path.

## 5. Selected vehicle telemetry

For the selected vehicle, the dispatcher can inspect available telemetry such as:

- coordinates;
- current or latest speed;
- heading/bearing;
- reported GPS accuracy;
- battery percentage;
- network type;
- last update time;
- connection/freshness state.

Missing optional sensor values are shown as unavailable rather than synthesized.

## 6. Route playback

The dispatcher can request recent route history for the selected vehicle using the available 1-hour, 6-hour, or 24-hour windows.

The playback UI:

- draws the returned route;
- fits the map to the trace;
- shows playback progress;
- allows play/pause and scrubbing;
- shows the selected playback timestamp;
- allows returning to the live view.

Important behavior: playback only uses the **most recent tracking session** in the requested window. If the phone app restarted, a simulator was run earlier, or a previous test session exists, the server does not connect those independent sessions into one false route.

## 7. Sign out

Use the dashboard sign-out control. Logout revokes the current server-side session and expires the browser cookie.

---

# Android tracker guide

## 8. Requirements

The current Android application requires:

- Android API level 28 or later;
- Internet access for live server communication (cellular or Wi-Fi);
- GNSS/location services;
- precise location permission;
- an HTTPS server URL;
- a provisioned vehicle code and device key.

GNSS itself does not require cellular data. If the phone loses Internet connectivity, the app can continue accepting GPS fixes and buffering them locally.

## 9. First-time provisioning

On the phone:

1. Install the approved APK.
2. Open **EMS Tracker**.
3. Enter the server URL, for example:

   ```text
   https://tracking.example.kn
   ```

4. Enter the assigned vehicle code, for example `AMB-01`.
5. Enter the provisioned device key.
6. Tap **Start tracking**.
7. Grant precise location permission when Android prompts.
8. Allow the foreground-service notification as required by the Android version/device policy.

The app requires `https://` before it will start production tracking.

The saved tracker configuration is encrypted using Android Keystore-backed storage. Once saved, the configuration fields are collapsed by default; use **Edit configuration** when the device must be reprovisioned.

## 10. Normal tracking behavior

After startup, the status moves through states such as:

- `Starting`;
- `Acquiring GPS`;
- `Live`;
- `Offline — buffering`;
- `Server unavailable — buffering`;
- `Disconnected — buffering`.

While tracking is active, the screen displays current telemetry including:

- map position and local trail;
- speed;
- GPS accuracy;
- coordinates;
- network type;
- buffered-record count;
- battery percentage;
- server/connection state.

Android also displays an ongoing foreground-service notification while tracking is active.

## 11. Using the mobile map

The Android map follows the first acquired location automatically.

- Drag or pinch the map to inspect another area.
- Touching the map disables automatic camera following so GPS updates do not fight manual navigation.
- Tap **Center** to resume following the current location without forcing a fixed zoom level.
- The local trail is bounded to the most recent 300 displayed points.

The map is an operator aid; transmission/persistence does not depend on the user keeping the map centered.

## 12. Offline behavior

The tracker uses enqueue-before-send semantics.

For each accepted fix:

1. the app writes the payload to its private SQLite queue;
2. it attempts WebSocket transmission if connected;
3. the server persists the event and returns an ACK;
4. only after the ACK does the tracker remove that event from the queue.

If all Internet connectivity disappears:

- GPS acquisition can continue;
- accepted fixes remain in SQLite;
- the buffer count grows;
- the app reconnects automatically;
- after reconnect, queued fixes are sent through the replay endpoint.

Pending local records older than seven days are trimmed when the tracking service starts.

## 13. GPS filtering behavior

The tracker deliberately rejects or suppresses some fixes to prevent obvious route corruption:

- reported accuracy worse than 50 m is rejected;
- stationary GPS wander inside an accuracy-derived radius is suppressed;
- stationary state still emits a low-rate heartbeat so telemetry does not become permanently stale;
- impossible one-off teleports are rejected unless the device-reported speed supports the movement;
- GNSS is preferred to network-provider positioning when GPS is enabled.

Because of this filtering, the raw Android location callback count and the number of server telemetry events are not expected to match one-for-one.

## 14. Stop tracking

Tap **Stop tracking** in the app. The foreground service stops and the app status changes to `Stopped`.

Do not force-stop or uninstall the app during normal service unless instructed. Uninstalling clears local application data, including saved tracker configuration and any pending offline queue rows.

## 15. Reprovisioning or rotating a device key

When operations rotates a device key:

1. stop tracking on the phone;
2. open **Edit configuration**;
3. replace the device key with the newly provisioned value;
4. save/start tracking again;
5. verify the app reaches `Live` and appears connected on the dispatcher.

A rotated server-side device key immediately revokes existing device sessions in the production-hardening tooling, so the phone must be updated before it can authenticate again.

---

# Troubleshooting

## 16. Tracker says “Device authentication failed”

Check:

- vehicle code exactly matches the provisioned code;
- device key is current;
- server URL is the correct HTTPS origin;
- the device was not recently rotated/revoked without updating the phone.

Do not paste production device keys into tickets, chat, screenshots, or logs.

## 17. Tracker says “Server unavailable — buffering”

Check Internet reachability first. If GNSS is still available, the queue should continue to collect accepted location events. Do not clear application data while troubleshooting, because that discards the local queue.

## 18. Dispatcher shows stale/offline but phone says Live

Check:

- whether the phone is pointed at the same environment/hostname the dispatcher is using;
- Cloudflare Tunnel health;
- `/healthz`;
- API and Caddy container health;
- whether the dispatcher browser session/WebSocket is still authenticated.

## 19. Map is blank but telemetry is updating

The OpenFreeMap basemap may be unavailable or blocked. Verify that coordinates, timestamps, and fleet telemetry continue updating before treating the issue as a tracking transport failure.

## 20. Route looks wrong after an app restart

Use the playback endpoint/UI again. The server intentionally scopes playback to the latest tracker session. Separate sessions are not joined. If a route is still incorrect within a single session, record the time window and inspect GPS accuracy/speed telemetry before changing server history data.
