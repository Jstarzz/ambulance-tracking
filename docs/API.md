# Ambulance Tracking API and Realtime Protocol

## Overview

The Ambulance Tracking service exposes a dispatcher HTTP/WebSocket API and a device HTTP/WebSocket API through:

```text
https://tracking.itsjosiahdavis.dev
```

The public edge is Cloudflare Tunnel. The origin does not require a public IP or inbound WAN port forwarding.

The system has two authentication domains:

- **Dispatcher users** authenticate with username/password and receive an HTTP-only session cookie plus a CSRF token.
- **Tracker devices** exchange a long-lived vehicle/device credential for a short-lived bearer session token.

Do not place dispatcher passwords, device keys, session cookies, CSRF tokens, or bearer tokens in source control or application logs.

---

## Health

### `GET /healthz`

Checks API/database health.

Success:

```http
HTTP/2 200
Content-Type: application/json
```

```json
{"status":"ok"}
```

Database failure returns `503` with:

```json
{"status":"unhealthy"}
```

---

# Dispatcher API

## Login

### `POST /api/v1/auth/login`

Request:

```json
{
  "username": "dispatcher",
  "password": "<password>"
}
```

Example:

```bash
curl -i -c cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"username":"dispatcher","password":"..."}' \
  https://tracking.itsjosiahdavis.dev/api/v1/auth/login
```

Success returns the user, a CSRF token, and session expiry. The server also sets the `ambulance_session` cookie.

```json
{
  "user": {
    "id": "...",
    "username": "dispatcher",
    "role": "admin"
  },
  "csrf_token": "...",
  "expires_at": "..."
}
```

Production cookies are `HttpOnly`, `Secure`, and `SameSite=Strict`.

Login attempts are rate-limited per observed source IP. Invalid credentials return `401`; excessive attempts return `429`.

## Logout

### `POST /api/v1/auth/logout`

Requires:

- valid `ambulance_session` cookie; and
- `X-CSRF-Token` header containing the token returned at login.

Example:

```bash
curl -i -b cookies.txt \
  -X POST \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  https://tracking.itsjosiahdavis.dev/api/v1/auth/logout
```

Success returns `204 No Content` and revokes the server-side session.

## Fleet snapshot

### `GET /api/v1/vehicles`

Requires an authenticated dispatcher session cookie.

Example:

```bash
curl -sS -b cookies.txt \
  https://tracking.itsjosiahdavis.dev/api/v1/vehicles
```

Response shape:

```json
{
  "vehicles": [
    {
      "vehicle_id": "...",
      "vehicle_code": "AMB-01",
      "label": "AMB-01",
      "status": "active",
      "location": {
        "tracking_session_id": "...",
        "sequence_number": 123,
        "recorded_at": "2026-09-11T21:00:00Z",
        "latitude": 17.302,
        "longitude": -62.717,
        "accuracy_m": 8.5,
        "speed_mps": 4.2,
        "bearing_deg": 95,
        "battery_pct": 76,
        "network_type": "cellular"
      }
    }
  ],
  "server_time": "..."
}
```

## Historical route playback

### `GET /api/v1/vehicles/{vehicleID}/history?hours=N`

Requires dispatcher authentication.

`hours` defaults to `1` and must be between `1` and `24`.

Example:

```bash
curl -sS -b cookies.txt \
  'https://tracking.itsjosiahdavis.dev/api/v1/vehicles/<vehicle-id>/history?hours=6'
```

The response contains chronological locations from the most recent tracking session in the requested window. This prevents independent app runs/restarts from being joined into one artificial route.

Large histories are deterministically down-sampled server-side to at most 5,000 points.

## Dispatcher realtime WebSocket

### `GET /api/v1/dispatch/ws`

Protocol:

```text
wss://tracking.itsjosiahdavis.dev/api/v1/dispatch/ws
```

Authentication uses the existing dispatcher session cookie. The server verifies the WebSocket `Origin` against the configured public origin and periodically revalidates the session.

The server sends events such as:

### Vehicle tracker presence

```json
{
  "type": "presence",
  "vehicle_id": "...",
  "connected": true
}
```

### Location update

