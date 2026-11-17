---
name: pi-check-session-logs
description: Analyze pi-go session logs (last 24h by default) for tool call errors, deduplicate them, and present a summary table with error patterns and counts.
---

# Check Session Logs

Scan recent pi-go session logs for tool call errors, deduplicate by pattern, and present results.

## Steps

1. **Run the analysis script** (scans last 24h by default, override with `HOURS=N`):

```bash
bash .pi-go/skills/pi-check-session-logs/analyze.sh
```

To scan a different time window:

```bash
HOURS=48 bash .pi-go/skills/pi-check-session-logs/analyze.sh
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
   mkdir -p ./specs/issues/001-session-errors
   echo "<research>" > ./specs/issues/001-session-errors/PROMPT.md
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

- `/pi-check-session-logs` — Analyze logs from last 24h
- `/pi-check-session-logs HOURS=48` — Analyze logs from last 48h

5. **Run the analysis for the findings**

Research on each issue using subagent - for each issue with priority, score, if it's going to be implemented or rejected. 

Save the findings in ./specs/issues/001-session-errors/PROMPT.md
## Session history lookup

When the user asks to check a specific session (e.g. `260809-0249-c53d2-7561f`)
or review session history, search under **`$HOME/.pi-go/`** — that is the
session root, not the repo.

Sessions live in `$HOME/.pi-go/sessions/<session-id>/`, one directory per
session (`sessionsDir()` in `internal/cli/cli.go:766`). Each contains:

- `meta.json` — id, title, model, provider, workDir, timestamps, host info.
- `events.jsonl` — the full turn/event stream (user + assistant messages).
- `trajectory.atif.json` — the ATIF trajectory (agent tool-call trace).
- `branches.json` — session branch state.

Other useful files under `$HOME/.pi-go/`:

- `last-session.json` — metadata of the most recent session start.
- `history.jsonl` / `history` — command history.
- `log/` — runtime logs (check here when init or a run fails).
- `config.json` — default model and settings.
- `memory/` — semantic memory store.

Useful commands:

```bash
ls $HOME/.pi-go/sessions/ | grep <session-id>   # confirm a session exists
cat $HOME/.pi-go/sessions/<session-id>/meta.json
tail -n 50 $HOME/.pi-go/sessions/<session-id>/events.jsonl
```

The `session-stats` tool (`internal/tools/session_stats.go`) scans these
directories for anomalies; it defaults to `$HOME/.pi-go/sessions` and accepts a
`session_dir` override.
