---
name: learn
description: Analyze recent session logs for tool errors and suggest improvements to prompts and tools. Use to learn from past mistakes.
---

# Learn

Analyze recent pi-go session logs to find recurring tool errors and suggest fixes.

## Steps

1. **Find recent logs**: List log files in `~/.pi-go/log/` sorted by date (most recent first). Pick the last 5-10 session logs.

2. **Spawn analysis subagent**: Use the `agent` tool to spawn a subagent with this prompt:

   "Read the session log files at [paths]. Each line is a JSON object with fields: time, type, agent, tool, content, args. Focus on entries where type is 'error' or 'tool_result' containing error messages. Extract:
   - All error entries (type=error)
   - Tool results containing 'error', 'failed', 'not found', 'permission denied', 'exit code'
   - The tool_call entry immediately before each error (to see what caused it)
   Report a structured summary: error type, count, tool name, example args that caused it."

3. **Categorize findings**: Group errors by:
   - **Tool errors**: wrong file paths, missing files, bad arguments
   - **Build errors**: compilation failures, test failures
   - **Permission errors**: access denied, sandbox issues
   - **Timeout/crash**: tool hangs, panics

4. **Suggest fixes**: For each error category, suggest:
   - System prompt improvements (add rules to prevent the error)
   - Tool improvements (better validation, error messages, defaults)
   - Skill improvements (update skill instructions to avoid the pattern)

5. **Report**: Present findings as:

       ## Error Summary
       | Error Type | Count | Tool | Cause |
       |------------|-------|------|-------|
       | ...        | ...   | ...  | ...   |

       ## Suggested Fixes
       ### Prompt Updates
       - ...
       ### Tool Updates
       - ...

6. **Apply fixes**: Ask the user which fixes to apply, then:
   - Update system instruction in `internal/agent/agent.go` if prompt changes needed
   - Update tool implementations in `internal/tools/` if tool changes needed
   - Update skill files in `.pi-go/skills/` if skill changes needed

## Examples

- `/learn` — Analyze last 10 sessions and suggest improvements

## Guidelines

- Use subagents for log analysis to avoid flooding the main context
- Only read log files, never modify them
- Focus on recurring patterns (3+ occurrences), not one-off errors
- Prioritize fixes that prevent the most common errors
- Ask before applying any code changes
