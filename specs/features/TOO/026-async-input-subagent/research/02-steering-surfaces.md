# Research: Delivering more input to a running child

## The three spawn paths, and their stdin

| Path | Child | stdin today | Can it take a second prompt? |
|---|---|---|---|
| default (`explore`, `plan`, `task`, `worker`, `quick-task`, `code-reviewer`, `spec-reviewer`, `memory-compressor`) | `pi --mode json "<prompt>"` | **never set** — `cmd.Stdin` is nil throughout `internal/subagent` (zero grep hits), so the child gets the null device. The prompt is positional in argv (`spawner.go:110-132`, "The prompt is positional and must stay last"). | Not as spawned. But `pi --mode rpc` **is** a steerable stdin server (below). |
| ACP (`claude`, `gemini`, `cursor`, `copilot`, `agy`; `orchestrator.go:78-84`) | ACP agent over stdio | `cmd.StdinPipe()` → `acp.NewClientSideConnection(client, stdin, stdout)` (`internal/acp/client/runner.go:37,56`) | Mechanically yes, but pi-go closes stdin right after the single `conn.Prompt` returns (`internal/acp/client/session.go:191`), plus `defer closeStdinOnce()` (`:210`). Then `waitProcess` → `cmd.Wait()` reaps it. |
| codex (`codex`, `codex-review`; `spawner_codex.go:17-20`) | codex app-server | `cmd.StdinPipe()` kept as `Client.stdin` (`internal/codex/client.go:110,68`) | Yes at protocol level; `Session.finish` calls `client.close()`, which closes stdin **and kills the process group** (`session.go:586`; `client.go:332-355`). No `turn/steer` constant exists. |

`Spawner`/`Process` exposes only `Events()`, `Wait()`, `Cancel()` — no
write-to-child, steer, or `SendInput` API anywhere (`spawner.go:90-105`).

## pi's own RPC server is a steerable child — the key finding

`internal/pirpc` already implements a newline-delimited JSON server over stdin
that keeps serving after each turn:

- Framing: `bufio.Scanner` over `s.in`, one JSON object per line, max
  `maxCommandBytes = 32<<20` (`rpc.go:149,159`). Blank lines skipped. Malformed
  JSON replies with an error and **keeps serving** (`:172-177`). Loop exits on
  scanner EOF or `ctx.Done()` (`:161-166,180-183`).
- Commands (`:121-135`, dispatched `:188-257`): `prompt`, `abort`, `get_state`,
  `get_available_models`, `get_session_stats`, `get_commands`, `get_messages`,
  `set_model`, plus accepted-noop `set_thinking_level`, `set_follow_up_mode`,
  **`set_steering_mode`**, `set_auto_compaction`, `set_session_name`,
  `switch_session`, `compact`. `export_html` is explicitly rejected.
- Everything except `prompt` answers inline; `prompt` replies then
  `go s.runTurn(ctx, cmd.Message)` (`:190-196`) — so **stdin keeps being read
  while a turn streams**. Rationale comment at `:28-29,186-187`.
- `abort` (`:198-216`) cancels the single in-flight turn: reads `s.cancel` under
  `s.mu`, replies success first, then `cancel()`.
- Turns are **not** serialized — no busy check, no mutex. `s.cancel` is a single
  slot (`:101,263-266`), so a second concurrent prompt would clobber the first's
  cancel handle.
- `runTurn` (`:262-312`) drives `s.agent.RunStreaming(turnCtx, s.sessionID, message)`
  wrapped in `agent.WithRetry(...)` — i.e. ADK `runner.Run`, SSE streaming,
  `DefaultUserID`. Session id is fixed at construction (`:63,110`) and never
  mutated.
- Wired in `internal/cli/cli.go:1150-1174` with `In: os.Stdin`, `Out: os.Stdout`
  hardcoded; `Config.In/Out` are `io.Reader`/`io.Writer` (`rpc.go:64-65`), so the
  server itself is transport-agnostic. **No TTY check** on the rpc path, and
  `--mode rpc` with an empty prompt is legal (the `prompt == ""` early return at
  `cli.go:1176-1179` is *after* the rpc branch).
- Caveat: `Run` returns at stdin EOF while the turn goroutine may still be
  running; the returned error unwinds `defer runtime.close()` (`cli.go:772`,
  `close()` at `:741-756` → bash `KillAll`, `orch.Shutdown()`, sandbox) and main
  exits. So **EOF tears down an in-flight turn** — a steerable child must keep
  stdin open.

## Event schema mismatch: `--mode json` vs `--mode rpc`

The parent's pump parses `--mode json` lines with
`jsonEvent` (`spawner.go:383-393`; tags `type`,`agent`,`role`,`delta`,`content`,
`tool_name`,`tool_input`,`session_id` — **no error field**). `--mode json` is
produced by `cli.go:1794-1839`:

