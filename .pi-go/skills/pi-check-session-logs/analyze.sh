#!/usr/bin/env bash
# Analyze pi-go session logs for tool call errors.
# Scans ~/.pi-go/log/ for all .log files, extracts errors from tool results,
# deduplicates by pattern, and prints a markdown summary table.

set -euo pipefail

LOG_DIR="${HOME}/.pi-go/log"

if [ ! -d "$LOG_DIR" ]; then
  echo "No log directory found at $LOG_DIR"
  exit 1
fi

LOG_FILES=$(find "$LOG_DIR" -name '*.log' -size +0c 2>/dev/null)
if [ -z "$LOG_FILES" ]; then
  echo "No log files found."
  exit 0
fi

TOTAL_FILES=$(echo "$LOG_FILES" | wc -l | tr -d ' ')
TOTAL_LINES=$(cat $LOG_FILES 2>/dev/null | wc -l | tr -d ' ')

echo "## Session Log Analysis"
echo ""
echo "- **Log files scanned**: ${TOTAL_FILES}"
echo "- **Total log entries**: ${TOTAL_LINES}"
echo ""

# --- STREAM_ERROR / error type events ---
echo "### Error Events (type=error or STREAM_ERROR)"
echo ""

ERROR_COUNT=$(grep -ch '"type":"error"' $LOG_FILES 2>/dev/null | awk '{s+=$1}END{print s+0}')
STREAM_COUNT=$(grep -c 'STREAM_ERROR' $LOG_FILES 2>/dev/null | awk '{s+=$1}END{print s+0}')

if [ "$ERROR_COUNT" -eq 0 ] && [ "$STREAM_COUNT" -eq 0 ]; then
  echo "_No error events found._"
  echo ""
else
  echo "| Type | Count |"
  echo "|------|-------|"
  [ "$ERROR_COUNT" -gt 0 ] && echo "| error event | ${ERROR_COUNT} |"
  [ "$STREAM_COUNT" -gt 0 ] && echo "| STREAM_ERROR | ${STREAM_COUNT} |"
  echo ""
fi

# --- Tool result errors ---
echo "### Tool Result Errors"
echo ""
echo "Tool results containing error indicators (error, failed, not found, permission denied, exit code != 0)."
echo ""

# Extract tool_result lines with error-like content, pull tool name and error snippet
TOOL_ERRORS=$(grep '"type":"tool_result"' $LOG_FILES 2>/dev/null \
  | grep -iE '"content":"[^"]*\b(error|failed|not found|permission denied|no such file|exit.code|panic|timeout|ENOENT|EACCES)\b' \
  | sed -E 's/.*"tool":"([^"]*)".*"content":"([^"]{0,200}).*/\1\t\2/' \
  | sed -E 's/\\n/ /g; s/\\t/ /g; s/\\"/"/g' \
  || true)

if [ -z "$TOOL_ERRORS" ]; then
  echo "_No tool result errors found._"
  echo ""
else
  # Normalize error messages for deduplication:
  # - Strip file paths to generic placeholders
  # - Strip line numbers
  # - Collapse whitespace
  NORMALIZED=$(echo "$TOOL_ERRORS" \
    | sed -E 's|/[^ ",:]+|<path>|g' \
    | sed -E 's/line [0-9]+/line N/g' \
    | sed -E 's/[0-9]{2,}/<num>/g' \
    | sed -E 's/  +/ /g' \
    | cut -c1-120)

  echo "| Tool | Error Pattern | Count |"
  echo "|------|---------------|-------|"

  echo "$NORMALIZED" \
    | sort \
    | uniq -c \
    | sort -rn \
    | head -30 \
    | while read -r count rest; do
        tool=$(echo "$rest" | cut -f1)
        pattern=$(echo "$rest" | cut -f2- | cut -c1-80)
        echo "| ${tool} | ${pattern} | ${count} |"
      done

  echo ""
fi

# --- Tool call frequency ---
echo "### Tool Call Frequency"
echo ""

TOOL_CALLS=$(grep '"type":"tool_call"' $LOG_FILES 2>/dev/null \
  | grep -oE '"tool":"[^"]*"' \
  | sed 's/"tool":"//; s/"//' \
  | sort \
  | uniq -c \
  | sort -rn \
  || true)

if [ -z "$TOOL_CALLS" ]; then
  echo "_No tool calls found._"
else
  echo "| Tool | Calls |"
  echo "|------|-------|"
  echo "$TOOL_CALLS" | while read -r count tool; do
    echo "| ${tool} | ${count} |"
  done
  echo ""
fi

# --- Failed bash commands (exit code != 0) ---
echo "### Failed Bash Commands"
echo ""

BASH_FAILS=$(grep '"type":"tool_result"' $LOG_FILES 2>/dev/null \
  | grep '"tool":"bash"' \
  | grep -v '"exit_code":0' \
  | grep -oE '"exit_code":[0-9]+' \
  | sort \
  | uniq -c \
  | sort -rn \
  || true)

if [ -z "$BASH_FAILS" ]; then
  echo "_No failed bash commands._"
else
  echo "| Exit Code | Count |"
  echo "|-----------|-------|"
  echo "$BASH_FAILS" | while read -r count code; do
    code=$(echo "$code" | sed 's/"exit_code"://')
    echo "| ${code} | ${count} |"
  done
  echo ""
fi

# --- Duplicate file reads (same file read multiple times in same session) ---
echo "### Duplicate File Reads (per session)"
echo ""

DUPES_FOUND=0
for logfile in $LOG_FILES; do
  SESSION_DUPES=$(grep '"type":"tool_call"' "$logfile" 2>/dev/null \
    | grep '"tool":"read"' \
    | grep -oE '"file_path":"[^"]*"' \
    | sort \
    | uniq -cd \
    | sort -rn \
    | head -5 \
    || true)

  if [ -n "$SESSION_DUPES" ]; then
    if [ "$DUPES_FOUND" -eq 0 ]; then
      echo "| Session | File | Reads |"
      echo "|---------|------|-------|"
      DUPES_FOUND=1
    fi
    SESSION=$(basename "$logfile" .log)
    echo "$SESSION_DUPES" | while read -r count path; do
      path=$(echo "$path" | sed 's/"file_path":"//; s/"//')
      echo "| ${SESSION} | ${path} | ${count} |"
    done
  fi
done

if [ "$DUPES_FOUND" -eq 0 ]; then
  echo "_No duplicate file reads detected._"
fi
echo ""
