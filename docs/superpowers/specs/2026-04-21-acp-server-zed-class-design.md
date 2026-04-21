# ACP Server — Best-Class Zed Integration

**Date:** 2026-04-21
**Owner:** dimetron
**Status:** Design approved; pending implementation plan

## Problem

The current ACP server (`internal/acp/server`) produces a poor experience in Zed's AI panel:

1. **Duplicate assistant messages.** Every streamed response is emitted twice — once by `runtime.go` as streaming chunks, again by `agent.go` as a full `FinalText`. Users see doubled text.
2. **No thinking surface.** ADK events carry `part.Thought == true` for reasoning content; the runtime filters them out. Zed never shows a "Thinking…" pane.
3. **No tool-call cards.** Tools run invisibly — Zed has no view into file reads, edits, or shell commands.
4. **No plan updates.** Zed's checklist panel is never populated.
5. **Minimal prompt capabilities.** Advertises the bare default (`{audio:false, embeddedContext:false, image:false}`), so Zed's input UI can't offer file-context inlining or image paste.
6. **No slash commands.** `available_commands` is never sent; pi-go's skills and sub-agents are invisible in Zed's autocomplete.
7. **No message-id echo.** `PromptRequest.MessageId` is dropped; Zed can't correlate its prompts with responses.

## Goals

- Eliminate the duplicate-message bug.
- Surface thinking, tool calls (with file locations and sub-agent nesting), and plan updates.
- Advertise capabilities and slash commands appropriate to the resolved provider and workspace.
- Echo message ids for traceability.
- Keep the pi runtime ACP-agnostic: all protocol concerns stay in `internal/acp/server/`.

## Non-goals

- User-facing permission prompts. Default policy is **always auto-approve**; `RequestPermission` is not wired.
- Session modes (`ask` / `plan` / `code`). Single implicit mode; `SetSessionMode` remains `method_not_found`.
- Cross-process session persistence. `LoadSession` stays as-is (creates fresh state for unknown ids).
- Audio prompt capability. Text, embedded-context, and image only.

## Architecture

Three layers, with a new thin adapter package between the ACP surface and the pi runtime:

```
┌──────────────────────────────────────────────────────────┐
│  acp/server/agent.go — ACP protocol surface              │
│  Initialize, NewSession, LoadSession, Prompt, Cancel     │
└──────────────────────┬───────────────────────────────────┘
                       │ PromptHandler, SessionUpdater
┌──────────────────────▼───────────────────────────────────┐
│  acp/server/adapter/ (NEW)                               │
│  ADK event → ACP SessionUpdate translation               │
│  tool-call lifecycle + hierarchical sub-agent nesting    │
└──────────────────────┬───────────────────────────────────┘
                       │ Before/AfterToolCallback
┌──────────────────────▼───────────────────────────────────┐
│  internal/agent + tools + subagent (unchanged)           │
└──────────────────────────────────────────────────────────┘
```

The pi runtime never imports `acp-go-sdk`. Extraction of the adapter also resolves the current `runtime.go` smell (one function doing build + stream + dedup).

## Components

### `acp/server/adapter/stream.go` (new)

Owns per-turn state:

```go
type Stream struct {
    updater    SessionUpdater
    toolCalls  map[string]*callState  // call-id → state
    subagentID string                 // non-empty while inside a sub-agent card
    finalText  strings.Builder
}

func (s *Stream) OnEvent(ctx context.Context, ev *adksession.Event) error
func (s *Stream) OnToolStart(ctx context.Context, name string, args map[string]any) (callID string, err error)
func (s *Stream) OnToolEnd(ctx context.Context, callID string, result any, err error) error
func (s *Stream) OnNestedEvent(ctx context.Context, ev *adksession.Event) error
func (s *Stream) Final() string
```

Dedup fix: `OnEvent` emits `UpdateAgentMessageText` only on streamed chunks. `Final()` just returns the accumulator — `agent.Prompt` no longer re-emits.

### `acp/server/adapter/thinking.go` (new)

Called from `OnEvent` when `part.Thought == true`. Emits `UpdateAgentThoughtText`. Replaces the existing `ev.Content.Role == "thinking"` filter (which was wrong anyway — thought-ness lives on the part, not the content role).

