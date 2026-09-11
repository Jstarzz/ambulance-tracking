#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

username="${1:-dispatcher}"
if [[ -z "$username" ]]; then
  echo "Username is required." >&2
  exit 1
fi

read -r -s -p "New password for $username (16+ characters): " new_password
echo
read -r -s -p "Confirm new password: " confirm_password
echo

if [[ "$new_password" != "$confirm_password" ]]; then
  echo "Passwords do not match." >&2
  exit 1
fi
if (( ${#new_password} < 16 )); then
  echo "Password must be at least 16 characters." >&2
  exit 1
fi

compose=(docker compose -f docker-compose.yml -f docker-compose.cloudflare.yml)
if ! "${compose[@]}" ps --status running --services | grep -Fxq 'api'; then
  echo "API container is not running." >&2
  unset new_password confirm_password
  exit 1
fi

# Pass the new password only over stdin. It is never placed in argv, printed, or
# written to a temporary file. adminctl hashes it with bcrypt and revokes all
# existing dispatcher sessions in the same database transaction.
printf '%s\n' "$new_password" | "${compose[@]}" exec -T api /adminctl rotate-password "$username"
unset new_password confirm_password
