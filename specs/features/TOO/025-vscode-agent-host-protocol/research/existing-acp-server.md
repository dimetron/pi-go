# pi-go ACP Server — Research Findings

Module `github.com/dimetron/pi-go`, Go 1.27. SDK: `github.com/coder/acp-go-sdk v0.13.5` (go.mod). Server implements `acp.Agent`; ADK v2 (`google.golang.org/adk/v2`) is the internal agent runtime.

## Package layout — `internal/acp/server/`
| File | Role |
|---|---|
| `agent.go` (547 ln) | `Agent` — implements `acp.Agent`; session map, prompt bridge, session/list |
| `session.go` (77 ln) | `Serve`/`ServeConfig` — stdio serving loop |
| `runtime.go` (648 ln) | `RuntimeConfig`, `NewPromptHandler` — composes the pi agent per ACP session |
| `store.go` (120 ln) | `SessionStore` interface + `FileSessionStore` over `pisession.FileService` |
| `replay.go` (118 ln) | `replayEvents` — transcript → ACP updates for `session/load` |
| `load.go` (39 ln) | `Agent.LoadSession` (satisfies `acp.AgentLoader`) |
| `cancel.go` (35 ln) | `Agent.Cancel` — routes cancel notification to in-flight turn |
| `commands.go` (35 ln) | `DiscoverAvailableCommands` — skills+subagents for a cwd |
| `adapter/` | `stream.go` (text/thought chunks), `toolcall.go` (tool-call cards), `thinking.go`, `commands.go`, bash output via `OnBashOutput` |

A separate older package `internal/acp` (`types.go`, `toolcall.go`, `permissions.go`) is the **local client-side** ACP model (RunRequest/Event/RunResult for driving external agents like claude/cursor/gemini via `internal/acp/client/`) — distinct from `internal/acp/server`, though `replay.go`/`toolcall.go` import it for `EnrichToolCallTitle`/`ToolKind`.

## Exported types / signatures
**agent.go**
- `type PromptHandler func(ctx context.Context, turn PromptTurn) (PromptResult, error)` (agent.go:29)
- `type PromptTurn struct { SessionID, CWD, Prompt string; Updater SessionUpdater }` (agent.go:32)
- `type PromptResult struct { FinalText string; StopReason acp.StopReason }` (agent.go:40)
- `type SessionUpdater interface { Update(ctx context.Context, update acp.SessionUpdate) error }` (agent.go:48)
- `type Agent struct { AgentInfo acp.Implementation; Handler PromptHandler; AvailableCommandsResolver func(cwd string) []acp.AvailableCommand; Skills []extension.Skill; Subagents []subagent.AgentConfig; Logger *slog.Logger; Sessions SessionStore; conn *acp.AgentSideConnection; sessions map[string]*sessionState }` (agent.go:54–75); `sessionState{cwd, cancel, commandsSent, commandsPending}` (agent.go:77)
- `var _ acp.Agent = (*Agent)(nil)` (agent.go:84); `var _ acp.AgentLoader = (*Agent)(nil)` (load.go:13)
- Methods: `SetAgentConnection` (86), `Authenticate`/`Logout` = no-op stubs (122, 128), `Initialize` (136), `NewSession` (160), `Prompt` (189), `ListSessions` (292), `CloseSession` (325), `ResumeSession` (339), `bindSession` (353), `SetSessionConfigOption` (394), `SetSessionMode` (399), `EchoPromptHandler` (407), `sendAvailableCommands` (488)

**session.go**
- `type ServeConfig struct { Agent *Agent; In io.Reader; Out io.Writer; Logger *slog.Logger }` (17)
- `func Serve(ctx context.Context, cfg ServeConfig) error` (31) — builds `acp.NewAgentSideConnection(cfg.Agent, cfg.Out, cfg.In)`, calls `Agent.SetAgentConnection`, blocks on `conn.Done()` (peer disconnect → nil) vs `ctx.Done()` (SIGINT → ctx.Err()). Pure stdio JSON-RPC via SDK; no socket mode. Does not close In/Out ("callers own the streams", session.go:29).

