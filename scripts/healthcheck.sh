#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

PUBLIC_ORIGIN="${PUBLIC_ORIGIN:-}"
compose=(docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml)

fail=0

check_container() {
  local service="$1"
  if "${compose[@]}" ps --status running "$service" | grep -q "$service"; then
    printf 'OK   container %-12s running\n' "$service"
  else
    printf 'FAIL container %-12s not running\n' "$service" >&2
    fail=1
  fi
}

for service in db api web caddy cloudflared; do
  check_container "$service"
done

if "${compose[@]}" exec -T db pg_isready -U ambulance -d ambulance >/dev/null 2>&1; then
  echo "OK   postgres ready"
else
  echo "FAIL postgres not ready" >&2
  fail=1
fi

if [[ -n "$PUBLIC_ORIGIN" ]]; then
  if body="$(curl --fail --silent --show-error --max-time 10 "https://${PUBLIC_ORIGIN}/healthz" 2>/dev/null)" && [[ "$body" == *'"status":"ok"'* ]]; then
    echo "OK   public health https://${PUBLIC_ORIGIN}/healthz"
  else
    echo "FAIL public health https://${PUBLIC_ORIGIN}/healthz" >&2
    fail=1
  fi
else
  echo "WARN PUBLIC_ORIGIN is unset; public health check skipped"
fi

exit "$fail"
