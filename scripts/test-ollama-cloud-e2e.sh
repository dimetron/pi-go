#!/usr/bin/env bash
set -euo pipefail

# E2E test: run pi with Ollama to explore codebase and generate PI.md
# Usage: ./scripts/test-ollama-e2e.sh [model]
# Default model: minimax-m2.5:cloud

MODEL="${1:-minimax-m2.5:cloud}"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PI_BIN="${PROJECT_DIR}/pi"
PI_MD="${PROJECT_DIR}/PI.md"
TIMEOUT=300  # 5 minutes

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; }
info() { echo -e "${YELLOW}→ $1${NC}"; }

ERRORS=0
error() { fail "$1"; ERRORS=$((ERRORS + 1)); }

# --- Prerequisites ---

info "Checking prerequisites..."

# Check ollama is running
if ! curl -sf http://localhost:11434/api/tags >/dev/null 2>&1; then
    fail "Ollama is not running at localhost:11434"
    echo "  Start it with: ollama serve"
    exit 1
fi
pass "Ollama is running"

# Check model is available
MODEL_NAME="${MODEL%:cloud}"  # strip :cloud suffix for ollama check
MODEL_NAME="${MODEL_NAME%:local}"  # strip :local suffix for ollama check
if ! curl -sf http://localhost:11434/api/tags | grep -q "${MODEL_NAME}"; then
    fail "Model '${MODEL_NAME}' not found in Ollama"
    echo "  Pull it with: ollama pull ${MODEL_NAME}"
    exit 1
fi
pass "Model '${MODEL_NAME}' is available"

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

# --- Clean up previous PI.md ---

rm -f "$PI_MD"

# --- Run the agent ---

info "Running pi --model ${MODEL} --mode print (timeout ${TIMEOUT}s)..."
echo ""

PROMPT='Explore this Go project codebase. Use the tree tool to see the directory structure, then read key files to understand the architecture. Finally, create a file called PI.md in the project root with a project overview that includes:
- Project name and purpose
- Architecture overview
- Key packages and their responsibilities
- Technology stack
- How to build and run

Use the tools available to you: tree, read, grep, write. Do NOT skip any tool calls. Execute every tool and use the results.'

START_TIME=$(date +%s)

# Capture exit code but don't abort on failure
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
        cat "${PROJECT_DIR}/.test-stderr.log" | head -20
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
    if [ "$CHAR_COUNT" -lt 500 ]; then
        error "PI.md is too short (${CHAR_COUNT} chars, expected > 500)"
    else
        pass "PI.md exists (${CHAR_COUNT} chars)"
    fi

    # Check for expected content
    if grep -qi "pi-go\|pi\.go\|project\|architecture\|package" "$PI_MD"; then
        pass "PI.md contains expected project content"
    else
        error "PI.md does not contain expected project keywords"
    fi
fi

# --- Validate Logs ---

info "Validating session logs..."

# Find the latest log file
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
            error "Mismatched tool calls: ${TOOL_CALLS} calls but only ${TOOL_RESULTS} results (${TOOL_CALLS - TOOL_RESULTS} skipped)"
        else
            pass "All ${TOOL_CALLS} tool calls have results"
        fi

        # Check for tool result errors (actual tool failures, not content containing "error" in source code).
        # Only match results where the serialized content starts with error markers like "map[error:" or "Error:".
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
    pass "ALL CHECKS PASSED"
    exit 0
else
    fail "${ERRORS} CHECK(S) FAILED"
    exit 1
fi
