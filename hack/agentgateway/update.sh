#!/usr/bin/env bash
# Push repo changes to the live agentgateway deployment and apply them.
#
#   ./update.sh                 # copy every managed file to $HOME/agentgateway
#   ./update.sh --dry-run       # show what would change, copy nothing
#   ./update.sh --skip-base     # leave base-costs.json alone (see below)
#   ./update.sh --restart       # force a container restart after copying
#
# Run it FROM THE REPO (hack/agentgateway), not from the deployment. The
# installed copy has no repo to copy from; running it there is a no-op.
#
#   SRC   repo copy      hack/agentgateway     tracked, the source of truth
#   DEST  deployment     $HOME/agentgateway    what docker compose runs
#
# Override the destination with AGENTGATEWAY_HOME.
#
# ## Apply semantics
#
# The gateway watches `config.yaml` and both cost catalogs, so copying them is
# enough — no restart. That was verified, not assumed: setting maxBufferSize to
# a small value changed request handling within seconds and with no restart.
#
# The awkward part is that a config the gateway cannot parse does NOT fail
# loudly. It logs
#
#   error Failed to reload config: ...
#
# and carries on serving the previous config. So a bad copy looks like success.
# This script therefore runs `--validate-only` on config.yaml BEFORE it copies,
# and refuses to push a config that does not parse.
#
# The log check at the end is advisory only: `config.yaml` enables
# llm.prompt/llm.completion logging, so past prompts appear in `docker logs` and
# can echo the same strings. Use --restart when you want certainty.
#
# ## Which direction data flows
#
#   config.yaml       MACHINE-MANAGED. With storage.mode=file (the default) the
#                     gateway rewrites this file when the UI saves, stripping
#                     every comment. Anything created in the UI — including API
#                     keys under llm.policies.apiKey — exists only here, so
#                     copying over it discards those edits. Every overwritten
#                     file is backed up to backup/pre-update-<timestamp>/ first.
#
#   base-costs.json   MACHINE-MANAGED, same reasoning: the gateway overwrites it
#                     on every refresh (POST /api/costs/refresh-base), so the
#                     deployed copy is often NEWER than the repo's. Copying it
#                     can move the catalog backwards, dropping cost data for any
#                     model models.dev has since added. Pass --skip-base to
#                     leave it, or refresh from the live side instead:
#
#                       curl -X POST http://localhost:4000/api/costs/refresh-base \
#                         -H "Authorization: Bearer ${AGENTGATEWAY_API_KEY:-<key>}"
#
#   pi-aliases.json   hand-maintained, so the repo is the source of truth and
#                     copying it here is the normal way to publish an edit.
#
#   everything else   config and scripts; the repo always wins.
#
# NEVER copied: `.env` (live provider keys — the one file the deployment owns)
# and `backup/` (request-log dumps containing real prompt data).
#
# For catalogs only, update-prices.sh does the same job with a narrower scope.
set -euo pipefail

SRC="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEST="${AGENTGATEWAY_HOME:-$HOME/agentgateway}"

DRY_RUN=0
SKIP_BASE=0
FORCE_RESTART=0

usage() {
  sed -n '2,60p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)   DRY_RUN=1 ;;
    --skip-base) SKIP_BASE=1 ;;
    --restart)   FORCE_RESTART=1 ;;
    -h|--help)   usage 0 ;;
    *) echo "unknown option: $1" >&2; usage 1 ;;
  esac
  shift
done

# Refuse to run from the deployment: there is no repo above it to copy from, and
# silently copying a directory onto itself would be a no-op reported as success.
if [[ "${SRC}" == "${DEST}" ]]; then
  echo "This is the installed copy; there is no repo to copy from." >&2
  echo "Run it from the repo instead: hack/agentgateway/update.sh" >&2
  exit 1
fi

if [[ ! -d "${DEST}" ]]; then
  echo "no deployment at ${DEST} — run ./install.sh first" >&2
  exit 1
fi

# Everything the deployment needs and nothing it owns. `.env` and backup/ are
# deliberately absent — see the header.
MANAGED="
.env.example
.gitignore
CLAUDE.md
README.md
backup-db.sh
restore-db.sh
run.sh
install.sh
update.sh
update-prices.sh
migrate-sessions.py
docker-compose.yaml
docker-compose.ollama-cloud.yaml
ollama-cloud.yaml
ollama-cloud-k8s.yaml
config.yaml
pi-aliases.json
base-costs.json
"

# ---------------------------------------------------------------- preflight ---

# A config that does not parse reloads to nothing, silently. Catch it here,
# while the mistake is still in the repo and the old config is still serving.
if command -v docker >/dev/null 2>&1; then
  echo "Validating ${SRC}/config.yaml"
  if ! docker run --rm -v "${SRC}:/etc/agentgateway:ro" -w /etc/agentgateway \
       ghcr.io/agentgateway/agentgateway:v1.5.0 --validate-only -f config.yaml >/tmp/agw-validate.$$ 2>&1; then
    echo "config.yaml does not validate — refusing to copy:" >&2
    sed 's/^/  /' /tmp/agw-validate.$$ >&2
    rm -f /tmp/agw-validate.$$
    exit 1
  fi
  sed 's/^/  /' /tmp/agw-validate.$$
  rm -f /tmp/agw-validate.$$
else
  echo "docker not found — skipping validation (config errors are silent!)" >&2
fi

# A malformed catalog is a warning at runtime, not an error, so cost data would
# just quietly go missing.
for f in pi-aliases.json base-costs.json; do
  [[ -f "${SRC}/${f}" ]] || continue
  if ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${SRC}/${f}" 2>/dev/null; then
    echo "${f} is not valid JSON — refusing to copy" >&2
    exit 1
  fi
