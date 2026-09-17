#!/usr/bin/env bash
# Dump the agentgateway Postgres request log to backup/<timestamp>.dump under
# the permanent deployment home ($HOME/agentgateway after install.sh), using
# the compose `postgres` service.
#
#   ./backup-db.sh                 # -> backup/agentgateway-YYYYMMDD-HHMMSS.dump
#   ./backup-db.sh name-1          # -> backup/name-1.dump
#
# dumps land in backup/ (gitignored and excluded by install.sh) because this
# directory is the natural place to keep them alongside the config — and the
# one thing you rsync/copy to a new machine is then whole. To pull a copy
# somewhere else:
#   scp host:~/.agentgateway/backup/agentgateway-*.dump .
#
# Postgres is not published to the host (see docker-compose.yaml), so the dump
# is taken inside the container and written to stdout, which this script
# redirects into the file. Restore with restore-db.sh.
set -euo pipefail

DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${DIR}"

mkdir -p backup

# POSTGRES_PASSWORD lives in .env (compose uses ${POSTGRES_PASSWORD:-agentgateway}).
# Load only that; do not export the whole file as environment.
set -a; [ -f .env ] && . ./.env; set +a
PGPASSWORD="${POSTGRES_PASSWORD:-agentgateway}"

NAME="${1:-agentgateway-$(date +%Y%m%d-%H%M%S)}"
OUT="backup/${NAME}.dump"

if ! docker compose ps --services postgres >/dev/null 2>&1 || \
     [[ "$(docker compose ps -q postgres 2>/dev/null)" == "" ]]; then
  echo "postgres service not running — start it first: docker compose up -d postgres" >&2
  exit 1
fi

echo "dumping agentgateway db -> ${OUT}"
docker compose exec -T -e PGPASSWORD="${PGPASSWORD}" postgres \
  pg_dump -U agentgateway -d agentgateway -Fc > "${OUT}"

echo "wrote $(cd backup && du -h "${NAME}.dump" | cut -f1)  ${OUT}"
echo "restore with: ./restore-db.sh ${OUT}"
