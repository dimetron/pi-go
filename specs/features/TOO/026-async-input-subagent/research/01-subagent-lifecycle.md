# Research: Subagent Lifecycle (as it exists today)

All anchors relative to repo root, HEAD `df249b0` (branch
`dimetron/async-input-subagent`).

## Orchestrator.Spawn — end to end

`func (o *Orchestrator) Spawn(ctx context.Context, input SpawnInput) (<-chan Event, string, error)`
— `internal/subagent/orchestrator.go:419-514`.

Ordered steps:

1. `ensurePruneLoop()` (`:421` → `:191-202`) — lazily starts one 30-min ticker.
2. `closed` guard under `o.mu` (`:423-428`) → `"orchestrator is shut down"`.
3. Empty `agent.Name` guard (`:431-434`).
4. `o.cfg.ResolveRole(agent.Role)` for the model (`:437-440`).
5. `o.pool.Acquire(ctx)` — buffered-chan semaphore (`:443-445`; `pool.go:26-33`).
6. **Agent ID** (`:448`): `fmt.Sprintf("%s-%d", agent.Name, uniqueNano())` →
   `"<agentName>-<unixNanos>"`. `uniqueNano` (`:896-917`) is a monotonic CAS loop
   because two spawns minted from one clock reading would silently replace each
   other in `o.agents`.
7. Worktree resolution (`:451-457` → `:518-530`, `:784-792`): explicit
   `input.WorkDir` wins and disables worktree; else `worktree.Create(...)`; else
   `repoRoot`.
8. Timeout (`:461-464`): `agent.Timeout`, with `input.Timeout > 0` overriding.
9. Builds `SpawnOpts` (`:467-479`), `Env: o.spawnEnv(...)` (`:556-569`).
10. `dispatchSpawn(ctx, spawnOpts, agent.Name)` (`:481` → `:575-584`):
    `isACPAgent` → `dispatchACP`; `isCodexAgent` → `dispatchCodex`; default →
    `o.spawner.Spawn`. On error → `abandonSpawn` (`:588-593`) = worktree
    `Cleanup` + `pool.Release`.
11. Builds `*agentState` (`:487-496`) and `trackAgent` (`:498` → `:597-605`).
12. `events := make(chan Event, 64)` (`:506`); `go o.forwardAgentEvents(...)`
    (`:511`); returns `(events, agentID, nil)` (`:513`).

## agentState

`orchestrator.go:92-103`: `ID`, `Type`, `Prompt`, `StartedAt`, `FinishedAt`
(set when status leaves `"running"`), `Process *Process`, `Worktree`,
`SkipCleanup`, `Status`. Status values: `running`, `completed`, `failed`,
`canceled`, `killed`, plus `timeout` from `terminalStatus` (`:648-659`).

**No session ID, no cancel func, and no consumer channel are retained on the
state.** This is the central fact for a steer feature: the state cannot today
reach back into the child.

## Returned channel lifetime

`forwardAgentEvents` (`:609-642`) is the sole owner of the channel:

- `defer close(out)` (`:610`), `defer o.pool.Release()` (`:611`).
- Loops `for ev := range proc.Events()` and does a **blocking** `out <- ev`
  (`:617`) — no `select`/`default`. A slow consumer backpressures this goroutine,
  which stops draining `proc.Events()` (whose producer drops on a full 64-buffer,
  `spawner.go:375-381`).
- After `proc.Wait()` (`:621`): sets `Status`/`FinishedAt` only `if
  state.Status == "running"` (`:623-627`), `evictCompletedAgentsLocked()`
  (`:629`), then publishes `Event{Type:"run_done", Status: finalStatus}`
  (`:634`) and returns → `close(out)`.

**Consequence:** the pool slot is held for the whole life of the event stream. A
caller that abandons the channel without draining pins both the goroutine and its
pool slot forever. Any background-spawn design must drain in a detached
goroutine.

## Observing and stopping (already present)

| Method | Anchor | Notes |
|---|---|---|
| `List() []AgentStatus` | `:713-733` | **Nondeterministic order.** `Duration` only when not running and `FinishedAt` non-zero. |
| `Get(agentID) (AgentStatus, bool)` | `:761-781` | `(zero,false)` when unknown/evicted. |
| `Cancel(agentID) error` | `:806-820` | Errors if missing (`agent %q not found`) or `Status != "running"`. Calls `Process.Cancel()`, sets `canceled` + `FinishedAt`. **Lock held across the kill.** |
| `evictCompletedAgentsLocked()` | `:681-710` | Only called from `forwardAgentEvents` (`:629`). Keeps ≤ `maxCompletedAgents = 50` (`:33-36`); deletes oldest by `FinishedAt`. Running entries never evicted. |