**store.go**
- `type SessionSummary struct { ID, Cwd, Title string; UpdatedAt time.Time }` (18)
- `type SessionStore interface { Exists(ctx, sessionID) bool; Replay(ctx, sessionID, updater SessionUpdater) error; List(ctx) ([]SessionSummary, error) }` (31)
- `func StoreSessionID(id string) string` (52) — safe ids pass through (so `pi --session <id>` reopens ACP transcripts); hostile ids become `acp-`+sha256[:16]
- `FileSessionStore` wraps `*pisession.FileService`; `Replay` re-emits stored events; `List` via `svc.ListMeta(piagent.AppName, piagent.DefaultUserID)`

**runtime.go**
- `type RuntimeConfig struct { Model, BaseURL string; Headers []string; Insecure bool; System string; LoadConfig func() (config.Config, error); SandboxRootFunc func(PromptTurn) string; SessionService adksession.Service }` (41)
- `type piSessionState struct { mu sync.Mutex; agent *piagent.Agent; sessionID string; streamProxy *streamProxy; bashSup *tools.BashSupervisor; cleanup func() }` (60)
- `func NewPromptHandler(rt RuntimeConfig) PromptHandler` (119) — per-ACP-session cache of `piSessionState`; `ps.mu` serializes turns
- Helpers: `initPiSessionState` (154), `runPromptTurn` (531), `installAutoCompact` (237), `openPiSession` (268), `buildSessionResources` (431), `buildToolsetsFromCfg`/`buildMCPToolsetsFromCfg` (613/624)

**adapter/stream.go**
- `type Stream struct { updater SessionUpdater; mu; finalText strings.Builder; toolCalls map[string]*callState; subagentID string; nextCallSeq int; dedup agent.StreamDedup }` (39)
- `func New(u SessionUpdater) *Stream` (54); `func (s *Stream) OnEvent(ctx, ev *adksession.Event) error` (72); `OnBashOutput` (160) re-emits live shell output as `agent_thought_chunk`; `Final() string` (178)

**adapter/toolcall.go**
- `func (s *Stream) OnToolStart(ctx, name, args) (callID string, err error)` (87); `OnToolEnd(ctx, callID, args, result, runErr) error` (149); `func ToolKind(name string) acp.ToolKind` (46) — read/ls→Read, grep/find/glob→Search, edit/write→Edit, bash/shell→Execute, subagent/agent→Think

## CLI wiring — `pi acp-server`
- `internal/cli/acp_server.go:18` `newACPServerCmd()` — cobra `Use: "acp-server"`, `Args: cobra.NoArgs`, registered at `internal/cli/cli.go:271` (`cmd.AddCommand(newACPServerCmd())`).
- Flags (acp_server.go:29–35): `--model` (default `""`, falls back to `glm-5.2:cloud` at :63–66), `--url`, `--header` (repeatable `key=value`), `--insecure`. `System: flagSystem` at :76 references a `flagSystem` var defined elsewhere in package cli.
- `runACPServer` (:39): `signal.NotifyContext(ctx, os.Interrupt)`; error log at `$HOME/.pi-go/sessions/acp-server.err.log` (custom `logHandler`, time/level/message only); opens store via `openServerSessionStore` (internal/cli/server_sessions.go:19 — non-fatal on failure, nil ⇒ in-memory); builds `RuntimeConfig`; constructs `&acpserver.Agent{AgentInfo: {pi-go, Version}, AvailableCommandsResolver: acpserver.DiscoverAvailableCommands, Handler: acpserver.NewPromptHandler(rt), Logger, Sessions: serverSessionStore(sessionSvc)}`; then `acpserver.Serve(ctx, ServeConfig{Agent, In: os.Stdin, Out: os.Stdout})`.

