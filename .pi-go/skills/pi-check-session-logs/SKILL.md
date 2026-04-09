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

4. **Extract actionable fixes** — After presenting the summary, create (or update) an actionable fixes document:

   ```
   mkdir -p ./specs/sessio-errors/
   echo "<research>" > ./specs/sessio-errors/PROMPT.md
   ```

   This file should contain a prompt that can be loaded to guide the agent toward fixing the detected error patterns. Structure it with sections for:
   - The top N error patterns are detected (with specific examples)
   - Corresponding fix suggestions per pattern
   - Context about when each fix applies (which tools, which scenarios)

   Use this format for the prompt file:

   ```markdown
   # Session Error Fix Prompts

   ## Pattern: [error name]
   - **Detected in**: [sessions/files where found]
   - **Fix suggestion**: [specific actionable steps]
   ```

   If the file already exists, update it to include new patterns while preserving existing ones.

## Examples

- `/pi-check-session-logs` — Analyze all recent session logs

5. **Run the analysis for the findings**

Research on each issue using subagent - for each issue with priority, score, if it's going to be implemented or rejected. 

Save the findings in ./specs/sessio-errors/PROMPT.md