# ATIF Specification Research

## What Is ATIF?

ATIF (Agent Trajectory Interchange Format) is a **standardized, JSON-based specification** for logging the complete interaction history of autonomous LLM agents. It captures user messages, agent responses, tool executions, internal reasoning, token metrics, and environment feedback in a single unified format.

## Origin & Maintenance

- **Author:** Boxuan Li
- **Origin:** RFC within the **Harbor** project (`harbor-framework/harbor` on GitHub, ~1,310 stars)
- **Date:** October 2025 (initial v1.0), currently at **v1.6**
- **Canonical spec:** `rfcs/0001-trajectory-format.md` in harbor-framework/harbor repo

## Purpose

ATIF was designed to unify three previously incompatible trajectory formats:
- **MiniSweAgent** (explicit action sequences with bash commands)
- **OpenHands** (structured event logs for replay)
- **Gemini CLI** (session-based message arrays)

The unified format ensures trajectories are usable across: debugging, visualization, **Supervised Fine-Tuning (SFT)**, and **Reinforcement Learning (RL)** pipelines.

## Schema Structure (v1.6)

### Root-level fields

| Field | Type | Required | Description |
|---|---|---|---|
| `schema_version` | String | Yes | e.g. `"ATIF-v1.6"` |
| `session_id` | String | Yes | Unique agent run identifier |
| `agent` | Object | Yes | Agent metadata (name, version, model_name, tool_definitions, extra) |
| `steps` | Array | Yes | Sequential interaction steps |
| `notes` | String | No | Developer notes |
| `final_metrics` | Object | No | Aggregate token/cost metrics |
| `continued_trajectory_ref` | String | No | Link to continuation trajectory file |
| `extra` | Object | No | Custom metadata (v1.1+) |

### StepObject fields

| Field | Type | Required | Description |
|---|---|---|---|
| `step_id` | Integer | Yes | Ordinal index starting from 1 |
| `timestamp` | String | No | ISO 8601 timestamp |
| `source` | String | Yes | `"system"`, `"user"`, or `"agent"` |
| `message` | String or Array | Yes | Text content, or array of ContentPart objects (v1.6+ multimodal) |
| `model_name` | String | No | LLM model used (agent steps only) |
| `reasoning_effort` | String/Float | No | Agent-only effort indicator |
| `reasoning_content` | String | No | Agent's explicit internal reasoning |
| `tool_calls` | Array | No | Tool invocations (agent-only) |
| `observation` | Object | No | Environment feedback / tool results |
| `metrics` | Object | No | Token counts, cost, logprobs, token IDs |
| `is_copied_context` | Boolean | No | Copied-context marker (v1.5+) |
| `extra` | Object | No | Custom step-level metadata |

### ToolCallSchema

- `tool_call_id` - Unique identifier
- `function_name` - Tool function name
- `arguments` - JSON object of arguments

### ObservationSchema

- `results` array, each with:
  - `source_call_id` (linking back to a tool_call_id)
  - `content` (text or multimodal)
  - `subagent_trajectory_ref` (for multi-agent systems)

### MetricsSchema

- `prompt_tokens`, `completion_tokens`, `cached_tokens`
- `cost_usd`
- `prompt_token_ids`, `completion_token_ids` (for RL training)
- `logprobs`
- `extra`

## Version History

| Version | Key Addition |
|---|---|
| v1.0 | Initial spec |
| v1.1 | Root-level `extra` field |
| v1.2 | System steps can carry observations |
| v1.3 | `completion_token_ids` for RL training |
| v1.4 | `prompt_token_ids` for prompt tokenization analysis |
| v1.5 | `tool_definitions` on agent, `is_copied_context` marker |
| v1.6 | Multimodal content (images) via ContentPart arrays |

## Existing Implementations

1. **Python (reference)** — Pydantic models in `harbor-framework/harbor` (`src/harbor/models/trajectories/`), plus CLI validator
2. **Rust** — `leto-labs/atif-rust` crate with strongly typed structs, serde serialization, Harbor compatibility tests (March 2026, MIT)

## Adoption

- **Harbor agents:** Terminus-2 natively produces ATIF
- **AgentLens** (dreadnode/agent-lens) — observability tooling for AI safety; captures ATIF via Claude Agent SDK
- **SFT exporter** in Harbor works with any ATIF-producing agent
- **Opik** (Comet) has Harbor integration
- **LiteLLM** lists Harbor integration
- **LangChain deepagents** includes Harbor library
- HuggingFace dataset: `obaydata/mcp-agent-trajectory-benchmark`

## Related Standards

| Standard | Scope | Relationship to ATIF |
|---|---|---|
| **Agent Client Protocol (ACP)** | Editor-to-agent communication | Complementary — ACP handles live communication; ATIF handles trajectory logging |
| **Model Context Protocol (MCP)** | Tool/resource access for LLMs | Orthogonal — MCP defines how agents access tools; ATIF logs what they did |
| **OpenTelemetry** | Distributed tracing | ATIF is agent-specific; OTel is general observability |
| **SMALL Protocol** | Execution state management | More focused on deterministic state |

## Sources

- [ATIF RFC Specification (GitHub)](https://github.com/harbor-framework/harbor/blob/main/rfcs/0001-trajectory-format.md)
- [harbor-framework/harbor (GitHub)](https://github.com/harbor-framework/harbor)
- [leto-labs/atif-rust (GitHub)](https://github.com/leto-labs/atif-rust)
- [dreadnode/agent-lens (GitHub)](https://github.com/dreadnode/agent-lens)
