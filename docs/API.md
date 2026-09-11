# API and Realtime Protocol Reference

## 1. Scope

The service exposes a small versioned HTTP API plus two WebSocket endpoints. The browser and Android tracker use the same origin in production through Caddy and Cloudflare Tunnel.

Example production base URL:

```text
https://tracking.example.kn
```

All JSON examples use UTC RFC 3339 timestamps.

A machine-readable REST contract is also available in [`openapi.yaml`](openapi.yaml). WebSocket message contracts are documented here because OpenAPI does not fully describe the bidirectional realtime protocol.

## 2. Authentication models

### Dispatcher session

Dispatchers authenticate with username/password at `POST /api/v1/auth/login`.

On success the API:

- creates an opaque random user-session token;
- stores only the hash of that token;
- sets an `ambulance_session` `HttpOnly` cookie;
- returns a CSRF token in the JSON response;
- stores only the CSRF-token hash.

Production cookies are configured with `Secure` and `SameSite=Strict`.

Dispatcher REST reads and the dispatcher WebSocket require the session cookie. `POST /api/v1/auth/logout` additionally requires the `X-CSRF-Token` header.

### Device session

A tracker authenticates with its provisioned vehicle code and long-lived device key at `POST /api/v1/device/session`.

On success the API returns a short-lived bearer token. Tracker HTTP/WebSocket requests send:

```http
Authorization: Bearer <access-token>
```

Server-side device keys and bearer tokens are stored as hashes.

## 3. Common JSON error shape

Most HTTP errors use:

```json
{
  "error": "human-readable error"
}
```

Common status codes:

| Status | Meaning |
| --- | --- |
| `200` | Successful request |
| `204` | Successful request with no response body |
| `400` | Invalid JSON, invalid parameter, invalid location batch, or validation failure |
| `401` | Missing/invalid/expired authentication |
| `403` | CSRF validation failed |
| `429` | Authentication rate limit exceeded |
| `500` | Internal persistence/query/session creation failure |
| `503` | Health check failed because the database is unavailable |

## 4. Health

### `GET /healthz`

Checks the application database connection.

Authentication: none.

Success:

```http
HTTP/1.1 200 OK
Content-Type: application/json
```

```json
{"status":"ok"}
```

Database failure:

```http
HTTP/1.1 503 Service Unavailable
```

```json
{"status":"unhealthy"}
```

The check has a one-second database timeout.

---

## 5. Dispatcher authentication

### `POST /api/v1/auth/login`

Authentication: none.

Request:

```json
{
  "username": "dispatcher",
  "password": "correct horse battery staple"
}
```

Success:

```json
{
  "user": {
    "id": "c82f6552-9ad4-4aaf-95a1-5adce34a84fe",
    "username": "dispatcher",
    "role": "admin"
  },
  "csrf_token": "<opaque-token>",
  "expires_at": "2026-09-11T22:00:00Z"
}
```

The response also sets:

```text
ambulance_session=<opaque-session-token>
```

Rate limit: 10 attempts per source-IP fixed window of one minute.

Invalid credentials return `401`. Rate-limited requests return `429` and a `Retry-After` header.

### `POST /api/v1/auth/logout`

Authentication: dispatcher session cookie.

Required header:

```http
X-CSRF-Token: <csrf-token-returned-at-login>
```

Success:

```http
HTTP/1.1 204 No Content
```

The database session is revoked and the browser cookie is expired.

---

## 6. Fleet snapshot

### `GET /api/v1/vehicles`

Authentication: dispatcher session cookie.

Returns every provisioned vehicle plus its latest persisted location, if one exists.

Example:

```json
{
  "vehicles": [
    {
      "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
      "vehicle_code": "AMB-01",
      "label": "AMB-01",
      "status": "AVAILABLE",
      "location": {
        "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
        "vehicle_code": "AMB-01",
        "tracking_session_id": "2c1e9641-ec9b-487a-a895-8f28bda38f30",
        "sequence_number": 42,
        "recorded_at": "2026-09-11T21:15:22Z",
        "latitude": 17.3029,
        "longitude": -62.7178,
        "accuracy_m": 5.4,
        "speed_mps": 8.1,
        "bearing_deg": 95.0,
        "battery_pct": 82,
        "network_type": "CELLULAR"
      }
    }
  ],
  "server_time": "2026-09-11T21:15:23Z"
}
```