### `acp/server/adapter/toolcall.go` (new)

Maps tool metadata to `SessionUpdateToolCall` fields:

- `kind`: `read`/`grep`/`glob`/`ls` → `ToolKindRead` or `ToolKindSearch`; `edit`/`write` → `ToolKindEdit`; `bash`/`shell` → `ToolKindExecute`; `agent` (sub-agent dispatch) → `ToolKindThink`; else `ToolKindOther`.
- `locations`: extracted from `args["path"]` when present.
- `rawInput` / `rawOutput`: tool args and result.
- `status`: `completed` on success, `failed` on error, `in_progress` between start and end.

Hierarchical nesting:

- If the tool name is `agent` (sub-agent dispatch), set `stream.subagentID = callID` and emit a top-level card with `kind=think`.
- While `subagentID != ""`, nested tool calls append to the parent's `content` via `UpdateToolCall(parent, WithUpdateContent(...))` rather than emitting new top-level cards.
- Clear `subagentID` when the parent's `OnToolEnd` fires.

### `acp/server/adapter/commands.go` (new)

```go
func BuildAvailableCommands(skills []extension.Skill, subagents []subagent.AgentConfig) []acp.AvailableCommand
```

Emits:

- Meta: `/clear` (reset ADK session), `/compact` (summarize history), `/help` (list commands).
- One per discovered skill (name + description from frontmatter).
- One per discovered sub-agent (name + role description).

Called at `Initialize`; embedded in `InitializeResponse`.

### `acp/server/runtime.go` (rewrite streaming loop)

Replace the inline `for ev, err := range ...` body. Construct a `Stream`, register its callbacks as `BeforeToolCallbacks` / `AfterToolCallbacks`, iterate events through `stream.OnEvent`, return `stream.Final()` as `PromptResult.FinalText`.

### `acp/server/agent.go` (capabilities + dedup removal)

- `Initialize` response:
  - `PromptCapabilities{EmbeddedContext: true, Image: providerSupportsImage(provider)}`.
  - `AvailableCommands` from the adapter's command builder.
  - `LoadSession: true` (unchanged).
- `Prompt` echoes `params.MessageId` as `PromptResponse.UserMessageId`.
- **Remove the trailing `updater.Update(ctx, UpdateAgentMessageText(FinalText))`** — the dedup bug's second half.

### `internal/tools/plan.go` (new)

```go
type UpdatePlanTool struct { Emit func(context.Context, []acp.PlanEntry) error }
```

One tool: `update_plan(entries: [{content, status, priority}])`. When invoked, it calls `Emit` with the translated ACP entries. `runtime.go` wires `Emit` to `stream.updater.Update(UpdatePlan(...))`.

Short paragraph added to `piagent.SystemInstruction` teaches the model to call `update_plan` when starting a multi-step task and again after each step completes.

### `internal/subagent/orchestrator.go` (tiny hook)

Add an optional field:

```go
OnNestedEvent func(ctx context.Context, ev *adksession.Event) error
```

When set, the orchestrator pipes its internal streaming through it. `runtime.go` sets `orchestrator.OnNestedEvent = stream.OnNestedEvent` once per turn. The routing decision (top-level vs. nested content) lives inside `Stream`, keyed off `subagentID`; the orchestrator itself stays ACP-agnostic.

## Data flow (one prompt turn)

1. Zed → `agent.Prompt(params)`
2. `agent.Prompt` constructs `Stream{updater, subagentID:"", toolCalls:{}}` and calls `runtime.PromptHandler`.
3. Runtime builds the pi agent with `Before/AfterToolCallbacks = [stream.OnToolStart, stream.OnToolEnd]`, invokes `ag.RunStreaming(...)`, routes each event through `stream.OnEvent`.
4. `OnEvent` per part:
   - `part.Thought == true` → `UpdateAgentThoughtText(part.Text)`
   - plain text → `UpdateAgentMessageText(part.Text)` + `finalText.WriteString(...)`
   - `part.FunctionCall` / `part.FunctionResponse` → ignored by `OnEvent` (already emitted via `OnToolStart` / `OnToolEnd`); suppresses double emission.
