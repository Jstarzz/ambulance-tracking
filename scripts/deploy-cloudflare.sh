#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ ! -f .env ]]; then
  echo "Missing .env"
  echo "Copy .env.cloudflare.example to .env, replace every placeholder, then rerun."
  exit 1
fi

required=(
  CLOUDFLARE_TUNNEL_TOKEN
  POSTGRES_PASSWORD
  DATABASE_URL
  PUBLIC_ORIGIN
  BOOTSTRAP_ADMIN_USERNAME
  BOOTSTRAP_ADMIN_PASSWORD
  DEMO_VEHICLE_CODE
  DEMO_DEVICE_KEY
)

set -a
# shellcheck disable=SC1091
source .env
set +a

for name in "${required[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    echo "Missing required value: $name"
    exit 1
  fi
done

if grep -Eq 'replace-with|change-me|example\.kn' .env; then
  echo "Refusing to deploy: .env still contains placeholder values."
  exit 1
fi

compose=(docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml)

"${compose[@]}" pull db caddy cloudflared
"${compose[@]}" up -d --build --remove-orphans
"${compose[@]}" ps

echo
echo "Deployment started."
echo "Cloudflare Tunnel should route the public hostname to: http://caddy:80"
echo "Health endpoint: https://${PUBLIC_ORIGIN}/healthz"