## Runtime composition (`runtime.go` initPiSessionState, 154–223)
Per ACP session, lazily on first prompt:
1. `cwd := turn.CWD` (fallback `os.Getwd` via injectable `getwd`, :38).
2. `loadSessionConfig` (:286) — `config.LoadFrom(cwd)`, override `cfg.Roles["default"].Model = rt.Model`.
3. `buildSessionLLM` (:313) — role resolution → provider info + baseURL (flag wins), `checkProviderCredentials` (:355), Ollama endpoint resolution/health (:366), `provider.NewLLM` with merged headers, `InsecureSkipTLS`, rate limits (shared budget with CLI); wrapped in `guardrail.WrapModel` with `guardrail.Tracker` sized for auto-compact.
4. `buildSessionResources` (:431): `tools.NewSandbox(sandboxRoot, $PI_WORKTREE_ROOT)` + extra dir `$HOME/.pi-go` (:443); `tools.NewBashSupervisor` + CoreTools/BashControlTools; `newSessionOrchestrator` (:515) — `subagent.DiscoverAgents(cwd, ScopeBoth)` → `subagent.NewOrchestrator` (git-root via `gitroot.Detect`); `lsp.NewManager(nil)`; `tools.AgentTools(orch,…)`; `tools.LSPTools(lspMgr)`; extension hook callbacks; `streamProxy`-backed tool-call Before/After callbacks; `lsp.BuildLSPAfterToolCallback`; `extension.BuildReadImageCallback` as BeforeModelCallback.
   - `cleanup` (:461): `bashSup.KillAll()`, `lspMgr.Shutdown()`, `orch.Shutdown()`, `sandbox.Close()` — only invoked on init failure (:203/:209); **no per-session disposal hook on `CloseSession`** (only cancels in-flight prompts + deletes map entry, agent.go:325–333).
5. `piagent.New(piagent.Config{Model, Tools, Toolsets: buildToolsetsFromCfg(cfg), Instruction, SessionService: rt.SessionService, WorkingDir, BeforeToolCallbacks, AfterToolCallbacks, BeforeModelCallbacks})` (:191).
6. `openPiSession` (:268): with a `SessionService`, `ag.OpenSession(ctx, StoreSessionID(acpSessionID))` (resumes persisted transcript under the ACP id); without, `ag.CreateSession` (in-memory).
7. `installAutoCompact` (:237): asserts `rt.SessionService.(*pisession.FileService)`; `autocompact.BuildHook` → `ag.SetPreTurnHook(hook)`; nil for in-memory or un-windowed tracker.
8. Toolsets (:613–641): MCP servers from `cfg.MCP` → `extension.BuildMCPToolsets`, plus `tools.NewLLMSCachedToolset(cfg.LLMSSources())` for `fetch_docs`.

**Turn loop** — `Agent.Prompt` (agent.go:189) → `NewPromptHandler` closure (runtime.go:123): lookup/create `piSessionState` → `ps.mu.Lock()` (serializes turns) → `stream := adapter.New(turn.Updater)` (fresh per turn, isolates tool-call ids) → `ps.beginTurnStream` (runtime.go:105) swaps `streamProxy`, sets bash-output sink → `runPromptTurn` (:531): `piagent.WithRetry(DefaultRetryConfig, func() iter.Seq2[*adksession.Event, error] { return ps.agent.RunStreaming(ctx, ps.sessionID, turn.Prompt) })`, feeding each event into `stream.OnEvent`; returns `PromptResult{FinalText: stream.Final(), StopReason}` (`StopReasonCancelled` if `ctx.Err() != nil`; blank stop → `StopReasonEndTurn` at agent.go:278–280).

## Event flow ADK → ACP
`piagent.Agent.RunStreaming` (ADK events) → `Stream.OnEvent` (adapter/stream.go:72):
- user-role events ignored; nil content no-op.
- Text parts → `agent_message_chunk` (`acp.UpdateAgentMessageText`), accumulated into `finalText`; dedup via `agent.StreamDedup` drops the aggregate re-send of already-streamed deltas (stream.go:47).
- `part.Thought` parts → `agent_thought_chunk` (`emitThought`, adapter/thinking.go:19; empty dropped; thought-ness per-part not per-role).
- FunctionCall/FunctionResponse parts are **skipped** in OnEvent — tool-call events flow through `extension.BuildToolCallCallbacks(streamProxy)` → `streamProxy.OnToolStart/OnToolEnd` → `Stream.OnToolStart` emits `acp.StartToolCall(id, title, kind, InProgress, RawInput, Locations)`; `OnToolEnd` emits `acp.UpdateToolCall(..., Completed|Failed, RawOutput, Content)`.
- Sub-agent nesting (toolcall.go:54–56, 94–99): `subagent`/`agent` dispatches mark the parent card; nested calls appended as text lines (`▶ name(args)` / `✓|✗ name(args)`) via `appendToParentLocked` re-sending the whole `WithUpdateContent` collection; single-level nesting. Locks: top-level updates after releasing `mu`; nested updates hold `mu` to preserve ordering (:83–86).
- Bash live output: `BashSupervisor.SetSink` → `Stream.OnBashOutput` → `agent_thought_chunk` (stream.go:158–174).
- Emission: `connectionUpdater` (agent.go:473) wraps `conn.SessionUpdate(ctx, acp.SessionNotification{SessionId, Update})`. Nil updater ⇒ discarded. `Prompt` never re-sends FinalText as a chunk — "avoiding the duplicate-message bug" (agent.go:183–185).