`Cancel` has **no production caller** — tests only
(`internal/subagent/orchestrator_test.go:95,622,634`).

## Timeout / inactivity

`ResolveTimeout(agentTimeoutMs int) TimeoutConfig{Absolute, Inactivity}` —
`internal/subagent/timeout.go:44-77`.

- Defaults: `DefaultAbsoluteTimeout = 20m`, `DefaultInactivityTimeout = 5m`
  (`:22,:32`).
- Priority: default → `PI_SUBAGENT_TIMEOUT_MS` → agent frontmatter `Timeout`.
  An explicit `input.Timeout` beats all (folded in at `orchestrator.go:461-464`).
- `inactivity` clamped to `absolute` (`:68-71`).
- **`pumpChildOutput` (which owns `InactivityTimer`) is used only by the
  pi-binary spawner** (`spawner.go:272-299`). ACP (`spawner_acp.go:81`) and codex
  (`spawner_codex.go:73`) call `ResolveTimeout` but never create an
  `InactivityTimer` — bounded by the absolute deadline only.
- On idle fire (`spawner.go:319-323`): `cancel()` kills the process group, drains,
  returns `timedOutIdle`. `childExitError` (`:358-372`) checks idle **first**, then
  `context.DeadlineExceeded`, then stderr-augmented.

## Concurrency

- `Pool` (`pool.go:8`): buffered-chan semaphore; `Acquire`/`Release`/`Size`/
  `Available`. Created once in `NewOrchestrator` as `NewPool(ConcurrencyFromEnv())`
  (`orchestrator.go:120`).
- `Orchestrator.Concurrency()` (`:827`) = `pool.Size()`.
- `ConcurrencyFromEnv()` (`concurrency.go:26-49`): env `PI_SUBAGENT_CONCURRENCY`,
  default `DefaultPoolSize = 3` (`orchestrator.go:28`), ceiling
  `maxConcurrencyBudget = 64`.
- `childConcurrency(parent)` halves the budget for children
  (`concurrency.go:62`), applied by `ChildEnv` (`environ.go:97-110`).
- Slot is acquired at spawn (`:443`) and released by `forwardAgentEvents`
  (`:611`) — held for the whole stream, not just process start.

## Session resume — does not exist

- pi-binary: `spawnArgs` (`spawner.go:110-132`) emits only `--mode json`,
  `--model`, `--url`, `--insecure`, `--header`, `--system`, `--lsp`, then the
  positional prompt. No `--session`/`--continue`/`--resume`. `SpawnOpts` has no
  session field.
- ACP: every runner's `RunRequest` has `SessionID string // Optional session ID to
  resume` (e.g. `internal/acp/client/claudecode/claude.go:66`), but the subagent
  layer never populates it, and `RunACPFlow` only calls `Initialize` +
  `NewSession` — never `LoadSession`/`ResumeSession`. `req.SessionID` is used
  solely as a label fallback (`internal/acp/client/session.go:170-173`).
- codex: `thread/resume` is explicitly listed as TODO/future
  (`internal/codex/protocol.go:23-25`); `thread/start` is always issued with
  `Ephemeral: true` (`internal/codex/session.go:103-111`).
- Session IDs that do exist are read-only observability: `Event.SessionID`
  (`types.go:105`), forwarded on `message_start`, captured into
  `AgentResult.SessionID` (`internal/tools/subagent.go:303-305`). Nothing consumes
  them to re-enter a session.
- The top-level CLI *does* have resume (`--session`, `--continue`:
  `internal/cli/cli.go:211,1042-1064`) — that is the parent process, not a
  spawned subagent.

## Shutdown

`ShutdownWithTimeout` (`:848-883`): stops the prune ticker, sets `closed`,
cancels every running agent and marks it `canceled`, then blocks on `<-ctx.Done()`
(`:876`) — i.e. it always waits out the full timeout (default 5s, `:841-843`)
rather than returning as processes exit — and finally `worktree.CleanupAll()`.
