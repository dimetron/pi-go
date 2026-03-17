---
name: pi-check-session-logs
description: Analyze pi-go session logs for tool call errors, deduplicate them, and present a summary table with error patterns and counts.
---

# Check Session Logs

Scan pi-go session logs for tool call errors, deduplicate by pattern, and present results.

## Steps

1. **Run the analysis script**:

```bash
bash .pi-go/skills/pi-check-session-logs/analyze.sh
```

2. **Present the raw output** to the user as-is — the script already formats a markdown table.

3. **Suggest optimizations** based on the error patterns:
   - For "not found" errors: suggest better path resolution or fallback logic
   - For "permission denied": suggest sandbox configuration changes
   - For "exit code" failures: suggest build/test fixes
   - For repeated tool errors: suggest system prompt improvements to avoid the pattern
   - For STREAM_ERROR: suggest retry/reconnect improvements

## Examples

- `/pi-check-session-logs` — Analyze all recent session logs
