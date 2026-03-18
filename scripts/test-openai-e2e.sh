#!/usr/bin/env bash
set -euo pipefail

# E2E test: run pi with OpenAI GPT model
# Usage: ./scripts/test-openai-e2e.sh [model]
# Default model: gpt-4o
#
# Requires: OPENAI_API_KEY environment variable

MODEL="${1:-gpt-5.4}"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PI_BIN="${PROJECT_DIR}/pi"
PI_MD="${PROJECT_DIR}/PI.md"
TIMEOUT=180  # 3 minutes (OpenAI can be slower)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; }
info() { echo -e "${YELLOW}→ $1${NC}"; }
detail() { echo -e "${CYAN}  $1${NC}"; }

ERRORS=0
error() { fail "$1"; ERRORS=$((ERRORS + 1)); }

# --- Prerequisites ---

info "Checking prerequisites..."

# Check OPENAI_API_KEY is set
if [ -z "${OPENAI_API_KEY:-}" ]; then
    # Try loading from ~/.pi-go/.env
    if [ -f "$HOME/.pi-go/.env" ]; then
        export $(grep -v '^#' "$HOME/.pi-go/.env" | grep OPENAI_API_KEY | xargs)
    fi
fi

if [ -z "${OPENAI_API_KEY:-}" ]; then
    fail "OPENAI_API_KEY is not set"
    echo "  Set it with: export OPENAI_API_KEY=sk-..."
    echo "  Or add to ~/.pi-go/.env"
    exit 1
fi
pass "OPENAI_API_KEY is set (${#OPENAI_API_KEY} chars)"

# Check jq is available
if ! command -v jq &>/dev/null; then
    fail "jq is required for log validation"
    echo "  Install it with: brew install jq"
    exit 1
fi
pass "jq is available"

# --- Build ---

info "Building pi binary..."
cd "$PROJECT_DIR"
go build -o "$PI_BIN" ./cmd/pi
pass "pi binary built"

# --- Test 1: Simple text response (no tools, --mode print) ---

info "Test 1: Simple text response (model: ${MODEL}, mode: print)..."
echo ""

SIMPLE_PROMPT='Reply with exactly: HELLO_PI_OPENAI. Nothing else.'
START_TIME=$(date +%s)
set +e
SIMPLE_OUT=$(timeout 30 "$PI_BIN" --model "$MODEL" --mode print "$SIMPLE_PROMPT" 2>"${PROJECT_DIR}/.test-stderr-simple.log")
SIMPLE_EXIT=$?
set -e
END_TIME=$(date +%s)
SIMPLE_DURATION=$((END_TIME - START_TIME))

if [ "$SIMPLE_EXIT" -ne 0 ]; then
    error "Test 1 FAILED: pi exited with code ${SIMPLE_EXIT} (${SIMPLE_DURATION}s)"
    if [ -f "${PROJECT_DIR}/.test-stderr-simple.log" ]; then
        echo "  stderr:"
        head -20 "${PROJECT_DIR}/.test-stderr-simple.log"
    fi
else
    if echo "$SIMPLE_OUT" | grep -q "HELLO_PI_OPENAI"; then
        pass "Test 1 PASSED: Got expected response (${SIMPLE_DURATION}s)"
    else
        error "Test 1 FAILED: Response does not contain HELLO_PI_OPENAI"
        detail "Got: $(echo "$SIMPLE_OUT" | head -3)"
    fi
fi
rm -f "${PROJECT_DIR}/.test-stderr-simple.log"

# --- Test 2: JSON mode output ---

info "Test 2: JSON mode output (model: ${MODEL}, mode: json)..."
echo ""

JSON_PROMPT='What is 3+3? Reply with just the number.'
set +e
JSON_OUT=$(timeout 30 "$PI_BIN" --model "$MODEL" --mode json "$JSON_PROMPT" 2>"${PROJECT_DIR}/.test-stderr-json.log")
JSON_EXIT=$?
set -e

if [ "$JSON_EXIT" -ne 0 ]; then
    error "Test 2 FAILED: pi exited with code ${JSON_EXIT}"
    if [ -f "${PROJECT_DIR}/.test-stderr-json.log" ]; then
        echo "  stderr:"
        head -20 "${PROJECT_DIR}/.test-stderr-json.log"
    fi
else
    # Validate JSONL structure
    JSON_LINES=$(echo "$JSON_OUT" | wc -l | tr -d ' ')
    INVALID_JSON=0
    HAS_START=false
    HAS_END=false
    HAS_TEXT=false

    while IFS= read -r line; do
        [ -z "$line" ] && continue
        if ! echo "$line" | jq . >/dev/null 2>&1; then
            INVALID_JSON=$((INVALID_JSON + 1))
            continue
        fi
        TYPE=$(echo "$line" | jq -r '.type // empty')
        case "$TYPE" in
            message_start) HAS_START=true ;;
            message_end)   HAS_END=true ;;
            text_delta)    HAS_TEXT=true ;;
        esac
    done <<< "$JSON_OUT"

    if [ "$INVALID_JSON" -gt 0 ]; then
        error "Test 2: ${INVALID_JSON} invalid JSON lines"
    fi
    if $HAS_START && $HAS_END && $HAS_TEXT; then
        pass "Test 2 PASSED: Valid JSONL with message_start, text_delta, message_end (${JSON_LINES} lines)"
    else
        error "Test 2 FAILED: Missing JSONL events (start=$HAS_START, text=$HAS_TEXT, end=$HAS_END)"
        detail "Output: $(echo "$JSON_OUT" | head -5)"
    fi
fi
rm -f "${PROJECT_DIR}/.test-stderr-json.log"

# --- Test 3: Tool calling with file creation ---