## session/list, load, resume, replay
- Advertised in `Initialize` (:136–151): `LoadSession: true` always; `Resume` always; `List` **only when a SessionStore is set**.
- `ListSessions` (:292): nil store ⇒ `acp.NewMethodNotFound(acp.AgentMethodSessionList)`; else `store.List` newest-first, optional `params.Cwd` filter → `acp.SessionInfo{SessionId, Cwd, Title?, UpdatedAt RFC3339}`.
- `LoadSession` (load.go:21): `bindSession` (agent.go:353) — with a store, unknown ids rejected; without a store **every id accepted** (runtime starts a fresh transcript under that id on first prompt). If persisted and live, `store.Replay` streams the transcript **before** the load response returns.
- Replay (replay.go:23): skips `ev.Partial`; user text → `UpdateUserMessageText`, model text → `UpdateAgentMessageText`, thought → `UpdateAgentThoughtText`; FunctionCall → `acp.StartToolCall(..., WithStartKind(ToolKind(name)), WithStartStatus(Completed), WithStartRawInput)` (id fallback `replay_<seq>`); FunctionResponse → `UpdateToolCall(Completed, RawOutput)` paired by id or `lastCallByName`; unpaired responses dropped.
- `ResumeSession` (:339) binds **without replay** — "the client already shows it and only needs the next prompt" (agent.go:335–338). `bindSession` resets `commandsSent/Pending`, re-sends `AvailableCommandsUpdate`.
- `NewSession` (:160): random `sess_` + 12 hex bytes; `sendAvailableCommands` in a goroutine (5s timeout, dedup flags, retried per-prompt if failed, agent.go:198–200).

## Errors / cancellation
- `Cancel` (cancel.go:14): looks up `sessionState.cancel` (registered per prompt in `Agent.Prompt`, agent.go:202/213–220); no in-flight prompt ⇒ silently dropped ("best-effort per spec"). Cancellation → `StopReasonCancelled` nil error (agent.go:266–273); runtime maps ctx cancel → `{stream.Final(), StopReasonCancelled}` (runtime.go:537–538, 551–552).
- Handler panics recovered in `Prompt` (agent.go:239–252) → `errors.New("handler panicked")`.
- ADK event errors surface via `piagent.EventError(ev)` (runtime.go:544) — "Without this a provider failure ends the turn with StopReasonEndTurn and empty text, and the ACP client shows nothing."
- Retry: `piagent.WithRetry(DefaultRetryConfig, …)` (runtime.go:532–534).
- Serve exits on peer disconnect (nil) or ctx cancel (`ctx.Err()`); CLI wraps as `"acp server: %w"` into `acp-server.err.log` (acp_server.go:93–97). LLM diagnostics never touch stdout.

## Slash commands / permissions
- `DiscoverAvailableCommands` (server/commands.go:16): `extension.LoadSkills(DefaultSkillDirsIn(cwd))` + `subagent.DiscoverAgents(cwd, ScopeBoth)` → `adapter.BuildAvailableCommands` (adapter/commands.go:29): meta `clear`/`compact`/`help` first, then skills, then subagents.
- `internal/acp/permissions.go` `AutoApproveOutcome` is **client-side** (claude/gemini/cursor runners), not part of the server; the server `Agent` implements no `RequestPermission` flow.

## Noted gaps (comments)
- `SetSessionConfigOption` → "not yet supported" (agent.go:393–396); `SetSessionMode` → "not yet supported" (:398–401). Image capability off "until zed-09 wires the provider gate" (:132–134). Runtime-rewrite phase references ("Zed-08/zed-09") in adapter comments. No TODO/FIXME markers in `internal/acp/server/**`.