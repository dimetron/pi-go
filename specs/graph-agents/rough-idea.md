# Rough Idea: Graph-Based Agents for pi-go

## Source

User request: "review pi-go/tmp/adk -> see how we can improve pi-go and use Graph based agents"

## Idea

pi-go already runs on Google ADK Go (`google.golang.org/adk/v2 v2.1.0`) but only uses the
**agent loop** (`llmagent` + `runner`). The ADK **workflow graph engine** (`adk/v2/workflow`)
— which ships in the same module — is never imported. Meanwhile pi-go hand-rolls all
orchestration as process spawning and prompt-driven JSON pipelines:

- `/run` spec workflow: ~1100 lines of bespoke retry/parallel/gate/merge logic in `internal/tui/run.go`
- `subagent` tool: LLM hand-rolls `{tasks:[...]}` / `{chain:[...]}` JSON pipelines
- No cross-turn orchestration resume, no graph validation, no routing, no fan-in barrier

The idea: adopt ADK's graph engine as the orchestration layer, keeping the subprocess
spawner for isolation. Deterministic pipelines (research fan-out, /run spec execution,
review loops) become declared graphs with retries, joins, routing, and persistence —
while LLM-initiated single spawns stay as-is.

## Reference

- `tmp/adk/adk-go` — Google ADK Go v2.2.0-4-g817fdc0 (workflow engine, workflowagents, examples)
- `tmp/adk/adk-samples` — llm-auditor (sequential critic→reviser), financial-advisor (agenttool)
- `tmp/adk/adk-utils-go` — Redis sessions, Postgres memory, contextguard, langfuse
- `tmp/adk/kagent` — production ADK wiring: A2A subagents with HITL, ACP shim, memory tools
- `specs/features/SOP/plan-command-sop/` — existing /plan + /run PDD pipeline (hand-rolled)
