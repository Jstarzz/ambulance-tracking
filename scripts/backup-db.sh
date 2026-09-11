#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BACKUP_DIR="${BACKUP_DIR:-$HOME/ambulance-backups}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
final="$BACKUP_DIR/ambulance-$stamp.dump"
tmp="$final.partial"
checksum="$final.sha256"

compose=(docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml)

cleanup() {
  rm -f "$tmp"
}
trap cleanup EXIT

if ! "${compose[@]}" ps --status running --services | grep -Fxq 'db'; then
  echo "Database container is not running; backup aborted." >&2
  exit 1
fi

if ! "${compose[@]}" exec -T db pg_isready -U ambulance -d ambulance >/dev/null 2>&1; then
  echo "Database is not ready; backup aborted." >&2
  exit 1
fi

# Custom-format pg_dump is compressed internally and is suitable for selective
# or full pg_restore. No database password is printed or written to the backup.
"${compose[@]}" exec -T db pg_dump \
  --username=ambulance \
  --dbname=ambulance \
  --format=custom \
  --no-owner \
  --no-privileges > "$tmp"

if [[ ! -s "$tmp" ]]; then
  echo "Backup is empty; refusing to publish it." >&2
  exit 1
fi

mv "$tmp" "$final"
chmod 600 "$final"
(
  cd "$BACKUP_DIR"
  sha256sum "$(basename "$final")" > "$(basename "$checksum")"
)
chmod 600 "$checksum"

# Keep the local staging area bounded. Production should additionally copy the
# dump and checksum to storage outside this VM.
find "$BACKUP_DIR" -type f \( -name 'ambulance-*.dump' -o -name 'ambulance-*.dump.sha256' \) \
  -mtime "+$BACKUP_RETENTION_DAYS" -delete

trap - EXIT
printf 'Backup complete: %s\nChecksum: %s\n' "$final" "$checksum"
