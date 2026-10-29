#!/usr/bin/env bash
set -euo pipefail

# E2E test: verify pi resolves gateway route names of ANY shape.
#
# The bug this guards against was not a wrong list of prefixes — it was the
# assumption that a model id can be recovered by stripping prefixes pi-go
# already knows. A gateway may expose a model under any route name, and
# `azure-llm/gpt-6-luna` is a real one. pi's capability checks (which endpoint,
# which token field, which context window) must be answered from the model id
# alone, or a custom route silently takes the wrong wire path.
#
# Usage: ./scripts/test-gateway-routes-e2e.sh [route ...]
# Default routes: azure-llm/gpt-6-luna azure-llm/gpt-5.6-luna
#
# Requires: a reachable agentgateway (AGENTGATEWAY_BASE_URL) and a built pi.
# The assertions are on the WIRE, not on the reply — a billing or quota error
# still proves the endpoint and field chosen were right.

MODEL_ROUTES=("$@")
if [ ${#MODEL_ROUTES[@]} -eq 0 ]; then
    MODEL_ROUTES=("azure-llm/gpt-6-luna" "azure-llm/gpt-5.6-luna")
fi

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PI_BIN="${PROJECT_DIR}/pi"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

pass() { echo -e "${GREEN}PASS${NC}  $1"; }
fail() { echo -e "${RED}FAIL${NC}  $1"; }
info() { echo -e "${YELLOW}----${NC}  $1"; }
detail() { echo -e "${CYAN}      $1${NC}"; }

ERRORS=0
error() { fail "$1"; ERRORS=$((ERRORS + 1)); }

# --- Prerequisites ---

if [ ! -x "$PI_BIN" ]; then
    echo "pi binary not found at $PI_BIN — run 'make build' first" >&2
    exit 1
fi

if [ -f "$HOME/.pi-go/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$HOME/.pi-go/.env"
    set +a
fi

GW_URL="${AGENTGATEWAY_BASE_URL:-http://127.0.0.1:4000}"
if ! curl -sf -m 5 -o /dev/null "$GW_URL/v1/models" \
        -H "Authorization: Bearer ${AGENTGATEWAY_API_KEY:-}"; then
    echo "agentgateway not reachable at $GW_URL — start it first" >&2
    exit 1
fi
info "gateway $GW_URL"

LOG_DIR="$HOME/.pi-go/log/$(date +%Y-%m-%d)"

# run_route echoes the path to the log file for the run it just made.
run_route() {
    local route="$1"
    local before
    before="$(ls -t "$LOG_DIR"/session-*.log 2>/dev/null | head -1 || true)"

    ( cd "$WORKDIR" && timeout 150 "$PI_BIN" \
        --model "agentgateway/$route" \
        --mode print --trace-http "Reply with exactly: OK" ) \
        >/dev/null 2>&1 || true

    # The newest log that is not the one we saw before the run.
    local f
    for f in $(ls -t "$LOG_DIR"/session-*.log 2>/dev/null); do
        if [ "$f" != "$before" ] && grep -q "agentgateway/$route\|$route" "$f" 2>/dev/null; then
            echo "$f"
            return
        fi
    done
}

# --- Assertions ---

for route in "${MODEL_ROUTES[@]}"; do
    log="$(run_route "$route")"
    if [ -z "$log" ]; then
        error "$route — no session log produced"
        continue
    fi

    # Responses-only models must take /v1/responses with max_output_tokens.
    # Everything else is Chat Completions with max_completion_tokens.
    case "$route" in
        *gpt-5.6-luna*|*gpt-5.6-sol*|*gpt-5.6-terra*|*gpt-6-*|*codex*)
            want_endpoint="responses"
            want_field="max_output_tokens"
            ;;
        *)
            want_endpoint="chat/completions"
            want_field="max_completion_tokens"
            ;;
    esac

    ok=1

    if grep -q "v1/$want_endpoint" "$log"; then
        detail "endpoint  /v1/$want_endpoint"
    else
        got="$(grep -o 'v1/[a-z/]*' "$log" | sort -u | tr '\n' ' ')"
        error "$route — wrong endpoint: want /v1/$want_endpoint, got: $got"
        ok=0
    fi

    if grep -q "$want_field\\\\*\":[0-9]" "$log"; then
        detail "field     $want_field (set)"
    else
        error "$route — $want_field absent from the request"
        ok=0
    fi

    # The capability decision must resolve, or auto-compaction is silently off.
    if grep -q "context window unknown" "$log"; then
        error "$route — context window unresolved (auto-compaction disabled)"
        ok=0
    else
        detail "ctxwindow resolved"
    fi

    [ "$ok" -eq 1 ] && pass "$route"
done

echo
if [ "$ERRORS" -eq 0 ]; then
    echo "${#MODEL_ROUTES[@]}/${#MODEL_ROUTES[@]} gateway routes resolved correctly"
    exit 0
fi
echo "$ERRORS assertion(s) failed" >&2
exit 1