5. `OnToolStart` emits `StartToolCall` (or appends to parent's content if nested). Returns `callID`.
6. `OnToolEnd` emits `UpdateToolCall(status)` (or appends to parent's content + closes parent on sub-agent end).
7. `update_plan` tool additionally calls `stream.updater.Update(UpdatePlan(entries))` inside its implementation.
8. Runtime returns `PromptResult{FinalText: stream.Final(), StopReason: endTurn}`.
9. `agent.Prompt` returns `PromptResponse{UserMessageId: params.MessageId, StopReason: endTurn}`. **No extra `UpdateAgentMessageText` here.**

Cancellation: existing path unchanged. `Cancel` cancels the context, `RunStreaming` exits, `Final()` returns partial text, `StopReasonCancelled` is returned.

Emission ordering: `Stream` is single-goroutine per turn; updates are serialized in call order. No locks beyond the existing `Agent.mu`.

## Capabilities and commands advertised

```
InitializeResponse{
  ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
  AgentInfo: {Name: "pi-go", Version: <build>},
  AgentCapabilities: {
    LoadSession: true,
    PromptCapabilities: {
      EmbeddedContext: true,
      Image: <true iff provider ∈ {anthropic, openai, gemini}>,
    },
  },
  AvailableCommands: [
    {name: "clear",   description: "Reset the conversation"},
    {name: "compact", description: "Summarize and shorten history"},
    {name: "help",    description: "List available commands"},
    // one per skill:
    {name: skill.Name, description: skill.Description},
    // one per sub-agent:
    {name: agent.Name, description: agent.Role},
  ],
}
```

`SetSessionMode` and `SetSessionConfigOption` continue to return `method_not_found`.

## Testing

**Unit — `adapter/stream_test.go`** (table-driven against a fake `SessionUpdater`):

- Single text chunk → one `AgentMessageChunk`, no duplicate. **Regression test for the bug.**
- Multiple chunks → N `AgentMessageChunk` in order.
- Thought part → `AgentThoughtChunk`.
- Tool call start→end → `StartToolCall` + `UpdateToolCall(completed)` with correct kind/locations.
- Tool call with error → `UpdateToolCall(failed)`.
- Sub-agent wrapping two inner tool calls → one parent `StartToolCall(kind=think)`, content-appending `UpdateToolCall`s for inner work, one `UpdateToolCall(parent, completed)`. Zero top-level cards for inner tools.
- `update_plan` tool → tool card + `Plan` update with entries.
- Cancellation mid-stream → partial updates, no `finalText` re-emit.

**Unit — `agent_test.go` additions:**

- `Initialize` advertises `PromptCapabilities.EmbeddedContext=true`.
- `Initialize` advertises `Image=true` for anthropic/openai/gemini; `false` for ollama.
- `Initialize` returns `AvailableCommands` containing `/clear`, `/compact`, `/help`, plus every discovered skill and sub-agent.
- `Prompt` echoes `MessageId` back as `UserMessageId`.
- `SetSessionMode` still `method_not_found`.

**Integration — extend `integration_test.go`:**

- One prompt → exactly one text chunk (end-to-end dedup regression).
- Prompt with a fake read tool → `tool_call` + `tool_call_update(completed)` with `kind=read` and the path in `locations`.
- Prompt triggering `update_plan` → a `plan` update arrives.

**Fixtures:** `testdata/adapter/*.json` — recorded ADK event sequences replayed through `Stream`; diffed against expected updates.

**Out of scope for this layer's tests:** provider image handling (provider package), Zed rendering (not ours), real LLM output (existing e2e tests cover the runtime).

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Sub-agent nesting racing with top-level emits | `Stream` is single-goroutine per turn; orchestrator's `OnNestedEvent` runs in the same call stack. |
| `update_plan` tool not used by the model | System-prompt snippet; worst case, plan panel stays empty — no regression. |
| Image capability advertised but provider misconfigured | Provider gate is the same code path already used for model validation; failures surface as normal prompt errors. |
| Skill/sub-agent command names colliding with meta (`/clear`) | Meta names are prefixed-unique; discovery loaders already validate skill names. |
| Dedup fix breaks existing `agent_test.go` | Expected — tests assert the buggy behavior. Updating them is part of the work. |

## Open questions

None. All design decisions made during brainstorming.
