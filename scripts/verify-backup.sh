#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

backup="${1:-}"
if [[ -z "$backup" || ! -f "$backup" ]]; then
  echo "Usage: $0 /path/to/ambulance-YYYYMMDDTHHMMSSZ.dump" >&2
  exit 1
fi
backup="$(cd "$(dirname "$backup")" && pwd)/$(basename "$backup")"

checksum="$backup.sha256"
if [[ -f "$checksum" ]]; then
  (
    cd "$(dirname "$backup")"
    sha256sum --check "$(basename "$checksum")"
  )
else
  echo "WARN checksum sidecar not found; continuing with archive validation only." >&2
fi

compose=(docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml)
if ! "${compose[@]}" ps --status running --services | grep -Fxq 'db'; then
  echo "Database container is not running." >&2
  exit 1
fi

# Validate the archive before creating anything in PostgreSQL.
"${compose[@]}" exec -T db pg_restore --list >/dev/null < "$backup"

verify_db="ambulance_restore_verify_$(date -u +%Y%m%d%H%M%S)_$$"
cleanup() {
  "${compose[@]}" exec -T db dropdb --username=ambulance --if-exists "$verify_db" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

"${compose[@]}" exec -T db createdb --username=ambulance "$verify_db"

# Restore into a throwaway database. The production `ambulance` database is never
# modified by this drill.
"${compose[@]}" exec -T db pg_restore \
  --username=ambulance \
  --dbname="$verify_db" \
  --no-owner \
  --no-privileges \
  --exit-on-error < "$backup"

result="$(
  "${compose[@]}" exec -T db psql \
    --username=ambulance \
    --dbname="$verify_db" \
    --no-psqlrc \
    --tuples-only \
    --no-align \
    --set=ON_ERROR_STOP=1 <<'SQL'
SELECT
  CASE WHEN to_regclass('public.vehicles') IS NOT NULL THEN 1 ELSE 0 END || ',' ||
  CASE WHEN to_regclass('public.devices') IS NOT NULL THEN 1 ELSE 0 END || ',' ||
  CASE WHEN to_regclass('public.location_events') IS NOT NULL THEN 1 ELSE 0 END || ',' ||
  CASE WHEN to_regclass('public.audit_log') IS NOT NULL THEN 1 ELSE 0 END;
SQL
)"

result="$(printf '%s' "$result" | tr -d '[:space:]')"
if [[ "$result" != "1,1,1,1" ]]; then
  echo "Restore completed but expected core tables are missing: $result" >&2
  exit 1
fi

counts="$(
  "${compose[@]}" exec -T db psql \
    --username=ambulance \
    --dbname="$verify_db" \
    --no-psqlrc \
    --tuples-only \
    --no-align \
    --set=ON_ERROR_STOP=1 <<'SQL'
SELECT 'vehicles=' || count(*) FROM vehicles;
SELECT 'devices=' || count(*) FROM devices;
SELECT 'locations=' || count(*) FROM location_events;
SELECT 'audit=' || count(*) FROM audit_log;
SQL
)"

printf 'Restore drill passed for %s\n%s\n' "$backup" "$counts"
cleanup
trap - EXIT INT TERM
