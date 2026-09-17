#!/usr/bin/env bash
# Restore a request-log dump (from backup-db.sh) into the compose postgres.
#
#   ./restore-db.sh                       # list available dumps and usage
#   ./restore-db.sh backup/agentgateway-20260913-101500.dump
#
# The restore drops existing objects first (--clean), so it overwrites the
# current request log with the dump's contents. Stop the gateway first so the
# two don't race on the database:
#   docker compose stop agentgateway
#   ./restore-db.sh backup/<dump>.dump
#   docker compose start agentgateway
set -euo pipefail

DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${DIR}"

FILE="${1:-}"
if [[ -z "${FILE}" ]]; then
  echo "usage: $0 <backup/...dump>" >&2
  echo "available dumps:" >&2
  if ls -1 backup/*.dump >/dev/null 2>&1; then
    ls -1ht backup/*.dump >&2
  else
    echo "  none in backup/ — run ./backup-db.sh first" >&2
  fi
  exit 1
fi

if [[ ! -f "${FILE}" ]]; then
  echo "no such dump file: ${FILE}" >&2
  exit 1
fi

set -a; [ -f .env ] && . ./.env; set +a
PGPASSWORD="${POSTGRES_PASSWORD:-agentgateway}"

if ! docker compose ps --services postgres >/dev/null 2>&1 || \
     [[ "$(docker compose ps -q postgres 2>/dev/null)" == "" ]]; then
  echo "postgres service not running — start it first: docker compose up -d postgres" >&2
  exit 1
fi

echo "restoring ${FILE} into agentgateway db (--clean)"
docker compose exec -T -e PGPASSWORD="${PGPASSWORD}" postgres \
  pg_restore -U agentgateway -d agentgateway --clean --if-exists -Fc < "${FILE}"

echo "restored ${FILE}"