The endpoint records an audit event containing the number of returned vehicles.

---

## 7. Historical route playback

### `GET /api/v1/vehicles/{vehicleID}/history?hours=N`

Authentication: dispatcher session cookie.

Parameters:

| Parameter | Location | Required | Rules |
| --- | --- | --- | --- |
| `vehicleID` | path | yes | Vehicle UUID string |
| `hours` | query | no | Integer `1..24`; defaults to `1` |

Example:

```http
GET /api/v1/vehicles/2f324a56-f4d2-43ba-8ee3-51f6056a736f/history?hours=6
```

Response:

```json
{
  "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
  "from": "2026-09-11T15:20:00Z",
  "to": "2026-09-11T21:20:00Z",
  "locations": [
    {
      "device_id": "ab91e810-8ad8-466e-9f2a-f0a16e33855b",
      "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
      "vehicle_code": "AMB-01",
      "tracking_session_id": "2c1e9641-ec9b-487a-a895-8f28bda38f30",
      "sequence_number": 1,
      "recorded_at": "2026-09-11T21:00:00Z",
      "latitude": 17.3029,
      "longitude": -62.7178
    }
  ]
}
```

Important semantics:

- only the **most recent `tracking_session_id`** found in the requested window is returned;
- separate app runs/simulations are never joined into one route by this endpoint;
- the database query returns chronological points;
- large traces are deterministically down-sampled to at most 5,000 points while preserving the first and last sample.

Invalid `hours` returns `400`.

---

## 8. Device authentication

### `POST /api/v1/device/session`

Authentication: long-lived provisioned device credential.

Request:

```json
{
  "vehicle_code": "AMB-01",
  "device_key": "<device-secret>"
}
```

Success:

```json
{
  "access_token": "<short-lived-bearer-token>",
  "expires_at": "2026-09-11T21:30:00Z",
  "device": {
    "id": "ab91e810-8ad8-466e-9f2a-f0a16e33855b",
    "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
    "vehicle_code": "AMB-01",
    "name": "AMB-01 tracker"
  }
}
```

Rate limit: 20 attempts per source-IP fixed window of one minute.

Invalid credentials return `401`.

---

## 9. Tracker WebSocket

### `GET /api/v1/tracker/ws`

Upgrade: WebSocket.

Authentication:

```http
Authorization: Bearer <device-session-token>
```

The server validates the bearer session before accepting the WebSocket and revalidates the session approximately every 30 seconds while messages are being received. If the session is no longer valid, the server closes the connection with a policy-violation status.

Per-message read limit: 8 KiB.

### 9.1 Location message: tracker -> server

```json
{
  "tracking_session_id": "2c1e9641-ec9b-487a-a895-8f28bda38f30",
  "sequence_number": 42,
  "recorded_at": "2026-09-11T21:15:22Z",
  "latitude": 17.3029,
  "longitude": -62.7178,
  "accuracy_m": 5.4,
  "speed_mps": 8.1,
  "bearing_deg": 95.0,
  "altitude_m": 21.3,
  "battery_pct": 82,
  "network_type": "CELLULAR"
}
```

Required:

- `tracking_session_id` — non-empty UUID-compatible identifier for a tracker run;
- `sequence_number` — integer >= 0;
- `recorded_at` — non-zero timestamp;
- `latitude` — `-90..90`;
- `longitude` — `-180..180`.

Timestamp acceptance window:

- no more than seven days old;
- no more than five minutes in the future.

Database constraints also reject negative accuracy/speed, invalid bearing, and battery values outside `0..100`.

### 9.2 ACK: server -> tracker

```json
{
  "type": "ack",
  "sequence_number": 42,
  "duplicate": false
}
```

A retransmitted event with the same device/session/sequence identity receives:

```json
{
  "type": "ack",
  "sequence_number": 42,
  "duplicate": true
}
```

Both ACK forms mean the server already has the immutable event identity and the tracker may remove its matching local queue row.

### 9.3 NACK: server -> tracker

Validation failure:

```json
{
  "type": "nack",
  "sequence_number": 42,
  "error": "recorded_at outside accepted window"
}
```

Persistence failure:

```json
{
  "type": "nack",
  "sequence_number": 42,
  "error": "persist failed"
}
```