```json
{
  "type": "location",
  "location": {
    "device_id": "...",
    "vehicle_id": "...",
    "vehicle_code": "AMB-01",
    "tracking_session_id": "...",
    "sequence_number": 123,
    "recorded_at": "2026-09-11T21:00:00Z",
    "latitude": 17.302,
    "longitude": -62.717
  },
  "received_at": "2026-09-11T21:00:01Z"
}
```

Replayed offline records may additionally include:

```json
{"replayed":true}
```

---

# Tracker Device API

## Obtain a device session

### `POST /api/v1/device/session`

The phone first exchanges its provisioned vehicle code and long-lived device key for a short-lived bearer token.

Request:

```json
{
  "vehicle_code": "AMB-01",
  "device_key": "<long-lived-device-key>"
}
```

Example:

```bash
curl -sS \
  -H 'Content-Type: application/json' \
  -d '{"vehicle_code":"AMB-01","device_key":"..."}' \
  https://tracking.itsjosiahdavis.dev/api/v1/device/session
```

Success:

```json
{
  "access_token": "...",
  "expires_at": "...",
  "device": {
    "id": "...",
    "vehicle_id": "...",
    "vehicle_code": "AMB-01",
    "name": "AMB-01 tracker"
  }
}
```

Invalid credentials return `401`. Device-session attempts are rate-limited per observed source IP.

The long-lived device key is stored server-side only as a SHA-256 digest and should be protected on the Android device using Android Keystore-backed storage.

## Live tracker WebSocket

### `GET /api/v1/tracker/ws`

Protocol:

```text
wss://tracking.itsjosiahdavis.dev/api/v1/tracker/ws
Authorization: Bearer <device-session-token>
```

The bearer session is checked before WebSocket acceptance and revalidated periodically while the connection remains open.

Each location message has this contract:

```json
{
  "tracking_session_id": "550e8400-e29b-41d4-a716-446655440000",
  "sequence_number": 123,
  "recorded_at": "2026-09-11T21:00:00Z",
  "latitude": 17.302,
  "longitude": -62.717,
  "accuracy_m": 8.5,
  "speed_mps": 4.2,
  "bearing_deg": 95,
  "altitude_m": 45.3,
  "battery_pct": 76,
  "network_type": "cellular"
}
```

Required validation includes:

- non-empty `tracking_session_id`;
- `sequence_number >= 0`;
- non-zero `recorded_at`;
- latitude between -90 and 90;
- longitude between -180 and 180; and
- timestamp no more than 5 minutes in the future or 7 days in the past.

On accepted persistence the server replies:

```json
{
  "type": "ack",
  "sequence_number": 123,
  "duplicate": false
}
```

An already-persisted `(device_id, tracking_session_id, sequence_number)` returns an ACK with:

```json
{"duplicate":true}
```

This idempotency is what allows safe replay from the phone's offline SQLite queue.

Invalid telemetry receives a NACK, for example:

```json
{
  "type": "nack",
  "sequence_number": 123,
  "error": "invalid coordinates"
}
```

The WebSocket message read limit is 8 KiB and WebSocket compression is disabled.

## Offline history replay

### `POST /api/v1/tracker/history`

Requires:

```http
Authorization: Bearer <device-session-token>
```

Body:

```json
{
  "locations": [
    {
      "tracking_session_id": "550e8400-e29b-41d4-a716-446655440000",
      "sequence_number": 120,
      "recorded_at": "2026-09-11T20:59:55Z",
      "latitude": 17.302,
      "longitude": -62.717
    }
  ]
}
```

The batch must contain between 1 and 500 records and the HTTP body is capped at 512 KiB.

Success:

```json
{
  "accepted": 1,
  "duplicates": 0
}
```

The phone should delete local queue entries only after the server confirms them.

---

## Operational error handling

Common status codes:

| Status | Meaning |
|---:|---|
| 200 | Successful request |
| 204 | Successful logout/no body |
| 400 | Invalid request or validation failure |
| 401 | Missing/invalid user or device authentication |
| 403 | CSRF validation failed |
| 429 | Authentication attempt rate limit reached |
| 500 | Server/database operation failed |
| 503 | Health/database dependency unavailable |

Tracker clients should reconnect with bounded exponential backoff after transient network/service failures and retain unsent telemetry locally until it is acknowledged.

## Data boundary

Tracker payloads contain vehicle/device telemetry. The current protocol intentionally has no patient, diagnosis, treatment, or other clinical fields.
