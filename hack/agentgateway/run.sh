#!/usr/bin/env bash
# Run agentgateway from a local binary against config.yaml.
#
#   ./run.sh                       # uses ./config.yaml
#   ./run.sh ollama-cloud.yaml     # or any other config in this directory
#
# Keys come from the environment. To reuse pi-go's:
#   set -a; . ../../.pi-go/.env; set +a; ./run.sh
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="${SCRIPT_DIR}/${1:-config.yaml}"
[[ $# -gt 0 ]] && shift

# Prefer an explicit override, then a cargo build in the agentgateway checkout,
# then whatever is on PATH.
BINARY="${AGENTGATEWAY_BIN:-}"
if [[ -z "${BINARY}" ]]; then
  REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
  for candidate in "${REPO_ROOT}/tmp/agentgateway/target/release/agentgateway" "$(command -v agentgateway || true)"; do
    if [[ -n "${candidate}" && -x "${candidate}" ]]; then BINARY="${candidate}"; break; fi
  done
fi

if [[ -z "${BINARY}" ]]; then
  echo "agentgateway binary not found." >&2
  echo "Install it, set AGENTGATEWAY_BIN, or build it:" >&2
  echo "  cargo build --release -p agentgateway-app   # in tmp/agentgateway" >&2
  echo "Or skip the binary entirely: docker compose up -d" >&2
  exit 1
fi

# config.yaml resolves ./base-costs.json relative to the working directory.
cd "${SCRIPT_DIR}"
echo "agentgateway $("${BINARY}" -V 2>/dev/null || echo '?') -f ${CONFIG}" >&2
exec "${BINARY}" -f "${CONFIG}" "$@"
