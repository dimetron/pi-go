# Research: Test infrastructure, TUI surfacing, and gates

## Faking a child process

**Primary mechanism: a temp `#!/bin/bash` script used as `Spawner.PiBinary`.**
No fake Go binary, no `TestHelperProcess`/`GO_WANT_HELPER_PROCESS` re-exec, no
`go run`.

- `mockPiScript(t, script) string` — `internal/subagent/spawner_test.go:14`.
  Writes `#!/bin/bash\n` + script to `<tmp>/mock-pi`, mode `0o755`; `t.Skip` on
  Windows (`:16-26`). Comment: "mimics pi --mode json output" (`:13`).
- `fakePi(t, script) string` — `internal/subagent/spawner_timeout_test.go:17`,
  near-identical duplicate writing `<tmp>/fake-pi`.
- `internal/testenv.FakeBinary(t, dir, name)` — `internal/testenv/testenv.go:82`
  (general "do-nothing executable"; `.bat` on Windows).
- `internal/testenv.RequireShell(t) string` — `testenv.go:49`.
- Scripts emit JSONL with `sleep`/`exit` for lifecycle control
  (`spawner_test.go:38-43,163,191,124-129`; `orchestrator_parallel_test.go:22-29`
  models a ~250ms worker with `sleep 0.25`).
- Wiring: `orch.spawner = NewSpawner(piBinary)` (`orchestrator_parallel_test.go:69`)
  or `orch.spawner.PiBinary = ...`. `Spawner` is a plain struct with one field
  (`spawner.go:57-61`); tests mutate `orch.spawner` in place.
- `orch.spawner.PiBinary = "/nonexistent/pi"` is the standard way to force an
  exec-time spawn failure (`orchestrator_test.go:214,244,268,297,322,346`;
  `coverage_test.go:50,69,494`).

ACP/codex children are faked with in-process session structs:
`fakeACPSession` (`spawner_acp_test.go:14`, `finish` at `:36`),
`newFakeACPSession()` (`:20`), `cancelableACPSession` (`:70`),
`fakeCodexSession` (`spawner_codex_test.go:14`, `finish`/`newFakeCodexSession` at
`:21,46`).

## Test seams

Two package-level function variables, both in non-test files, both swappable with
`t.Cleanup` restore:

| Seam | Declared | Type |
|---|---|---|
| `startACPSessionFn` | `internal/subagent/spawner_acp.go:30` (`= startACPSession`); used at `:84` in `dispatchACP` (`:71`) | `func(context.Context, agentName, prompt string, SpawnOpts) (acpSession, error)` |
| `startCodexSessionFn` | `internal/subagent/spawner_codex.go:39` (`= startCodexSession`); used at `:76` in `dispatchCodex` (`:63`) | `func(context.Context, agentName, prompt string, SpawnOpts) (codexSession, error)` |

Return-type interfaces: `acpSession` (`spawner_acp.go:20`) and `codexSession`
(`spawner_codex.go:30`); both doc-comment that they exist for test overrides.

Install helpers: `withFakeACPRunner` (`spawner_acp_test.go:42-49`),
`withFakeCodexRunner` (`spawner_codex_test.go:52-59`),
`captureSpawnOpts` (`orchestrator_timeout_test.go:19-50`).

**There is no seam for `dispatchSpawn` itself**, and no `Orchestrator` interface —
`internal/tools/subagent.go` takes a concrete `*subagent.Orchestrator`
(`:89,:108,:163`), so the tool layer cannot substitute an orchestrator. `explore`/
`task`/`worker` always route to the real `Spawner` and need the `PiBinary` script;
only the ACP names and `codex`/`codex-review` route through the seams.

Test-only backdoors: `(*Orchestrator).SetStatusForTest(agentID, status) bool`
(`orchestrator.go:740`); tests also poke `orch.agents`, `orch.mu`,
`orch.recentTasks`, `orch.worktree` directly.

## How `internal/tools/subagent_test.go` drives the tool

It calls the **unexported handler directly**, not the ADK `Run` closure:

- Real orchestrator: `subagent.NewOrchestrator(defaultConfigPtr(), "", agents)`
  (`subagent_test.go:81,122,201,394,430,563`; `defaultConfigPtr()` at `:12-15`),
  `repoRoot == ""` so worktrees are disabled.
- Driving surface: `subagentHandler(ctx, orch, input, onEvent)`
  (`internal/tools/subagent.go:163`), called 21 times.
- **`agent.Context` is passed as literal `nil`** in every call, relying on
  `resolveContext(nil)` → `context.Background()` (`subagent.go:17`, tested at
  `subagent_test.go:446-451`).
