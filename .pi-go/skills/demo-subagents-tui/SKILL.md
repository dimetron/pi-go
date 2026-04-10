# Demo: Subagents in pi-go TUI

This skill demonstrates how to use the **agent tool** to spawn subagents within the pi-go TUI for parallel task execution. Subagents are autonomous child processes that run concurrently, enabling efficient parallelization of independent tasks.

## Overview

The subagent system allows the main agent to spawn child agents that execute tasks independently. Each subagent:
- Runs as a separate `pi` subprocess
- Receives its own prompt and context
- Streams events back to the TUI for live display
- Returns a final result upon completion

**Available agent types:**

| Type | Role | Worktree | Best For |
|------|------|----------|----------|
| `explore` | smol | No | Fast, read-only codebase exploration |
| `plan` | plan | No | Analysis and implementation planning |
| `task` | default | Yes | Full coding tasks with code changes |
| `designer` | slow | Yes | Code creation and modification |
| `reviewer` | slow | No | Code review with git inspection |
| `quick-task` | smol | No | Small, focused tasks |

**Limitations:**
- Maximum **8 concurrent subagents** at a time (pool size: 5)
- Subagents run in isolated git worktrees for `task`, `designer` types
- Worktrees are automatically cleaned up after completion

---

## Three Execution Modes

The subagent tool supports three execution modes:

### Single Mode
Spawn one agent to perform a specific task.

```json
{
  "agent": "explore",
  "task": "Find all test files in the project and identify testing patterns used."
}
```

### Parallel Mode
Spawn multiple agents concurrently (max 8) to work on independent tasks simultaneously.

```json
{
  "tasks": [
    {"agent": "explore", "task": "Find all error handling patterns in internal/tools/"},
    {"agent": "reviewer", "task": "Review the session package for code quality issues"},
    {"agent": "quick-task", "task": "Run go vet on internal/agent/ and report issues"}
  ]
}
```

### Chain Mode
Run agents sequentially, passing each result to the next step.

```json
{
  "chain": [
    {"agent": "explore", "task": "Find the main entry point and session management code"},
    {"agent": "plan", "task": "Based on this: {previous}\nSuggest a refactoring plan for the session package"},
    {"agent": "task", "task": "Implement the refactoring plan: {previous}"}
  ]
}
```

---

## Example Usage in TUI

When you need to perform multiple independent analysis tasks, use the subagent tool:

### Example 1: Parallel Code Analysis

You want to simultaneously:
1. Find all test files and understand testing patterns
2. Review the tools package for code quality
3. Check for any TODO comments in the codebase

**Tool call:**
```
{type: "parallel", tasks: [
  {agent: "explore", task: "Find all *_test.go files and identify testing patterns (table-driven, testify, golden files). List key packages."},
  {agent: "reviewer", task: "Review internal/tools/ package. Note any code smells, missing error wrapping, or areas for improvement."},
  {agent: "quick-task", task: "Search for TODO comments in the codebase. Return file:line and the TODO text."}
]}
```

### Example 2: Sequential Investigation

You want to:
1. First explore the codebase structure
2. Then analyze a specific area based on what you find
3. Finally create a plan for improvements

**Tool call:**
```
{type: "chain", chain: [
  {agent: "explore", task: "Explore the subagent system in internal/subagent/. What are the main components?"},
  {agent: "plan", task: "Based on this exploration: {previous}\nCreate a plan to add unit tests for the orchestrator."},
  {agent: "task", task: "Implement the test plan: {previous}\nFocus on the Spawn and Cancel methods."}
]}
```

---

## TUI Display

When subagents run, the TUI displays:

```
┌─ Subagents ──────────────────────────────────────┐
│ ┌─ explore ─────────────────────────────────────┐ │
│ │ 🟢 Spawned: explore (pipe-1234567890)        │ │
│ │ 📄 Reading: internal/agent/                   │ │
│ │ 📄 Found: 47 test files                      │ │
│ │ ✅ Done (2.3s)                               │ │
│ └───────────────────────────────────────────────┘ │
│ ┌─ reviewer ────────────────────────────────────┐ │
│ │ 🟢 Spawned: reviewer (pipe-1234567890)        │ │
│ │ 📄 Reviewing internal/tools/...               │ │
│ │ ⚠️  Missing error context at tools.go:42     │ │
│ │ ✅ Done (4.1s)                                │ │
│ └───────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────┘
```

---

## Result Structure

Results are returned as structured JSON:

```json
{
  "mode": "parallel",
  "results": [
    {
      "agent": "explore",
      "agent_id": "explore-abc123",
      "status": "completed",
      "result": "Found 47 test files across 12 packages...",
      "duration": "2.3s"
    },
    {
      "agent": "reviewer",
      "agent_id": "reviewer-def456",
      "status": "completed",
      "result": "Review findings:\n- Missing error context...",
      "duration": "4.1s"
    }
  ],
  "summary": "parallel: 2/2 completed in 4.2s"
}
```

---

## Best Practices

### When to Use Subagents

**Use subagents when:**
- Tasks are independent and can run concurrently
- You need specialized expertise (e.g., `reviewer` for code review)
- Exploring multiple paths or areas simultaneously
- Work requires isolated environment (git worktree for code changes)

**Do the work directly when:**
- Tasks are sequential and depend on previous results → use **chain** mode instead
- The task is simple and quick (under 10 lines of code)
- You're uncertain about the approach and need to experiment

### Writing Effective Task Prompts

1. **Be specific about output format**
   - Bad: "Review the code"
   - Good: "Review the code and return: files_reviewed[], issues_found[], recommendations[]"

2. **Include context about the project**
   - Bad: "Find similar patterns"
   - Good: "In this Go project, find functions that follow the builder pattern"

3. **Specify constraints**
   - Time limits, file count limits, or scope limits help agents stay focused
   - Bad: "Test everything"
   - Good: "Run tests for the tools package only. Focus on error handling."

---

## Quick Reference

```bash
# Tool schema for LLM calls
{
  "agent": "string",     # single mode: agent name
  "task": "string",      # single mode: task prompt
  "tasks": [             # parallel mode: array of {agent, task}
    {"agent": "explore", "task": "..."}
  ],
  "chain": [             # chain mode: sequential, supports {previous} template
    {"agent": "explore", "task": "..."},
    {"agent": "plan", "task": "Based on: {previous}"}
  ]
}
```

**Typical workflow:**
1. Analyze the user's request
2. Identify independent subtasks
3. Spawn subagents for each subtask (max 8)
4. Wait for all results
5. Synthesize results into final response
6. Report completion with durations