done

# ------------------------------------------------------------------- copy ---

STAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP_DIR="${DEST}/backup/pre-update-${STAMP}"
changed=""
copied=0
uptodate=0

# Copy via a temp file and rename. A plain `cp` writes through the existing
# inode; the rename is atomic, and it keeps a half-written config or catalog
# from ever being visible to the gateway's file watcher.
install_file() {
  local f="$1"
  cp "${SRC}/${f}" "${DEST}/${f}.tmp"
  mv -f "${DEST}/${f}.tmp" "${DEST}/${f}"
}

echo
echo "Updating ${DEST} from ${SRC}"
for f in ${MANAGED}; do
  if [[ ! -f "${SRC}/${f}" ]]; then
    continue
  fi

  if [[ "${f}" == "base-costs.json" && "${SKIP_BASE}" == "1" ]]; then
    echo "  ${f}: skipped (--skip-base)"
    continue
  fi

  if [[ ! -f "${DEST}/${f}" ]]; then
    echo "  ${f}: new"
  elif cmp -s "${SRC}/${f}" "${DEST}/${f}"; then
    echo "  ${f}: up to date"
    uptodate=$((uptodate + 1))
    continue
  else
    printf '  %s: updating (%s -> %s)\n' "${f}" \
      "$(du -h "${DEST}/${f}" | cut -f1 | tr -d '[:space:]')" \
      "$(du -h "${SRC}/${f}" | cut -f1 | tr -d '[:space:]')"
  fi

  changed="${changed} ${f}"

  if [[ "${DRY_RUN}" == "1" ]]; then
    continue
  fi

  # Keep the outgoing copy before overwriting. config.yaml and base-costs.json
  # are machine-managed — the UI may have written content that exists nowhere
  # else, and a copy over it is otherwise unrecoverable.
  if [[ -f "${DEST}/${f}" ]]; then
    mkdir -p "${BACKUP_DIR}"
    cp "${DEST}/${f}" "${BACKUP_DIR}/${f}"
  fi

  install_file "${f}"
  copied=$((copied + 1))
done

if [[ -z "${changed}" ]]; then
  echo
  echo "Already up to date — nothing to apply."
  exit 0
fi

if [[ "${DRY_RUN}" == "1" ]]; then
  echo
  echo "Dry run — would copy:${changed}"
  echo "Re-run without --dry-run to apply."
  exit 0
fi

echo
echo "Replaced files were saved to:"
echo "  ${BACKUP_DIR}"
echo "If the deployment carried an edit the repo lacks (config.yaml and"
echo "base-costs.json are machine-managed, and pi-aliases.json is edited by hand),"
echo "diff it before discarding the backup:"
echo "  diff ${BACKUP_DIR}/<file> ${DEST}/<file>"

# ------------------------------------------------------------------ apply ---

# Compose is not hot-reloaded. `docker compose up -d` recreates the container
# only if the service definition actually changed, so this is cheap when it did
# not — but it needs the compose file, so only run it when that is in play.
COMPOSE_CHANGED=0
for f in ${changed}; do
  case "${f}" in
    docker-compose*.yaml|.env.example) COMPOSE_CHANGED=1 ;;
  esac
done

NEED_RESTART=0
[[ "${FORCE_RESTART}" == "1" ]] && NEED_RESTART=1
[[ "${COMPOSE_CHANGED}" == "1" ]] && NEED_RESTART=1

if [[ "${NEED_RESTART}" == "1" ]]; then
  echo
  if [[ "${COMPOSE_CHANGED}" == "1" ]]; then
    echo "Compose definition changed — recreating containers"
  else
    echo "Restarting to apply deterministically"
  fi
  (cd "${DEST}" && docker compose up -d)
else
  echo
  echo "config.yaml and the catalogs are watched — the gateway reloads on its own."
  echo "Re-run with --restart if you want it applied deterministically."
fi

# ---------------------------------------------------------------- verify ---

echo
# Deterministic check: re-validate the copy that actually landed on disk, so a
# truncated or mangled write is caught even though the source parsed.
if command -v docker >/dev/null 2>&1; then
  if docker run --rm -v "${DEST}:/etc/agentgateway:ro" -w /etc/agentgateway \
       ghcr.io/agentgateway/agentgateway:v1.5.0 --validate-only -f config.yaml >/tmp/agw-dest.$$ 2>&1; then
    echo "Deployed config.yaml validates."
  else
    echo "WARNING: the config now on disk does NOT validate:" >&2
    sed 's/^/  /' /tmp/agw-dest.$$ >&2
    echo "Restore it from ${BACKUP_DIR} and investigate." >&2
  fi
  rm -f /tmp/agw-dest.$$
fi

if ! (cd "${DEST}" && docker compose ps -q agentgateway >/dev/null 2>&1); then
  echo "gateway container not running — it will pick the config up on next start"
  exit 0
fi

# Deliberately no log parsing here. config.yaml turns on llm.prompt and
# llm.completion logging, which puts whole conversations — and anything quoted
# in them — into `docker logs`, so grepping for "reload" or "state_manager"
# matches old prompts as readily as real events. An unreliable check is worse
# than none: it reads as a failed apply when nothing is wrong.
if [[ "${NEED_RESTART}" == "0" ]]; then
  echo
  echo "The gateway watches config.yaml and both catalogs, so the change is live."
  echo "That is the documented behaviour and it was verified in practice, but the"
  echo "reload happens on the gateway's side and this script cannot observe it."
  echo "For certainty:"
  echo "  docker restart agentgateway"
  echo "or re-run with --restart."
fi
