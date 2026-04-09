#!/usr/bin/env bash
# Analyze pi-go session logs for tool call errors.
# Single-pass awk scan of ~/.pi-go/log/ — extracts errors, tool frequency,
# bash failures, and duplicate reads. Compatible with macOS (nawk + bash 3).

set -euo pipefail

# Force byte-level processing — log files may contain non-UTF-8 bytes.
export LC_ALL=C

# Prefer gawk on Darwin — significantly faster than BSD nawk on large inputs.
AWK=awk
if [ "$(uname)" = Darwin ] && command -v gawk &>/dev/null; then
  AWK=gawk
fi

LOG_DIR="${HOME}/.pi-go/log"

if [ ! -d "$LOG_DIR" ]; then
  echo "No log directory found at $LOG_DIR"
  exit 1
fi

# Collect log files (non-empty). -print0 handles spaces in paths.
LOG_FILES=()
while IFS= read -r -d '' f; do
  LOG_FILES+=("$f")
done < <(find "$LOG_DIR" -name '*.log' -size +0c -print0 2>/dev/null)

if [ ${#LOG_FILES[@]} -eq 0 ]; then
  echo "No log files found."
  exit 0
fi

# Single-pass awk: process all log files and emit the full markdown report.
# Uses FS='"' so JSON keys and values land in alternating fields.
$AWK -F'"' '
BEGIN {
  files = 0; lines = 0; err_events = 0; stream_errors = 0
  te_count = 0
}

FNR == 1 { files++ }
{ lines++ }

/"type":"error"/ { err_events++; next }
/STREAM_ERROR/   { stream_errors++; next }

/"type":"tool_call"/ {
  tool = ""
  fp = ""
  for (i = 2; i <= NF; i += 2) {
    if ($i == "tool") { tool = $(i+2); continue }
    if ($i == "file_path") { fp = $(i+2); continue }
  }
  if (tool != "") {
    tool_calls[tool]++
    if (tool == "read" && fp != "") {
      key = FILENAME SUBSEP fp
      read_counts[key]++
      read_file[key] = fp
      read_session[key] = FILENAME
    }
  }
  next
}

/"type":"tool_result"/ {
  tool = ""
  ec = ""
  for (i = 2; i <= NF; i += 2) {
    if ($i == "tool") { tool = $(i+2); continue }
    if ($i == "exit_code") {
      v = $(i+1)
      sub(/^:/, "", v)
      sub(/[^0-9].*/, "", v)
      ec = v
      continue
    }
  }

  # Bash exit code failures
  if (tool == "bash" && ec != "" && ec+0 != 0) bash_fails[ec]++

  # Error-bearing tool results
  if (tool == "") next
  lc = tolower($0)
  if (index(lc, "error") == 0 && index(lc, "failed") == 0 && \
      index(lc, "not found") == 0 && index(lc, "permission denied") == 0 && \
      index(lc, "no such file") == 0 && index(lc, "panic") == 0 && \
      index(lc, "timeout") == 0 && index(lc, "enoent") == 0 && \
      index(lc, "eacces") == 0) next

  # Extract content using index() — handles embedded quotes correctly.
  cpos = index($0, "\"content\":\"")
  if (cpos == 0) next
  msg = substr($0, cpos + 11, 200)
  gsub(/\\n/, " ", msg)
  gsub(/\\t/, " ", msg)
  gsub(/\\"/, "\"", msg)
  gsub(/\/[^ ",:]+/, "<path>", msg)
  gsub(/line [0-9]+/, "line N", msg)
  gsub(/[0-9][0-9]+/, "<num>", msg)
  gsub(/  +/, " ", msg)
  msg = substr(msg, 1, 80)
  key = tool "\t" msg
  if (!(key in tool_error_count)) {
    te_order[te_count++] = key
    tool_error_tool[key] = tool
    tool_error_msg[key] = msg
  }
  tool_error_count[key]++
  next
}

END {
  print "## Session Log Analysis"
  print ""
  print "- **Log files scanned**: " files
  print "- **Total log entries**: " lines
  print ""

  # --- Error events ---
  print "### Error Events (type=error or STREAM_ERROR)"
  print ""
  if (err_events == 0 && stream_errors == 0) {
    print "_No error events found._"
  } else {
    print "| Type | Count |"
    print "|------|-------|"
    if (err_events > 0)    print "| error event | " err_events " |"
    if (stream_errors > 0) print "| STREAM_ERROR | " stream_errors " |"
  }
  print ""

  # --- Tool result errors ---
  print "### Tool Result Errors"
  print ""
  print "Tool results containing error indicators."
  print ""
  if (te_count == 0) {
    print "_No tool result errors found._"
  } else {
    print "| Tool | Error Pattern | Count |"
    print "|------|---------------|-------|"
    for (i = 0; i < te_count; i++) sorted[i] = te_order[i]
    for (i = 1; i < te_count; i++) {
      tmp = sorted[i]
      j = i - 1
      while (j >= 0 && tool_error_count[sorted[j]] < tool_error_count[tmp]) {
        sorted[j+1] = sorted[j]; j--
      }
      sorted[j+1] = tmp
    }
    limit = (te_count < 30) ? te_count : 30
    for (i = 0; i < limit; i++) {
      k = sorted[i]
      printf "| %s | %s | %d |\n", tool_error_tool[k], tool_error_msg[k], tool_error_count[k]
    }
  }
  print ""

  # --- Tool call frequency ---
  print "### Tool Call Frequency"
  print ""
  tc_n = 0
  for (t in tool_calls) tc_keys[tc_n++] = t
  if (tc_n == 0) {
    print "_No tool calls found._"
  } else {
    print "| Tool | Calls |"
    print "|------|-------|"
    for (i = 1; i < tc_n; i++) {
      tmp = tc_keys[i]
      j = i - 1
      while (j >= 0 && tool_calls[tc_keys[j]] < tool_calls[tmp]) {
        tc_keys[j+1] = tc_keys[j]; j--
      }
      tc_keys[j+1] = tmp
    }
    for (i = 0; i < tc_n; i++)
      printf "| %s | %d |\n", tc_keys[i], tool_calls[tc_keys[i]]
  }
  print ""

  # --- Failed bash commands ---
  print "### Failed Bash Commands"
  print ""
  bf_n = 0
  for (c in bash_fails) bf_keys[bf_n++] = c
  if (bf_n == 0) {
    print "_No failed bash commands._"
  } else {
    print "| Exit Code | Count |"
    print "|-----------|-------|"
    for (i = 1; i < bf_n; i++) {
      tmp = bf_keys[i]
      j = i - 1
      while (j >= 0 && bash_fails[bf_keys[j]] < bash_fails[tmp]) {
        bf_keys[j+1] = bf_keys[j]; j--
      }
      bf_keys[j+1] = tmp
    }
    for (i = 0; i < bf_n; i++)
      printf "| %s | %d |\n", bf_keys[i], bash_fails[bf_keys[i]]
  }
  print ""

  # --- Duplicate file reads ---
  print "### Duplicate File Reads (per session)"
  print ""
  rd_n = 0
  for (k in read_counts) {
    if (read_counts[k] > 1) rd_keys[rd_n++] = k
  }
  if (rd_n == 0) {
    print "_No duplicate file reads detected._"
  } else {
    print "| Session | File | Reads |"
    print "|---------|------|-------|"
    for (i = 1; i < rd_n; i++) {
      tmp = rd_keys[i]
      j = i - 1
      while (j >= 0 && read_counts[rd_keys[j]] < read_counts[tmp]) {
        rd_keys[j+1] = rd_keys[j]; j--
      }
      rd_keys[j+1] = tmp
    }
    limit = (rd_n < 30) ? rd_n : 30
    for (i = 0; i < limit; i++) {
      k = rd_keys[i]
      sess = read_session[k]
      gsub(/.*\//, "", sess)
      sub(/\.log$/, "", sess)
      printf "| %s | %s | %d |\n", sess, read_file[k], read_counts[k]
    }
  }
  print ""
}
' "${LOG_FILES[@]}"