info "Test 3: Tool calling — explore and create PI.md (model: ${MODEL}, mode: print)..."
echo ""

rm -f "$PI_MD"

PROMPT='Explore this Go project codebase. Use the tree tool to see the directory structure, then read go.mod to understand dependencies. Finally, create a file called PI.md in the project root with a brief project overview (project name, purpose, key packages).

Use the tools available to you: tree, read, write. Do NOT skip any tool calls. Execute every tool and use the results.'

START_TIME=$(date +%s)
set +e
timeout "$TIMEOUT" "$PI_BIN" --model "$MODEL" --mode print "$PROMPT" 2>"${PROJECT_DIR}/.test-stderr.log"
PI_EXIT=$?
set -e
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo ""
info "Agent finished in ${DURATION}s (exit code: ${PI_EXIT})"

if [ "$PI_EXIT" -ne 0 ] && [ "$PI_EXIT" -ne 124 ]; then
    error "pi exited with code ${PI_EXIT}"
    if [ -f "${PROJECT_DIR}/.test-stderr.log" ]; then
        echo "  stderr:"
        head -20 "${PROJECT_DIR}/.test-stderr.log"
    fi
fi

if [ "$PI_EXIT" -eq 124 ]; then
    error "pi timed out after ${TIMEOUT}s"
fi

# --- Validate PI.md ---

info "Validating PI.md..."

if [ ! -f "$PI_MD" ]; then
    error "PI.md was not created"
else
    CHAR_COUNT=$(wc -c < "$PI_MD" | tr -d ' ')
    if [ "$CHAR_COUNT" -lt 100 ]; then
        error "PI.md is too short (${CHAR_COUNT} chars, expected > 100)"
    else
        pass "PI.md exists (${CHAR_COUNT} chars)"
    fi

    if grep -qi "pi-go\|pi\.go\|project\|architecture\|package" "$PI_MD"; then
        pass "PI.md contains expected project content"
    else
        error "PI.md does not contain expected project keywords"
    fi
fi

# --- Validate Logs ---

info "Validating session logs..."

LOG_DIR="$HOME/.pi-go/log/$(date +%Y-%m-%d)"
if [ ! -d "$LOG_DIR" ]; then
    error "No log directory found at ${LOG_DIR}"
else
    LATEST_LOG=$(ls -t "$LOG_DIR"/session-*.log 2>/dev/null | head -1)
    if [ -z "$LATEST_LOG" ]; then
        error "No session log files found in ${LOG_DIR}"
    else
        pass "Log file: ${LATEST_LOG}"

        # Count entries by type
        TOTAL=$(wc -l < "$LATEST_LOG" | tr -d ' ')
        TOOL_CALLS=$(grep -c '"type":"tool_call"' "$LATEST_LOG" || true)
        TOOL_RESULTS=$(grep -c '"type":"tool_result"' "$LATEST_LOG" || true)
        ERROR_COUNT=$(grep -c '"type":"error"' "$LATEST_LOG" || true)
        LLM_TEXT=$(grep -c '"type":"llm_text"' "$LATEST_LOG" || true)

        info "Log stats: ${TOTAL} entries, ${TOOL_CALLS} tool_calls, ${TOOL_RESULTS} tool_results, ${LLM_TEXT} llm_text, ${ERROR_COUNT} errors"

        # Check for errors
        if [ "$ERROR_COUNT" -gt 0 ]; then
            error "Found ${ERROR_COUNT} error entries in logs:"
            jq -r 'select(.type == "error") | "  ERROR: \(.content)"' "$LATEST_LOG"
        else
            pass "No error entries in logs"
        fi

        # Check tool_call / tool_result pairing
        if [ "$TOOL_CALLS" -eq 0 ]; then
            error "No tool calls found — agent did not use any tools"
        elif [ "$TOOL_RESULTS" -lt "$TOOL_CALLS" ]; then
            error "Mismatched tool calls: ${TOOL_CALLS} calls but only ${TOOL_RESULTS} results"
        else
            pass "All ${TOOL_CALLS} tool calls have results"
        fi

        # Check for tool result errors
        TOOL_ERRORS=$(jq -r 'select(.type == "tool_result") | select(.content | test("^map\\[error:|^Error:|^error:|^ERROR:")) | "\(.tool): \(.content[0:100])"' "$LATEST_LOG" 2>/dev/null | head -10)
        if [ -n "$TOOL_ERRORS" ]; then
            TOOL_ERROR_COUNT=$(echo "$TOOL_ERRORS" | wc -l | tr -d ' ')
            if [ "$TOOL_ERROR_COUNT" -gt "$((TOOL_CALLS / 2))" ]; then
                error "Too many tool errors (${TOOL_ERROR_COUNT}/${TOOL_CALLS}):"
                echo "$TOOL_ERRORS" | while read -r line; do
                    echo "  $line"
                done
            else
                info "Tool errors (${TOOL_ERROR_COUNT}/${TOOL_CALLS}, within tolerance):"
                echo "$TOOL_ERRORS" | while read -r line; do
                    echo "  $line"
                done
                pass "Tool errors within acceptable threshold"
            fi
        else
            pass "No error indicators in tool results"
        fi

        # Check that LLM actually produced text
        if [ "$LLM_TEXT" -eq 0 ]; then
            error "No LLM text output found"
        else
            pass "LLM produced ${LLM_TEXT} text entries"
        fi
    fi
fi

# --- Cleanup ---

rm -f "${PROJECT_DIR}/.test-stderr.log"

# --- Results ---

echo ""
echo "========================================="
if [ "$ERRORS" -eq 0 ]; then
    pass "ALL CHECKS PASSED (model: ${MODEL})"
    exit 0
else
    fail "${ERRORS} CHECK(S) FAILED (model: ${MODEL})"
    exit 1
fi