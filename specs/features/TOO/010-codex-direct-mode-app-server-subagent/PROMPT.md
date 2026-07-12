# Codex Direct-Mode App-Server Subagent

## Objective

Add "codex" and "codex-review" as new subagent types that spawn the Codex CLI's `app-server` as a subprocess and
communicate via JSON-RPC 2.0 over stdio (direct mode, no broker, no ACP shim). Events are translated into pi-go's
existing `subagent.Event` format and fed through the orchestrator's `*Process` pipeline.

## Key Requirements

1. **New dispatch path** — `dispatchCodex()` in `spawner_codex.go`, separate from `dispatchACP()` and `Spawner.Spawn()`.
   New `codexAgentNames` map + `isCodexAgent()` check.
2. **Codex JSON-RPC client** — `internal/codex/` package: spawn `codex app-server`, speak JSON-RPC over stdin/stdout,
   implement `initialize` → `thread/start` → `turn/start` (or `review/start`) → notifications → `turn/completed`.
3. **Two bundled agents** — `codex` (workspace-write, turn/start) and `codex-review` (read-only, review/start).
4. **Binary resolution** — `findBinary` pattern + `PI_CODEX_CMD` env var override, matching existing claudecode/copilot
   pattern.
5. **Minimal protocol** — Only implement needed methods. TODO comments for future: thread/resume, thread/list,
   externalAgentConfig/import, config/read, account/read.
6. **No sentinel** — Codex has explicit `turn/completed`, no need for the `<Task Completed>!` hack.
7. **Test mocking** — `startCodexSessionFn` var pattern mirrors `startACPSessionFn`.

## Acceptance Criteria

### Codex Task Subagent

- Given codex CLI is installed and authenticated, when orchestrator spawns `codex` agent, then `codex app-server` is
  launched, prompt sent via `turn/start` with `workspace-write` sandbox, events stream back as `subagent.Event` until
  `turn/completed`.

### Codex Review Subagent

- Given codex CLI is installed, when orchestrator spawns `codex-review` agent, then `review/start` is used with
  `read-only` sandbox, review output streams back.

### Binary Not Found

- Given codex CLI not installed, when `codex` agent spawned, then clear error: "codex not found in PATH or default
  locations".

### PI_CODEX_CMD Override

- Given `PI_CODEX_CMD` set to custom path, when `codex` agent spawned, then that binary is used.

### Cancel

- When `Cancel()` called on running codex subagent, then `turn/interrupt` sent and subprocess terminated.

### Thread-ID Filtering

- Given codex spawns a collab/subagent thread, when `turn/completed` arrives for that child thread, then the outer
  session is NOT terminated — only `turn/completed` with matching threadID completes the session.

### Subprocess Crash

- When `codex app-server` exits unexpectedly before `turn/completed`, then the session returns an error result with
  stderr content and `done` is closed.

### Test Mocking

- Tests can override `startCodexSessionFn` with fake `codexSession` to exercise `dispatchCodex` and `pumpCodexSession`
  without real codex binary.

## Implementation Slices

1. **Protocol types** — `internal/codex/protocol.go`: JSON-RPC + codex message types, verify:
   `go build ./internal/codex/`
2. **JSON-RPC client** — `internal/codex/client.go`: spawn `codex app-server`, reader goroutine,
   request/notify/notifications/close, findBinary, PI_CODEX_CMD, verify: `go build ./internal/codex/`
3. **Session + event translation** — `internal/codex/session.go`: Session lifecycle, notification→Event translation,
   turn completion, Cancel, verify: `go build ./internal/codex/`
4. **Dispatch integration** — `internal/subagent/spawner_codex.go`: codexAgentNames, isCodexAgent, codexSession,
   dispatchCodex, pumpCodexSession, verify: `go build ./internal/subagent/`
5. **Orchestrator wiring** — `internal/subagent/orchestrator.go`: third Spawn branch, ACP event logging, verify:
   `go build ./...`
6. **Bundled agents** — `internal/subagent/bundled/codex.md` + `codex-review.md`, verify:
   `go test ./internal/subagent/ -run TestLoadBundled -count=1`
7. **Tests** — client_test.go, session_test.go, spawner_codex_test.go, verify:
   `go test ./internal/codex/ ./internal/subagent/ -count=1`

## Gates

- **build**: `go build ./...`
- **test**: `go test ./internal/codex/ ./internal/subagent/ -count=1`
- **vet**: `go vet ./internal/codex/ ./internal/subagent/`

## Reference

- Design: `specs/features/TOO/010-codex-direct-mode-app-server-subagent/design.md`
- Outline: `specs/features/TOO/010-codex-direct-mode-app-server-subagent/outline.md`
- Plan: `specs/features/TOO/010-codex-direct-mode-app-server-subagent/plan.md`
- Requirements: `specs/features/TOO/010-codex-direct-mode-app-server-subagent/requirements.md`
- Research: `specs/features/TOO/010-codex-direct-mode-app-server-subagent/research/`

## Constraints

- Codex uses JSON-RPC 2.0 over stdio, NOT the ACP SDK — cannot reuse `acpSession`, `RunningSession`, or `pumpACPSession`
- Direct mode only — spawn `codex app-server` as subprocess, no broker
- Codex has explicit `turn/completed` notification — no sentinel detection needed
- **Thread-ID filtering on `turn/completed`**: only terminal if `params.ThreadID == session.threadID` (codex can spawn
  collab/subagent threads)
- **Subprocess crash detection**: goroutine calling `cmd.Wait()` feeds error if process exits before `turn/completed`
- **Stderr streaming**: goroutine drains stderr, emits `Event{Type: "stderr"}`, accumulates into `RunResult.Stderr`
- **Non-blocking event sends**: `select`/`default` drop pattern (mirrors ACP `emit()`)
- **Notification handler must start BEFORE `turn/start`** to avoid missing early notifications; `notifCh` buffer 256
- **Never add codex to `acpAgentNames`** — use `isACPAgent(name) || isCodexAgent(name)` at the logging site only
- Add `"CODEX_HOME"` to `DefaultEnvAllowlist` in `environ.go`
- Use `FilterEnv(nil)` + `opts.Env` for environment
- Mirror existing patterns: claudecode/copilot Runner pattern, spawner_acp.go dispatch pattern, startACPSessionFn test
  injection pattern
- TODO comments for unimplemented protocol methods (thread/resume, thread/list, etc.)