### 9.4 Presence side effect

When a tracker WebSocket connects, the server broadcasts a dispatcher event:

```json
{
  "type": "presence",
  "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
  "connected": true
}
```

On disconnect it broadcasts the same event with `connected: false`.

---

## 10. Offline replay

### `POST /api/v1/tracker/history`

Authentication: device bearer token.

Maximum HTTP request body: 512 KiB.

Batch size: `1..500` locations.

Request:

```json
{
  "locations": [
    {
      "tracking_session_id": "2c1e9641-ec9b-487a-a895-8f28bda38f30",
      "sequence_number": 40,
      "recorded_at": "2026-09-11T21:15:20Z",
      "latitude": 17.3028,
      "longitude": -62.7180
    },
    {
      "tracking_session_id": "2c1e9641-ec9b-487a-a895-8f28bda38f30",
      "sequence_number": 41,
      "recorded_at": "2026-09-11T21:15:21Z",
      "latitude": 17.3029,
      "longitude": -62.7179
    }
  ]
}
```

Response:

```json
{
  "accepted": 2,
  "duplicates": 0
}
```

Newly inserted replay rows are also broadcast to dispatchers as live `location` events with `replayed: true`.

Current implementation note: individual invalid rows in an otherwise valid batch are skipped rather than returning a per-row error structure. Tracker-generated queue rows are expected to have already passed client-side construction rules, but third-party device clients should not assume a `200` response means every arbitrary malformed row was stored.

---

## 11. Dispatcher WebSocket

### `GET /api/v1/dispatch/ws`

Upgrade: WebSocket.

Authentication: `ambulance_session` browser cookie.

The server periodically revalidates the dispatcher session and sends WebSocket pings. If the browser session is revoked or expires, the WebSocket closes.

### 11.1 Presence event

```json
{
  "type": "presence",
  "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
  "connected": true
}
```

### 11.2 Live location event

```json
{
  "type": "location",
  "location": {
    "device_id": "ab91e810-8ad8-466e-9f2a-f0a16e33855b",
    "vehicle_id": "2f324a56-f4d2-43ba-8ee3-51f6056a736f",
    "vehicle_code": "AMB-01",
    "tracking_session_id": "2c1e9641-ec9b-487a-a895-8f28bda38f30",
    "sequence_number": 42,
    "recorded_at": "2026-09-11T21:15:22Z",
    "latitude": 17.3029,
    "longitude": -62.7178,
    "accuracy_m": 5.4,
    "speed_mps": 8.1,
    "battery_pct": 82,
    "network_type": "CELLULAR"
  },
  "received_at": "2026-09-11T21:15:22.200Z"
}
```

Replayed telemetry adds:

```json
"replayed": true
```

## 12. Location schema

| Field | Type | Required from tracker | Notes |
| --- | --- | --- | --- |
| `device_id` | string | no | Added by server on dispatcher/history output |
| `vehicle_id` | string | no | Added by server on dispatcher/history output |
| `vehicle_code` | string | no | Added by server on dispatcher/history output |
| `tracking_session_id` | string | yes | Identifies one tracker run |
| `sequence_number` | integer | yes | Monotonic within a tracker session |
| `recorded_at` | timestamp | yes | Device observation time |
| `latitude` | number | yes | `-90..90` |
| `longitude` | number | yes | `-180..180` |
| `accuracy_m` | number | no | Non-negative |
| `speed_mps` | number | no | Non-negative |
| `bearing_deg` | number | no | `0 <= bearing < 360` |
| `altitude_m` | number | no | Optional altitude |
| `battery_pct` | integer | no | `0..100` |
| `network_type` | string | no | Current Android values include `CELLULAR`, `WIFI`, `ETHERNET`, `OTHER`, `NONE` |

## 13. Idempotency contract

The persistence key is:

```text
(device_id, tracking_session_id, sequence_number)
```

This is central to offline operation. A client may retry the same immutable event until it receives an ACK. It must **not** reuse the same identity for different coordinates or timestamps.

## 14. Audit behavior

The service writes audit records for major security/operational events, including dispatcher login/logout, dispatcher WebSocket connection, device session creation, tracker connection, fleet reads, route-history reads, and replay batches.

Audit records include actor/resource identifiers, source IP where available, outcome, timestamp, and operation-specific JSON metadata.
