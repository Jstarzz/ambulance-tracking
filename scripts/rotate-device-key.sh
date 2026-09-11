#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

vehicle_code="${1:-}"
if [[ -z "$vehicle_code" ]]; then
  read -r -p "Vehicle code: " vehicle_code
fi
if [[ -z "$vehicle_code" ]]; then
  echo "Vehicle code is required." >&2
  exit 1
fi

read -r -s -p "New device key (32+ characters): " new_key
echo
read -r -s -p "Confirm new device key: " confirm_key
echo

if [[ "$new_key" != "$confirm_key" ]]; then
  echo "Keys do not match." >&2
  exit 1
fi
if (( ${#new_key} < 32 )); then
  echo "Device key must be at least 32 characters." >&2
  exit 1
fi

key_hash="$(printf '%s' "$new_key" | sha256sum | awk '{print $1}')"
unset new_key confirm_key

compose=(docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml)
if ! "${compose[@]}" ps --status running --services | grep -Fxq 'db'; then
  echo "Database container is not running." >&2
  exit 1
fi

updated="$(
  "${compose[@]}" exec -T db psql \
    --username=ambulance \
    --dbname=ambulance \
    --no-psqlrc \
    --tuples-only \
    --no-align \
    --set=ON_ERROR_STOP=1 \
    --set=vehicle_code="$vehicle_code" \
    --set=key_hash="$key_hash" <<'SQL'
WITH changed AS (
  UPDATE devices d
  SET key_hash = decode(:'key_hash', 'hex')
  FROM vehicles v
  WHERE d.vehicle_id = v.id
    AND v.code = :'vehicle_code'
    AND d.active = true
  RETURNING d.id
)
SELECT count(*) FROM changed;
SQL
)"

updated="$(printf '%s' "$updated" | tr -d '[:space:]')"
if [[ "$updated" != "1" ]]; then
  echo "Expected to rotate exactly one active device for $vehicle_code; changed $updated. No key was printed." >&2
  exit 1
fi

echo "Rotated device key for $vehicle_code. Update the physical tracker with the new key before its next authentication."