```
{"type":"message_start","agent":…,"role":"model","session_id":…}
{"type":"thinking_delta","agent":…,"delta":…}
{"type":"text_delta","agent":…,"delta":…}
{"type":"tool_call","agent":…,"tool_name":…,"tool_input":{…}}
{"type":"tool_result","agent":…,"tool_name":…,"content":"<json string>"}
{"type":"error","agent":…,"error":…}
{"type":"message_end"}
```

`--mode rpc` emits a different shape (`rpc.go:273-274,344-363,385-393,513-521`):

```
{"type":"response","id":…,"command":…,"success":…}
{"type":"agent_start"}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":…}}
{"type":"tool_execution_start","toolCallId":…,"toolName":…,"args":{…}}
{"type":"tool_execution_end","toolCallId":…,"result":…,"isError":false}
{"type":"agent_end"}
{"type":"agent_settled"}
```

**Feeding rpc lines to the current pump silently degrades:** every line is valid
JSON so nothing crashes, but `message_update` loses its text (nested under
`assistantMessageEvent`; there is no top-level `delta`), `tool_execution_start`
loses the tool name (`toolName` ≠ tag `tool_name`) and args (no `args` field),
and `readChildLines` accumulates results **only** on the `text_delta` branch
(`spawner.go:305,339-341`) — so the child's result would always be `""`. There is
also no `error` event type in rpc mode: `emitError` renders failures as assistant
text (`emitDelta("text_delta", "\n\nError: "+err.Error()+"\n")`, `rpc.go:395-402`).

Adopting rpc for steerable pi children therefore requires a translation layer
(rpc line → `subagent.Event`), not just a flag swap.

## ACP second-prompt surface

- Method: `session/prompt` — `acp.ClientSideConnection.Prompt(ctx, acp.PromptRequest{SessionId, Prompt})`
  (`third_party/acp-go-sdk/client_gen.go:291-299`). The SDK's `SendRequest`
  allocates a fresh pending id each call (`connection.go:629-663`); there is **no
  one-prompt-per-connection restriction**.
- `session/load` / `session/resume` exist in the SDK
  (`constants_gen.go:32,35`; `client_gen.go:283`) and pi's own ACP server
  advertises `LoadSession: true` + `SessionCapabilities.Resume`
  (`internal/acp/server/agent.go:134-146`), but the client never calls them.
- Missing pieces for reuse: `RunningSession` must retain `conn` + stdin as
  `io.WriteCloser` (today `stdin` is stored as `io.Closer`, `session.go:20`),
  stop closing stdin at `:191` and in the `:210` defer, stop calling `cmd.Wait()`
  after the first prompt, and expose the session id (today cached in unexported
  `curSession`/`result.SessionID`, `:36,:239-244`).
- `Cancel()` does **not** send `session/cancel`; it calls `cmd.Process.Kill()`
  (`:99-111`).

## Codex second-turn / steer surface

- `turn/start` requires only `{threadId, input}` — already implemented at
  `internal/codex/session.go:173-176`, so a second turn on the same thread is a
  first-class request.
- A dedicated mid-turn steering method exists: `turn/steer` with
  `TurnSteerParams{threadId, expectedTurnId ("Required active turn id
  precondition"), input[], clientUserMessageId?}`. Confirmed against codex-cli
  0.155.0 (`codex app-server generate-json-schema` → `ClientRequest.json`, 102
  client methods incl. `turn/steer`, `turn/interrupt`, `thread/resume`,
  `thread/fork`, `thread/inject_items`, `thread/read`, `thread/turns/list`).
- Missing pieces: `Session.finish` → `client.close()` kills the process group
  (`session.go:586`, `client.go:349-353`); `threadID`/`client` are unexported;
  `loop` tracks one `turnID` (`:72,:242-246`) and treats a `turn/completed` from a
  different thread id as progress (`:385-388`), so a second turn would be
  misattributed; `thread/start` uses `Ephemeral: true`, which interacts with any
  `thread/resume` approach.

## ACP mock (for hermetic tests)

`cmd/pi-acp-mock/main.go` — `Prompt` (`:161-217`) is **callable repeatedly on one
connection**; each call appends to a `mockSession` transcript. `Initialize`
advertises `LoadSession: true`, `SessionCapabilities.List`, `PromptCapabilities.
EmbeddedContext` (`:86-98`). `LoadSession` replays stored transcript
(`:134-159`) — **but has a nil-pointer bug**: `chunks := append([]mockChunk(nil),
s.transcript...)` at `:137` dereferences `s` before the nil check at `:139`.
`ResumeSession` (`:130-132`) is a no-op that does not bind the session. `Cancel`
(`:256-258`) is a no-op that does not interrupt an in-flight `Prompt`. Env knobs:
`PI_MOCK_RESPONSE`, `PI_MOCK_DELAY_MS`, `PI_MOCK_TOOLS`, `PI_MOCK_TOOLS_FAIL`,
`PI_MOCK_THOUGHTS`, `PI_MOCK_COMMANDS`, `PI_MOCK_ECHO_RESOURCE` (`:11-26`).
