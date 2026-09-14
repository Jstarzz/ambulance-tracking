# System Requirements

This document defines the supported runtime envelope for the current Android tracker, dispatcher web application, and server stack. It separates hard software requirements from recommendations that improve operational reliability.

## Android ambulance tracker

### Minimum software

| Requirement | Minimum | Recommended |
| --- | --- | --- |
| Android | Android 9 / API 28 | Android 12 or newer |
| Location | Precise location permission | Precise location with GNSS enabled |
| Network | Intermittent IP connectivity is supported | Cellular data plus Wi-Fi where available |
| Notifications | Required by modern Android versions for the visible foreground tracking notification | Enabled |
| Google Play Services | Not required | Not required |

The tracker uses the Android platform `LocationManager`, Android Keystore-backed configuration storage, an app-private SQLite replay queue, and a foreground location service. It does not depend on Google Play Services for GNSS.

### Hardware

A tracker phone should have:

- functioning GNSS/GPS;
- a cellular radio/SIM if live tracking is required away from Wi-Fi;
- enough battery or vehicle power for the intended shift;
- a stable vehicle mount if motion/crash-sensing features are later enabled operationally; and
- a device model whose vendor firmware does not aggressively terminate foreground services.

No specific CPU or RAM floor beyond Android 9 compatibility has been measured. The tracker is deliberately small; GNSS quality, power policy, and network availability matter more than phone compute performance.

### Background and battery behavior

Tracking runs as an Android **foreground location service** with an ongoing notification. Closing the activity does not intentionally stop tracking.

Android and OEM power managers can still affect long-running work. The tracker therefore exposes the phone's battery-optimization state in the operational sheet and links directly to battery settings. For a dedicated ambulance tracker, configure the application as **Unrestricted / Not optimized** where the device vendor provides that option.

Do not interpret this as a guarantee that every Android vendor behaves identically in battery saver. Before operational deployment, test the exact phone model with:

1. screen locked for at least 30 minutes;
2. app activity swiped away while tracking remains active;
3. standard Battery Saver enabled;
4. loss and restoration of cellular/Wi-Fi connectivity;
5. device idle/doze conditions; and
6. a multi-hour drive while connected to vehicle power.

The acceptance criterion is that the foreground service continues acquiring locations or, if connectivity is unavailable, the local queue grows and later replays without route gaps caused by application shutdown.

## Dispatcher web application

The dispatcher requires a modern browser with:

- JavaScript enabled;
- WebSocket support;
- WebGL support for MapLibre;
- cookies enabled for the authenticated dispatcher session; and
- HTTPS access to the public deployment in production.

Current versions of Chromium-based browsers, Firefox, and Safari are appropriate targets. Desktop or tablet use is recommended for dispatch because the interface includes a fleet rail, map, unit inspector, route planning, and playback controls.

The dispatcher does not require installation. It is served as a static React application and can run on Windows, macOS, Linux, ChromeOS, Android tablets, and iPadOS/Safari as a web client.

## Server

### Software

Required:

- Linux host or VM capable of running Docker;
- Docker Engine;
- Docker Compose;
- PostgreSQL 17 with PostGIS 3.5 through the supplied container image; and
- outbound HTTPS access for map tiles and any configured routing provider.

The supplied production topology also uses Caddy and, when selected, Cloudflare Tunnel.

### Validated baseline

The existing deployment has been functionally validated on a 4 vCPU / 4 GiB RAM VM with a 50 GiB virtual disk. This is a **validated prototype baseline, not a measured capacity ceiling**. See `DEPLOYMENT_VALIDATION.md` before assigning a fleet-size or request-rate SLO.

For a fresh deployment, 4 vCPU, 4 GiB RAM, and 20+ GiB of available storage is a practical starting allocation for the current single-node stack. Storage should be monitored as telemetry retention grows.

### Storage growth

Location storage scales approximately with:

```text
stored fixes = active vehicles × fixes per second × active tracking seconds
```

At the current tracker cadence, moving vehicles can produce roughly one accepted fix per second. Stationary dead-band/heartbeat behavior lowers that rate while stopped. Actual database bytes per event should be measured from the deployed PostgreSQL instance before setting retention limits; this document intentionally does not invent a per-row byte estimate.

## Network

### Tracker

The tracker is designed for unreliable mobile networks:

- live fixes use an authenticated WebSocket when connected;
- every accepted fix is queued locally before transmission;
- ACKed live fixes are removed from the local queue;
- disconnected fixes remain on the phone; and
- the queue replays after reconnect.

GNSS itself does not require an Internet connection. The server simply cannot receive the position in real time while the phone has no usable communications path.

### Server ingress

The production Compose topology does not need to publish PostgreSQL or the Go API directly to the public Internet. With Cloudflare Tunnel, public HTTPS/WSS is carried through an outbound tunnel to Caddy.

### External map/routing services

Map tiles currently use OpenFreeMap. Route planning supports an optional OSRM-compatible service configured with:

```text
ROUTER_URL=https://your-router.example
ETA_FALLBACK_KPH=35
```

When `ROUTER_URL` is blank or unavailable, the dispatcher returns a clearly-labelled **approximate** ETA based on distance and live speed. That fallback is useful for continuity but is not road pathfinding.

For production road routing, use a controlled/self-hosted router or a provider with availability and usage terms appropriate to the deployment. Do not build operational dependence on a public demonstration routing endpoint.

## Time synchronization

Phones and servers should use automatic network time. Tracker observations are accepted only within the API's timestamp validity window, and route/history ordering assumes sane device clocks.

## Operational requirements

Beyond compute requirements, a real deployment needs:

- stable Android application signing;
- controlled tracker enrollment and device revocation;
- server/database backups and restore testing;
- credential rotation;
- host patching;
- monitoring/alerting;
- an Android device-management policy for dedicated tracker phones; and
- a documented process for a lost, replaced, or damaged tracker device.

These are operational controls, not optional performance tuning.
