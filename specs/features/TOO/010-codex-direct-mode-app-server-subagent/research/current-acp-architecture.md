# Current ACP Subagent Architecture Research

## Overview

pi-go has two subagent spawning paths, both producing a `*Process`:

1. **Non-ACP (native pi)** — `Spawner.Spawn()` runs `pi --mode json` as a subprocess,
   parsing JSONL events from stdout
2. **ACP** — `dispatchACP()` launches an external CLI (claude/gemini/cursor/copilot)
   via the `coder/acp-go-sdk` and translates ACP events

The orchestrator routes based on `isACPAgent(agent.Name)` which checks the
`acpAgentNames` map: `{"claude", "gemini", "cursor", "copilot"}`.

## Key Types

### SpawnOpts (shared by both paths)

```go
type SpawnOpts struct {
    AgentID, Model, WorkDir, Prompt, Instruction string
    Timeout int
    Env []string
    BaseURL string
    Insecure bool
    Headers []string
}
```

### Process (shared handle)

```go
type Process struct {
    cmd    *exec.Cmd       // nil for ACP
    events chan Event      // buffer 64 (non-ACP) or 256 (ACP)
    done   chan struct{}
    cancel context.CancelFunc
    result string
    err    error
    mu     sync.Mutex
}
```

### Two Event Types

- `sharedacp.Event` (internal/acp) — ACP domain: types = "message", "progress", "tool", "stderr", "error"
- `subagent.Event` (internal/subagent) — orchestrator domain: types = "text_delta", "tool_call", "tool_result", "
  message_end", "error"

### RunResult (shared ACP)

```go
type RunResult struct {
    Status, Result, Error, SessionID, Stderr, StopReason string
}
```

## ACP Path Architecture

### acpSession Interface

```go
type acpSession interface {
    Events() <-chan sharedacp.Event
    Done()   <-chan struct{}
    Cancel() error
    Wait()   sharedacp.RunResult
}
```

### dispatchACP Flow

1. Wrap prompt with `acpPromptPreamble(agentName, prompt)` — injects `<Task Completed>!` sentinel
2. Call `startACPSessionFn(ctx, agentName, prompt, opts)` → acpSession
3. Construct `*Process{events: 256, cancel: sess.Cancel + ctx.cancel}`
4. Launch `go pumpACPSession(sess, proc, agentName)`
5. Return proc

### startACPSession Dispatch

Switch on agentName → construct per-agent Runner → call `r.Start(ctx, req)` → `*client.RunningSession`

| Agent   | Package    | Binary             | Args                                              |
|---------|------------|--------------------|---------------------------------------------------|
| claude  | claudecode | bunx               | `-y @agentclientprotocol/claude-agent-acp@latest` |
| gemini  | gemini     | gemini             | `--acp`                                           |
| cursor  | cursor     | agent/cursor-agent | `acp`                                             |
| copilot | copilot    | copilot            | `--acp --stdio`                                   |

### pumpACPSession (Event Translation)

```
EventTypeMessage  → Event{text_delta}  (accumulate for sentinel detection)
EventTypeProgress → Event{tool_call}
EventTypeTool     → Event{tool_call}
EventTypeStderr   → Event{stderr}
EventTypeError    → Event{error}
```

- On sentinel detection → `sess.Cancel()`, coerce to success
- Always emits terminal `message_end` event
- Defer order: close events, then close done

### RunningSession (ACP SDK Lifecycle)

- `RunACPFlow`: Initialize → NewSession → Prompt → closeStdin → waitProcess → finish
- `handleUpdate`: AgentMessageChunk→message, AgentThoughtChunk→progress, ToolCall→tool
- Events channel buffer: 256
- Cancel: kills subprocess
- Wait: blocks on done, returns RunResult

## Per-Agent Runner Pattern

All four runners follow identical structure:

1. `Runner` struct: `ClientInfo`, `Binary`, `Logger`, `ExtraEnv`
2. `RunRequest` struct: `Prompt`, `SessionID`, `CWD`, `Env`, `Command` (test override)
3. `Start(ctx, req) (*client.RunningSession, error)`:
    - `resolveCommand(req)` — binary + args (priority: Binary → Command → env var → defaults)
    - `exec.CommandContext(ctx, binary, args...)`
    - `client.Runner{...}.StartCommand(ctx, cmd, shared.RunRequest{...})`
4. `findBinary(paths)` — PATH lookup + stat
5. `rpcTimeout = 60s` — caps Initialize/NewSession

## Test Mocking

`startACPSessionFn` is a package-level `var` pointing to `startACPSession`.
Tests swap it with a closure returning a `fakeACPSession` that implements `acpSession`.

## Key Architectural Observations

1. **Codex is NOT ACP** — it uses JSON-RPC, not the ACP SDK. Cannot reuse `client.RunningSession`.
2. **No Go SDK for codex** — must implement JSON-RPC client from scratch.
3. **Process is protocol-agnostic** — both paths produce `*Process`; the orchestrator
   doesn't care how events arrive, just that they arrive on the events channel.
4. **Event translation is the integration point** — codex notifications → subagent.Event
5. **The `acpSession` interface won't fit** — codex has different event/result types.
   Need either a new interface or a new dispatch path.
6. **Direct mode = no broker** — spawn `codex app-server` directly, communicate over stdin/stdout.
7. **No sentinel needed** — codex has explicit `turn/completed` notification, unlike ACP agents
   which need the `<Task Completed>!` sentinel hack.
8. **env handling differs** — ACP uses `os.Environ()`, non-ACP uses `FilterEnv(nil)`. Codex
   needs `CODEX_HOME`, `OPENAI_API_KEY` and other codex-specific env vars.