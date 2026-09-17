#!/usr/bin/env bash
# Push the cost catalogs from the repo copy to the live deployment.
#
#   ./update-prices.sh
#
# Copies base-costs.json and pi-aliases.json from this directory (the repo copy
# under hack/agentgateway) into the deployment home, then waits for the gateway
# to pick them up.
#
# Run it FROM THE REPO, not from $HOME/agentgateway — the installed copy has no
# repo to copy from, and running it there is a no-op.
#
# Which direction, and why:
#
#   pi-aliases.json  hand-maintained, so the repo is the source of truth and
#                    this is the normal way to publish an edit.
#
#   base-costs.json  MACHINE-MANAGED. The gateway overwrites it on every
#                    refresh (POST /api/costs/refresh-base, or the UI's
#                    "refresh costs" action), so the deployed copy is often
#                    NEWER than the repo's. Copying it here can therefore move
#                    the catalog backwards, dropping cost data for any model
#                    models.dev has since added. Pass --skip-base to leave it
#                    alone, or refresh from the live deployment instead:
#
#                      curl -X POST http://localhost:4000/api/costs/refresh-base \
#                        -H "Authorization: Bearer $AGENTGATEWAY_API_KEY"
#
# The gateway watches both files, so no restart is needed — see the README
# section "Cost catalogs".
set -euo pipefail

SRC="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEST="${AGENTGATEWAY_HOME:-$HOME/agentgateway}"

if [[ "${SRC}" == "${DEST}" ]]; then
  echo "This is the installed copy; there is no repo to copy from." >&2
  echo "Run it from the repo instead: hack/agentgateway/update-prices.sh" >&2
  exit 1
fi

if [[ ! -d "${DEST}" ]]; then
  echo "no deployment at ${DEST} — run ./install.sh first" >&2
  exit 1
fi

SKIP_BASE=0
[[ "${1:-}" == "--skip-base" ]] && SKIP_BASE=1

# Validate before copying: a malformed catalog is a startup/runtime problem, and
# the gateway reports it as a warning rather than an error, so cost data would
# just silently go missing.
for f in pi-aliases.json base-costs.json; do
  [[ -f "${SRC}/${f}" ]] || continue
  if ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${SRC}/${f}" 2>/dev/null; then
    echo "${f} is not valid JSON — refusing to copy" >&2
    exit 1
  fi
done

copy() {
  local f="$1" before after
  if [[ ! -f "${DEST}/${f}" ]]; then
    echo "${f}: not in ${DEST}; copying"
  elif cmp -s "${SRC}/${f}" "${DEST}/${f}"; then
    echo "${f}: already up to date"
    return 0
  else
    before="$(cd "${DEST}" && du -h "${f}" | cut -f1)"
    after="$(cd "${SRC}" && du -h "${f}" | cut -f1)"
    echo "${f}: updating (${before} -> ${after})"
  fi
  # Copy to a temp file in the destination, then rename over the target. A
  # plain `cp` writes through the existing inode; mv is atomic and the watcher
  # reacts to the rename either way, but this cannot expose a half-written
  # catalog to the gateway mid-copy.
  cp "${SRC}/${f}" "${DEST}/${f}.tmp"
  mv -f "${DEST}/${f}.tmp" "${DEST}/${f}"
}

echo "Updating catalogs in ${DEST}"
if [[ "${SKIP_BASE}" == "1" ]]; then
  echo "base-costs.json: skipped (--skip-base)"
else
  copy base-costs.json
fi
copy pi-aliases.json

# The watcher reloads on its own; this just confirms it. `docker logs` carries
# full prompts (config.yaml adds llm.prompt/llm.completion), so never print it
# unfiltered — grep to the catalog lines only.
if docker compose -f "${DEST}/docker-compose.yaml" ps -q agentgateway >/dev/null 2>&1; then
  sleep 2
  echo
  echo "catalog state reported by the gateway:"
  docker logs agentgateway --since 2m --tail 500 2>&1 \
    | grep -i 'model catalog loaded' | tail -1 || true
  echo "if that line is older than this run, reload to be sure: docker restart agentgateway"
else
  echo
  echo "gateway container not running — the catalog will load on next start"
fi
