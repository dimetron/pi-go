# Research: Precedent — the bash start/attach/stop pattern

`bash` is the closest existing model of "start a process, keep a handle, read it
later, stop it". A background-subagent feature should look like this, not invent a
new shape.

## Handle format and handoff

- `nextID()` — `internal/tools/bash_supervisor.go:121-126`:
  `fmt.Sprintf("bg_%d", s.seq)`, monotonic per supervisor, never reset.
- `supervise` loop (`:287-320`) has three exits: `<p.done` → `finish`;
  `<-ctx.Done()` → `terminate()` (foreground cancel **kills**, never backgrounds);
  `<-deadline.C` → `background(p, "still running after <elapsed>")`;
  `<-tick.C` → heartbeat, and if `idle >= req.idleTimeout` emit `"stall"` then
  `background(p, "no output for <idle>")`.
- `BashOutput` (`internal/tools/bash.go:52-77`): `Stdout, Stderr, ExitCode,
  Running, Handle, Elapsed, Idle, Timeout, IdleTimeout, Note`. On handoff
  `ExitCode = -1`, `Running = true` (`bash_supervisor.go:396-407`).
- `background()` (`:371-408`) registers, emits `"background"`, consumes the
  initial output via `since(0)` and stores the cursors so the first poll doesn't
  repeat it, then builds the model-facing `Note`:
  `Command <reason> and was moved to the background; it is still running. Call the
  bash_wait tool with handle "bg_N" to read more output, or the bash_kill tool
  with handle "bg_N" to stop it. Do not type these as shell commands — they are
  tool names, not shell syntax.` Plus a "no output at all — probably too broad"
  hint when silent, `limitsHint(...)` (`:423-433`), and `droppedNote(...)`.
- `controlToolName` (`bash.go:226-242`) rejects `bash_wait(handle="bg_1")` typed
  as shell, because backgrounding results embed the control tool names.

## Registry surface

- `newTool[TArgs, TResults any](name, description string, handler
  functiontool.Func[TArgs, TResults], aliases ...map[string]string) (tool.Tool, error)`
  — `internal/tools/registry.go:219`. Builds a lenient runtime schema plus a
  strict declaration schema, wrapped in `*coercingTool` (string→int/bool/JSON
  coercion, alias remapping).
- `BashControlTools(sup *BashSupervisor) ([]tool.Tool, error)` —
  `bash.go:250-276`: `bash_wait` (`handle`, `wait_sec`, default 60s blocking) and
  `bash_kill` (`handle`). Doc comment `:244-249` explains they are deliberately
  **not** in `CoreTools` — only worth advertising when something can background.
- The `bash` tool itself is built inside `CoreTools` (`registry.go:44-88`) via
  `newBashTool(sb, cfg.bashSupervisor)`; `WithBashSupervisor` (`:30-32`); if nil,
  `CoreTools` makes a private supervisor (`:49-51`).
- Tool-list assembly sites all do `CoreTools` + `BashControlTools` + append:
  `piagent/agent.go:226-245`, `internal/cli/interactive.go:411-424`,
  `internal/cli/cli.go:688-699`, `internal/acp/server/runtime.go:446-459`,
  `internal/eval/inventory.go:62-73`.

## Output accumulation and incremental reads

- `stream` (`internal/tools/bash_stream.go:20-114`): `data []byte` retained tail,
  `base` absolute offset of `data[0]`, `end` one past last byte, `notify chan
  struct{}` woken by `Write`. `since(off) (string, next, dropped)` (`:64-75`)
  implements the incremental read; `wait()` (`:110-114`) check-then-wait.
  `streamCap = maxOutputBytes = 256*1024` (`truncate.go:6`).
- `sinkWriter` (`bash_supervisor.go:704-733`) writes to the stream, marks
  activity, and newline-splits into UI events (flush partial at >8192 bytes).
- `OutputSink func(execID, kind, content string)` (`:68`), kinds `start`,
  `output`, `stderr`, `heartbeat`, `stall`, `background`, `exit` (`:62-67`).
  Implementations must not block; `emit` copies the sink under the lock and calls
  it outside (`:112-119`).
- Cursor ownership is per-process (`outCur`/`errCur` under the proc's own mutex,
  `:154-158`), not per-stream — the cursor belongs to the reader.
- `readOutput(handle, wait) (BashStatus, error)` (`:588-635`): lookup (miss →
  `unknown handle %q (running: …)`), optional `awaitChange`, `since(cursor)` for
  both streams, budget, build status, `dropped` measured against **this reader's**
  cursor. If still running, omit `ExitCode` and note "No new output since the last
  read." If exited, set `ExitCode` and **`forget(handle)`** — a spent handle then
  returns "unknown handle".
- `awaitChange` (`:640-649`) selects on `done`, both streams' `wait()`, and a
  timer, so a wait ends on child exit even with zero writes.
- Budgeting: `budgetStreams` = `redactSecrets(truncateOutput(...))`, with stderr
  additionally passed through `stripRuntimeNoise` (`:440-443`).

## Cleanup / reaping / capacity

- Constants (`:18-58`): `defaultIdleTimeout = 90s`, `shortIdleTimeout = 1m`,
  `heartbeatInterval = 5s`, `maxBackgroundProcs = 8`,
  `backgroundMaxLifetime = 30m`.
- `start` uses `context.WithoutCancel(ctx)` for `runCtx` (`:246`) so a background
  command is **not killed by turn end** — the exact property an async subagent
  needs.
- `register` (`:464-484`) evicts `oldestLocked` when at capacity (preferring an
  already-exited victim, `:511-528`), then arms `reap(p, lifetime)` (`:495-506`)
  which kills at the lifetime limit.
- `killHandle` (`:652-696`): terminate, wait ≤ `procs.DefaultWaitDelay`, read
  residual output, report `reaped=false` as `ExitCode = -1` with an explicit note.
- `KillAll` (`:569-581`) snapshots, **replaces the map** (handles stop resolving),
  then terminates outside the lock. Called from `piagent/agent.go:229`,
  `internal/cli/interactive.go:48-53`, `internal/cli/cli.go:748-751`,
  `internal/acp/server/runtime.go:461-466`.

## Why the start/attach/stop split is deliberate

`specs/issues/bash_wait_removal.md` evaluates collapsing `bash_wait` into `bash`
behind a `background: true` flag and **rejects** it: "The axis separating the two
tools is not sync/async but **who owns the process**". The three-tool shape —
`bash` starts, `bash_wait` attaches, `bash_kill` stops — is the accepted
precedent for handle-based async work in this codebase.
