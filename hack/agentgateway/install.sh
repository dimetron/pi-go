#!/usr/bin/env bash
# Install agentgateway to $HOME/agentgateway and bring it up with docker compose.
#
# The repo copy (hack/agentgateway) is the source of truth for the config and
# scripts, but it is not where you run it from — this installs a live copy to
# $HOME/agentgateway so the machine-managed config.yaml, the request-log
# database volume and the backups in backup/ all live somewhere stable, outside
# the repo.
#
#   ./install.sh
#
# What it does, in order:
#   1. rsync the whole directory to $HOME/agentgateway (override with
#      AGENTGATEWAY_HOME), excluding runtime state so stale backups are never copied.
#   2. Copy .env.example -> .env in place if you have never configured keys.
#   3. cd into it and run `docker compose up -d`   (skip with PI_AGW_SKIP_START=1)
#
# After install you normally keep working directly in $HOME/agentgateway:
#   cd ~/agentgateway && docker compose up -d
#   curl -s http://localhost:4000/ui
set -euo pipefail

SRC="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEST="${AGENTGATEWAY_HOME:-$HOME/agentgateway}"

if [[ ! -d "${SRC}" ]]; then
  echo "source directory not found: ${SRC}" >&2
  exit 1
fi

echo "Installing agentgateway -> ${DEST}"
mkdir -p "${DEST}"

# rsync the directory. The trailing slash on SRC copies the *contents*.
# Exclusions keep runtime state and secrets out of a fresh install:
#   backup/  request-log dumps written by backup-db.sh
#   .env     live keys — copied in-place from .env.example only if missing
if command -v rsync >/dev/null 2>&1; then
  rsync -a \
    --exclude '/backup' \
    --exclude '.env' \
    "${SRC}/" "${DEST}/"
else
  # No rsync (macOS/Linux almost always have it); fall back to cp + prune.
  cp -R "${SRC}/." "${DEST}/"
  rm -rf "${DEST}/backup"
  rm -f "${DEST}/.env"
fi

# A fresh install has no keys yet — start from the example so $EDITOR can fill
# them in before the gateway is first used.
if [[ ! -f "${DEST}/.env" ]]; then
  cp "${DEST}/.env.example" "${DEST}/.env"
  echo "created ${DEST}/.env from .env.example — edit it to add your keys:"
  echo "  ${EDITOR:-vi} ${DEST}/.env"
fi

# Backups land here (gitignored, excluded from the copy above).
mkdir -p "${DEST}/backup"

echo "Installed. Work in ${DEST} from now on:"
echo "  cd ${DEST}"
echo "  docker compose up -d"

if [[ "${PI_AGW_SKIP_START:-0}" == "1" ]]; then
  echo "PI_AGW_SKIP_START=1 — not starting compose."
else
  echo "Starting compose in ${DEST}..."
  cd "${DEST}"
  docker compose pull
  docker compose up -d
fi
