# Production acceptance checklist

This project has proven the core tracker -> API -> PostgreSQL -> dispatcher path with a real Android device, including offline buffering and later replay. This checklist is the remaining work before treating the system as production EMS infrastructure.

## Already proven

- Android foreground tracker acquires GNSS fixes and reports telemetry.
- Tracker WebSocket live upload works through Cloudflare Tunnel, Caddy, and the Go API.
- Location records persist in PostgreSQL/PostGIS.
- Dispatcher WebSocket receives live vehicle updates.
- Offline records survive loss of connectivity and replay after reconnect.
- Dispatcher playback is scoped to the newest tracking session so separate app runs are not joined into one fake route.
- Stationary GNSS wander is filtered and implausible jumps are rejected.
- CI builds backend, web, Android, and validates deployment Compose files.
- Backend integration coverage includes two concurrent tracker WebSockets and playback-session isolation.

## Required field acceptance tests

Run these with at least two provisioned phones/vehicles before deployment sign-off.

1. **Cellular-only:** disable Wi-Fi, drive for at least 20 minutes, and verify live dispatcher movement.
2. **Offline/replay:** remove all data connectivity for at least 10 minutes while moving, restore it, and verify the queue drains and the route is complete.
3. **Screen locked:** lock the phone for at least 20 minutes while driving and verify one-second-ish movement continues.
4. **Battery saver:** repeat a short drive with Android battery saver enabled and verify the foreground location service continues.
5. **Process restart:** start tracking, force-stop/reboot the phone, relaunch, and verify a new tracking session is created without joining the old route.
6. **Two vehicles:** run two trackers simultaneously and verify independent presence, telemetry, map markers, and playback.
7. **Bad GPS:** test indoors/under cover and verify poor fixes do not create large map jumps.
8. **Long run:** keep the tracker running for at least four hours and verify memory, battery, queue size, reconnect behavior, and dispatcher freshness.

Record phone model, Android version, start/end time, network conditions, and pass/fail for each run.

## Operations

### Health check

Run:

```bash
./scripts/healthcheck.sh
```

It verifies all five containers, PostgreSQL readiness, and the public `/healthz` endpoint without printing deployment secrets.

### Database backups

Run:

```bash
BACKUP_DIR=/path/on/off-host-storage ./scripts/backup-db.sh
```

The script writes a PostgreSQL custom-format dump plus SHA-256 checksum, uses an atomic temporary file, and retains 14 days by default. Set `BACKUP_RETENTION_DAYS` to override retention.

A backup stored only on the application VM is not sufficient. Copy or mount the backup directory to separate storage and perform a restore drill before production sign-off.

### Device-key rotation

For a provisioned vehicle, run on the application VM:

```bash
./scripts/rotate-device-key.sh AMB-01
```

The script prompts twice for the replacement key without echoing it, hashes the key locally, and updates exactly one active tracker record. The plaintext replacement is never printed. Update the physical phone with the same replacement key before its next authentication. Existing short-lived device sessions may remain usable until they expire, so treat rotation as a controlled maintenance action.

## Stable Android signing

The normal CI job continues to produce a disposable debug APK. Production installs should use the manual `android-release` workflow so every APK is signed by the same key and can update the previous installation in place.

Create one production keystore offline and store it somewhere recoverable outside GitHub. Add these repository Actions secrets:

- `ANDROID_KEYSTORE_BASE64` — base64 of the keystore file.
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

Then run **Actions -> android-release -> Run workflow**. The workflow assigns a monotonically increasing `versionCode`, builds a signed release APK, uploads it as `ambulance-tracker-release-<run number>`, and deletes the temporary keystore from the runner.

Never commit the keystore or any signing password. Losing the production signing key means future builds cannot update already-installed production copies of the app.

## Security / deployment work still required

- Rotate any dispatcher/device credentials that have appeared in chat, logs, screenshots, or shell history.
- Configure MDM or an equivalent managed-device policy for ambulance phones.
- Define who can provision/revoke devices and dispatcher users.
- Add centralized log retention/alerting and protect audit logs from application-level deletion.
- Define backup encryption, off-VM retention, and recovery objectives.
- Review Cloudflare configuration, caching exclusions, WebSocket support, and BAA requirements if ePHI will ever transit the service.
- Complete organization-level HIPAA risk analysis, policies, incident response, workforce controls, and BAAs as applicable. Source code alone is not HIPAA compliance.

## Release gate

Do not call the system production-ready until all required field acceptance tests pass, an off-VM backup has been restored successfully, production credentials have been rotated, and the Android production signing/MDM process is in place.
