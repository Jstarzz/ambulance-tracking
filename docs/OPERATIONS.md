# Operations Runbook

## 1. Scope

This runbook covers the supplied single-node deployment model: Docker Compose on one Linux host/VM, Cloudflare Tunnel for public ingress, Caddy as the private edge, a Go API service, static React web service, and PostgreSQL/PostGIS.

It is written for system operators. Application usage is in [USER_GUIDE.md](USER_GUIDE.md); implementation details are in [ARCHITECTURE.md](ARCHITECTURE.md).

## 2. Production topology

The production Compose stack does not publish PostgreSQL, API, Caddy, or web ports directly on the host. `cloudflared` establishes an outbound tunnel and forwards the configured hostname to:

```text
http://caddy:80
```

Required outbound connectivity includes Cloudflare Tunnel and the map provider used by clients.

## 3. Required configuration

Start from:

```bash
cp .env.cloudflare.example .env
```

Required values:

| Variable | Purpose | Production expectation |
| --- | --- | --- |
| `PUBLIC_ORIGIN` | Allowed browser/WebSocket origin and public hostname | Public DNS hostname only; no scheme, e.g. `tracking.example.kn` |
| `SECURE_COOKIES` | Marks dispatcher session cookie secure | `true` |
| `CLOUDFLARE_TUNNEL_TOKEN` | Remotely managed tunnel credential | Secret; never commit or print |
| `POSTGRES_PASSWORD` | PostgreSQL application password | Random, unique secret |
| `DATABASE_URL` | API PostgreSQL DSN | Must use the same DB password and internal `db` hostname |
| `HTTP_ADDR` | API listen address in container | `:8080` |
| `BOOTSTRAP_ADMIN_USERNAME` | Initial dispatcher/admin username | Set before first bootstrap |
| `BOOTSTRAP_ADMIN_PASSWORD` | Initial dispatcher/admin password | Strong temporary bootstrap secret; rotate after deployment |
| `DEMO_VEHICLE_CODE` | Initial provisioned vehicle code | Replace with operational code as appropriate |
| `DEMO_DEVICE_KEY` | Initial tracker credential | Random device secret; do not reuse between production systems |

File permissions should restrict `.env` to the deployment operator/service account.

## 4. Initial deployment

Prerequisites on the host:

- Docker Engine;
- Docker Compose plugin;
- Git;
- `curl`;
- outbound Internet connectivity;
- sufficient persistent disk for PostgreSQL and backups.

Deploy:

```bash
git checkout feat/production-hardening
git pull
cp .env.cloudflare.example .env
# edit .env with real values
chmod 600 .env
chmod +x scripts/*.sh
./scripts/deploy-cloudflare.sh
```

The deploy helper validates required environment values and refuses obvious placeholders before bringing up the stack.

## 5. Cloudflare setup

In the Cloudflare dashboard:

1. Create or select a remotely managed tunnel.
2. Add a published application/hostname.
3. Set the service URL to `http://caddy:80`.
4. Ensure WebSockets are supported on the route.
5. Do not place an interactive Cloudflare Access challenge in front of the whole hostname; the Android tracker must reach API/WebSocket endpoints directly.
6. Do not cache `/api/*` or `/healthz`.

See [CLOUDFLARE.md](CLOUDFLARE.md) for the tunnel-specific checklist.

## 6. Health verification

Run:

```bash
./scripts/healthcheck.sh
```

The script checks:

- `db` container running;
- `api` container running;
- `web` container running;
- `caddy` container running;
- `cloudflared` container running;
- PostgreSQL readiness;
- public `https://$PUBLIC_ORIGIN/healthz` response.

Expected public response:

```json
{"status":"ok"}
```

The container stack also has health checks for PostgreSQL, web, and Caddy. Cloudflare Tunnel is configured to wait for a healthy Caddy service.

## 7. Routine status checks

```bash
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml ps
./scripts/healthcheck.sh
```

For recent service logs:

```bash
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml logs --tail=200 api
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml logs --tail=200 caddy
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml logs --tail=200 cloudflared
```

The Compose stack bounds Docker JSON logs to 10 MiB per file with five files per service. This prevents unbounded local log growth, but it is **not** centralized or immutable log retention.

## 8. Updating the deployment

Before updating:

1. verify the current health check passes;
2. take a database backup;
3. record the currently deployed Git commit;
4. review release/PR notes for database or configuration changes.

Then:

```bash
git fetch --all
git checkout <approved-release-ref>
git pull --ff-only
./scripts/deploy-cloudflare.sh
./scripts/healthcheck.sh
```

The server schema migration is designed to be idempotent through `CREATE ... IF NOT EXISTS` statements. This is appropriate for the current schema evolution stage, but future destructive/transformative schema changes should move to explicit versioned migrations before production use.

## 9. Rollback

Application rollback:

```bash
git checkout <previous-known-good-commit>
./scripts/deploy-cloudflare.sh
./scripts/healthcheck.sh
```

Do not automatically roll back the database to an older backup just because application code is rolled back. Database restore is a separate, high-impact recovery action and should be performed only when the data state itself is known to be damaged or incompatible.

## 10. Backups

Create a custom-format PostgreSQL backup:

```bash
BACKUP_DIR=/path/on/separate/storage ./scripts/backup-db.sh
```

The script:

- verifies the database container is running;
- uses PostgreSQL custom format;
- writes to a temporary partial file first;
- refuses an empty backup;
- atomically renames the completed dump;
- writes a SHA-256 sidecar;
- uses restrictive file permissions;
- deletes old local backup files after the configured retention interval.

Default local retention:

```text
14 days
```

Override with:

```bash
BACKUP_RETENTION_DAYS=30 BACKUP_DIR=/mnt/backup/ambulance ./scripts/backup-db.sh
```

A backup stored only on the application VM is not a disaster-recovery backup. Use separate storage or copy the resulting dump/checksum off the VM.

## 11. Restore drill

Verify a backup without touching the live `ambulance` database:

```bash
./scripts/verify-backup.sh /path/to/ambulance-YYYYMMDDTHHMMSSZ.dump
```

The script:

1. verifies the checksum if the sidecar is present;
2. validates the PostgreSQL archive;
3. creates a uniquely named temporary database;
4. restores into the temporary database;
5. verifies core tables;
6. prints core row counts;
7. drops the temporary database.

Run restore drills on a regular schedule and after meaningful schema changes.

## 12. Dispatcher password rotation

After the production-hardening revision is deployed:

```bash
./scripts/rotate-dispatcher-password.sh dispatcher
```

The helper:

- prompts without echo;
- requires a minimum 16-character replacement;
- passes the password to the internal `adminctl` only over stdin;
- bcrypt-hashes the password;
- updates the active user;
- revokes all existing user sessions in the same transaction.

No plaintext password is written to argv or a temporary file by the helper.

After rotation, all dispatcher browsers must authenticate again.

## 13. Device-key rotation

For a provisioned vehicle:

```bash
./scripts/rotate-device-key.sh AMB-01
```

The helper:

- prompts twice without echo;
- requires a replacement key of at least 32 characters;
- hashes the replacement before storage;
- updates exactly one active tracker record;
- revokes existing device sessions for that device;
- does not print the plaintext replacement.

Operational sequence:

1. stop tracking on the physical phone;
2. rotate the key server-side;
3. update the same secret on the phone using **Edit configuration**;
4. restart tracking;
5. verify the dispatcher shows the unit live.

## 14. Android release builds

Normal CI produces a disposable debug APK for testing. Production installs should use a stable signing key so future APKs can update existing installations in place.

Repository secrets required by the manual `android-release` workflow:

```text
ANDROID_KEYSTORE_BASE64
ANDROID_KEYSTORE_PASSWORD
ANDROID_KEY_ALIAS
ANDROID_KEY_PASSWORD
```

The workflow:

- reconstructs the keystore only on the GitHub runner;
- uses the workflow run number as `versionCode`;
- builds `:app:assembleRelease`;
- uploads a signed APK artifact;
- removes the temporary keystore from the runner.

The canonical production keystore must also be stored in a protected offline/recoverable location outside GitHub. Losing it prevents future APKs from updating existing production installations under the same application ID.

## 15. Production data retention

Current behavior:

- Android pending queue trims records older than seven days when the service starts.
- PostgreSQL `location_events` do **not** have automatic application-level retention.
- `audit_log` does **not** have automatic application-level retention.
- Docker service logs are locally bounded but are not a substitute for an audit-retention policy.

Before production sign-off, define retention requirements for location history and audit events, then implement either database partition/retention jobs or an external archival policy.

## 16. Monitoring and alerting gaps

The repository currently provides health endpoints/checks but not a full monitoring stack. Production should add alerts for at least:

- public `/healthz` failure;
- container restart loops;
- PostgreSQL storage utilization;
- backup age/failure;
- tunnel unavailability;
- high authentication failure/rate-limit counts;
- unexpectedly stale fleet telemetry;
- host disk/memory pressure.

## 17. Security incident actions

If a device secret is suspected compromised:

1. stop/revoke the affected device operationally;
2. rotate its device key;
3. confirm existing device sessions are revoked;
4. inspect audit records and recent telemetry;
5. reprovision the physical phone with the new key.

If a dispatcher credential is suspected compromised:

1. rotate the dispatcher password;
2. confirm all existing user sessions are revoked;
3. review login and dispatcher audit records;
4. rotate any related infrastructure secret if there is evidence it was exposed.

If the `.env`, tunnel token, database password, or Android signing key is exposed, treat that as an infrastructure-secret incident rather than only an application-account incident.

## 18. Troubleshooting

### Public health fails, local containers are running

Check:

```bash
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml logs --tail=200 cloudflared
docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml logs --tail=200 caddy
```

Then verify the Cloudflare published hostname points to `http://caddy:80` and DNS/tunnel status is healthy.

### Caddy unhealthy

The Caddy health check proxies `/healthz`, so investigate both API reachability and PostgreSQL health.

### Tracker buffering but dispatcher website works

Check device credentials, bearer authentication, WebSocket connectivity, and whether the phone is pointed at the same public hostname/environment.

### Database disk usage grows

Check PostgreSQL volume size and location-event retention. Do not delete the Docker volume as a cleanup shortcut.

### Never use this as a recovery command

```bash
docker compose down -v
```

The `-v` option removes named volumes and can destroy the PostgreSQL data volume.

## 19. Release gate

A release is not production-ready until the field tests, restore drill, credential rotation, stable Android signing, managed-device process, and relevant compliance/organizational controls in [PRODUCTION_CHECKLIST.md](PRODUCTION_CHECKLIST.md) are complete.