- The exercised surface is therefore validation/limit/summary paths plus pure
  helpers: `detectMode`, `expandChainTemplate`, `buildParallelSummary`,
  `buildChainSummary`, `consumeAgentEvents`, `emitEvent`, `buildSubagentDescription`,
  and registration-only `Name() == "subagent"` checks.
- An `agent.Context` mock exists elsewhere in the package — `mockToolCtx`
  (`internal/tools/tool_invoke_test.go:25`, `var _ agent.Context = mockToolCtx{}`
  at `:82`, driver `runnableTool`/`runTool` at `:84-95`) — but **no test ever calls
  `NewSubagentTool(...).Run(...)`**; the ADK `Run` closure (`subagent.go:93`) is
  untested.
- `noRoleOrchestrator(t, names...)` (`subagent_spawn_fail_test.go:19-33`): real
  orchestrator with `cfg.Roles = nil` so `ResolveRole` fails → `Spawn` errors
  **before any process starts**. The hermetic way to exercise the spawn-failure
  branch. `t.Cleanup(orch.Shutdown)` at `:31`.
- Env-var seam example: `t.Setenv(subagent.ConcurrencyEnvVar, "4")` before
  `NewOrchestrator` (`subagent_description_test.go:15-21`).

`consumeAgentEvents` (`subagent.go:579`) has **no production caller** — only
`subagent_test.go:461,479,492`.

## TUI tests

**No teatest harness.** `charm.land/bubbletea/v2 v2.0.9` is the only bubbletea
dependency (`go.mod:7`); no `teatest` import exists anywhere.
`internal/tui/teatest_test.go` is misnamed — it is a plain unit-test file that
also defines the shared `newTestModel(t) *model` helper (`:19-36`), the
most-used model factory (105 call sites). Mechanism everywhere: direct
`m.Update(msg)` / `m.handleXxx(msg)` and a type-assert back to `*model` — no
`Program`, no screen snapshot.

Agent-event tests live in three files (not `tui_update_test.go`, which has zero
`AgentSubEvent` references):

- `internal/tui/agent_event_test.go:606-820` — `TestAgentSubEvent_SpawnAssignsID`
  (`:608`), `_SpawnAssignsToLatestUnmatched` (`:628`), `_ToolCallAppended` (`:652`),
  `_ToolResultAppended` (`:679`), `_TextDeltaConvertedToText` (`:702`),
  `_MultipleEventsAccumulate` (`:725`), `_RoutedByAgentID` (`:752`),
  `_UnknownAgentIDIgnored` (`:781`), `_ResetsScroll` (`:801`). Pattern:
  `ch := make(chan AgentSubEvent, 1)`, build a bare `&model{...}`, `m.Update(...)`.
- `TestWaitForSubEvent_NilChannel` / `_ReceivesEvent` (`:953,:959`) invoke the
  `tea.Cmd` by hand. `TestInit_WithAgentEventCh` etc. (`:1164-1185`).
- `internal/tui/agent_loop_handlers_test.go:123-180` — `handleAgentSubEvent` via
  `newHandlerModel()` (`:12-16`): `_SpawnBindsCard` (`:125`), `_SpawnNoCard`
  (`:142`), `_TextDeltaMergesIntoCard` (`:154`), `_NonTextEventAppends` (`:170`).
- `internal/tui/bash_stream_test.go:23-72` reuses `handleBashEvent` for
  `bash:start` / `bash:output` kinds — background-bash output **shares** the
  `AgentSubEvent` channel (`agent_loop.go:1776,1812`);
  `BashEventKind(kind)` is exported at `types.go:169`, producers at
  `cli.go:729`, `interactive.go:233`, `maxLiveBashEvents = 64`
  (`agent_loop.go:1781`).
- `internal/tui/tui_mouse_branch_test.go:259-281` builds
  `InitResult{AgentEventCh: ...}` inside an `initEventMsg` and asserts
  `handleInitEvent` wires it — the closest thing to an end-to-end wiring test.

## TUI surfacing of running agents

- Sidebar: pull-based, **per-frame**, not a ticker. `sidebarAgentLines`
  (`internal/tui/sidebar.go:513-529`) calls `in.Orchestrator.List()` once per
  render (`:518`), via `sidebarRenderInput()` (`tui.go:1721-1752`, field at
  `:1745`) from `View()` (`tui.go:1579`). Uses only `Status`, `Type`,
  `StartedAt`, `AgentID`.
- **Defect found:** `agentRow` (`sidebar.go:590-603`) handles `"done"` but the
  orchestrator never emits it — `terminalStatus` yields `completed`/`timeout`,
  `Cancel` yields `canceled`, so finished agents fall through to a dim `∙`
  instead of a green `✓`. `agentStatusPriority` (`:783-796`) has the same gap.
  The `/subagents` list (`commands.go:403-419`) handles `completed` correctly.
