# Cloudflare Tunnel deployment

This is the preferred production ingress for the prototype when the on-island server has no public IPv4 address.

## Why Tunnel

`cloudflared` opens outbound-only connections from the server to Cloudflare. The origin does not need a public IP and does not need inbound TCP 80/443 exposed. The public hostname resolves to Cloudflare, and Cloudflare forwards HTTP/WebSocket traffic through the tunnel to the private Docker network.

Traffic path:

```text
Android tracker / dispatcher browser
        |
        | HTTPS / WSS
        v
Cloudflare edge
        |
        | outbound tunnel connection
        v
cloudflared -> caddy:80 -> web / Go API -> PostGIS
```

## Cloudflare dashboard

1. Put the intended domain/zone in Cloudflare.
2. Go to **Networking -> Tunnels** and create a remotely-managed tunnel named `ambulance-tracking`.
3. Add a **Published application** route:
   - Hostname: for example `tracking.example.kn`
   - Service URL: `http://caddy:80`
4. Under **Network**, make sure **WebSockets** are enabled.
5. Do not cache `/api/*` or `/healthz`. The dashboard assets may be cached normally; API and WebSocket traffic must not be cached.
6. Copy the tunnel token from the tunnel's **Add a replica** flow. Do not commit it.

Do not place a Cloudflare Access challenge in front of the entire hostname unless the native tracker paths are explicitly excluded or the app is updated to authenticate to Access. The tracker already uses per-device credentials and short-lived application sessions.

## Server setup

The server only needs outbound Internet access. If egress is restricted, allow Cloudflare Tunnel connectivity on port 7844 (UDP and/or TCP, depending on negotiated transport) plus normal DNS/HTTPS access for image/package pulls.

```bash
git clone https://github.com/Jstarzz/ambulance-tracking.git
cd ambulance-tracking
git checkout dev
cp .env.cloudflare.example .env
nano .env
```

Replace every placeholder. The `PUBLIC_ORIGIN` value must be the hostname only, for example:

```text
PUBLIC_ORIGIN=tracking.example.kn
```

Then deploy:

```bash
chmod +x scripts/deploy-cloudflare.sh
./scripts/deploy-cloudflare.sh
```

Equivalent command without the helper:

```bash
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml up -d --build
```

No host port is published by the production Compose path. PostgreSQL remains on an internal Docker network, and Caddy is reachable only from containers on the `edge` network.

Verify from another network:

```bash
curl -fsS https://tracking.example.kn/healthz
```

Expected response:

```json
{"status":"ok"}
```

Then configure the Android app with:

```text
Server URL: https://tracking.example.kn
Vehicle: AMB-01
Device key: <DEMO_DEVICE_KEY from .env>
```

## Local development

The base Compose file intentionally publishes no origin ports. To expose the app on port 8080 locally:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build
```

Then browse to `http://localhost:8080` using the local `.env.example` settings.

## HIPAA boundary

The current tracker protocol deliberately excludes patient/clinical fields. If Cloudflare will create, receive, maintain, or transmit ePHI in a production deployment, the organization must have the required agreements and controls in place. Cloudflare states that it only enters HIPAA Business Associate Agreements with Enterprise customers. Do not introduce ePHI through a self-serve Cloudflare deployment and label it HIPAA compliant.

See `docs/HIPAA.md` for the rest of the deployment/organizational requirements.