- Repaint: every `AgentSubEvent` calls `Update` (`tui.go:827-829`), and a 150ms
  `matrixTickCmd` chain runs **only while `m.running`** (`tui.go:756-771`, started
  at `agent_loop.go:962`, cleared at `agent_loop.go:1906`). **A subagent outliving
  the parent turn therefore has no scheduled repaint** — directly relevant to an
  async feature.
- `/subagents` (`commands.go:106`): `handleAgentsCommand` (`:454-468`) →
  `formatAgentsList` (`:421-452`) with icons from `agentStatusIcon` (`:403-419`:
  `▶ ✓ ✗ ◼ ⚠`) and `countAgentsByStatus` (`:387-401`). No `/status` command
  exists.
- **Slash commands are dropped while a turn runs** (`tui.go:726-729`), so
  `/subagents` cannot inspect agents while the parent turn is in flight.
- Cancel is **whole-turn only**: `handleInterruptKey` (`tui.go:1213-1240`) →
  `cancelAgent` (`agent_loop.go:823-858`) cancels the parent turn context.
  `Orchestrator.Cancel(agentID)` has no production caller. No per-agent UI.
- Non-TUI: the ACP server registers agent tools with a **no-op** event callback
  (`internal/acp/server/runtime.go:470`) and exposes no `List()`/`Get()`; subagent
  state reaches the peer only as tool-call lifecycle
  (`internal/acp/server/adapter/toolcall.go:37-38,97,248-250`). The webserver has
  no agent endpoints (`server.go:81-102`); state is visible only as scraped pixels
  (`pty_agent.go:149-155`). `piagent` holds `orch` but exposes no accessor.

## Legacy wrapper

`internal/tools/agent.go` (24 lines):
`type AgentEventCallback func(agentID, eventType, content string)` (`:10-12`) and
`AgentTools(orch, onEvent)` (`:15-24`) wrapping
`cb = func(ev SubagentEvent) { onEvent(ev.AgentID, ev.Kind, ev.Content) }` →
`SubagentTools(orch, cb)`. It **drops `PipelineID`, `Mode`, `Step`, `Total`**, and
error text arrives flattened into `Content` (`subagent.go:288-295`).

Production callers (4): `internal/cli/interactive.go:226` (TUI),
`internal/cli/cli.go:720` (non-interactive), `internal/acp/server/runtime.go:470`
(no-op), `piagent/agent.go:250`. Tests: `internal/tools/agent_test.go:18,40`,
`internal/agent/e2e_enhanced_test.go:757`.

Consequence: both CLI producers build `tui.AgentSubEvent` with only
`AgentID`/`Kind`/`Content` (`interactive.go:222`, `cli.go:716`), so pipeline
metadata is **always zero-valued in the live TUI**; the pipeline shape reaches the
UI only from tool args (`splitSubagentCards`, `agent_loop.go:1483-1492`).

## Gates

`go.mod:3` → `go 1.27.0`, module `github.com/dimetron/pi-go`, no `toolchain`
directive.

| Target | Recipe | Anchor |
|---|---|---|
| `build` | `cache-clean` then `go build -v -ldflags "-X …/internal/cli.BuildTag=$(git rev-parse --short HEAD)" ./cmd/pi` and `./cmd/pi-sandbox` | `Makefile:21` |
| `test` | ≡ `test-unit` | `Makefile:87` |
| `test-unit` | `go test ./...` | `Makefile:89` |
| `test-integration` | `go test -tags integration ./...` | `Makefile:92` |
| `test-e2e` | `go test -tags e2e ./...` | `Makefile:95` |
| `test-all` | `test-unit test-integration test-e2e` | `Makefile:159` |
| `vet` | `go vet ./...` | `Makefile:196` |
| `lint` | `golangci-lint run ./...` (`.golangci.yml`, v2 per CLAUDE.md) | `Makefile:193` |
| `test-coverage` | `go test -coverprofile=coverage.out -coverpkg=./internal/... ./internal/...` | `Makefile:161` |

CI mirrors with `go test -count=1 ./...` and
`go test -race -count=1 $(go list ./... | grep -v '/internal/acp/server$')`
(`.github/workflows/ci.yml:74,76,92`); release runs
`go test -race -count=1 ./...` (`release.yml:34`).

Tagged tests in scope: only `//go:build e2e` on
`internal/subagent/spawner_codex_e2e_test.go:1` (real codex CLI, `exec.LookPath`
skip) and `internal/tui/run_eval_e2e_test.go:1`. **No `//go:build integration`
exists in `internal/subagent`, `internal/tools`, or `internal/tui`.** Everything
else in the subagent suite is untagged and hermetic (23 test files).
