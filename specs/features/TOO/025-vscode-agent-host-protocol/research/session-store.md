# pi-go Session Store — Research Findings

## 1. Storage layout & event model
Each session is a directory `baseDir/<session-id>/` (`internal/session/store.go:105-112`) containing:
- `meta.json` — metadata (atomic write via temp+rename, `writeFileAtomic` store.go:1034-1064, `writeMeta` :1012)
- `events.jsonl` — one ADK `session.Event` per line, appended (`appendEventToFile` :1062-1078)
- `branches.json` — branch state (`branch.go:14-23`, atomic `saveBranches` branch.go:186)
- `branches/<name>/events.jsonl` — per-branch event copies (branch.go:45-56, 152-167)
- `trajectory.atif.json` — parallel ATIF v1.6 trajectory (`internal/atif/writer.go`, written on every AppendEvent)

**Event type**: no pi-go-specific event-kind enums. Events are ADK's `session.Event` (`google.golang.org/adk/v2@v2.3.0/session/session.go:99-148`), embedding `model.LLMResponse` (`adk/v2/model/llm.go:42-57`). **No discriminator field** — "type" implied by payload shape. Real line shape (from `~/.pi-go/sessions/.../events.jsonl`):
```json
{"Content":{"parts":[{"text":"..."}],"role":"user"},"CitationMetadata":null,
 "ID":"50b0d047-...","Timestamp":"2026-04-20T00:15:52.346146+02:00",
 "InvocationID":"e-3b2f9e6f-...","Branch":"","Author":"user",
 "Actions":{"StateDelta":{},"ArtifactDelta":{},"RequestedToolConfirmations":null,
             "SkipSummarization":false,"TransferToAgent":"","Escalate":false},
 "LongRunningToolIDs":null}
```
Classification by **`Content.role` + part shape** (genai.Part keys: `text` (+`thought`), `functionCall{id,args,name}`, `functionResponse{id,name,response}`) and `Author` (`"user"` / `"pi"`). Roles accepted by converters: `"user"`, `"model"` (aliases `"assistant"`, `"agent"`, `"pi"` — `internal/atif/convert.go:91,99`).

**JSON field shapes** (ADK `session/session.go`): `Event` = `id`, `timestamp`, `invocationId`, `branch,omitempty`, `isolationScope,omitempty`, `author`, `actions`, `longRunningToolIds,omitempty`, `routes,omitempty`, `requestedInput,omitempty`, `output,omitempty`, `nodeInfo,omitempty` + embedded `LLMResponse` (`content,omitempty`, `usageMetadata,omitempty`, `modelVersion,omitempty`, `partial,omitempty`, `turnComplete`, `interrupted`, `errorCode`, `errorMessage`, `finishReason` …).
- `EventActions` (session.go:246-278): `stateDelta`, `artifactDelta` (custom `MarshalJSON` :280 writes `null` vs `{}` via pointer-shadow fields), `requestedToolConfirmations,omitempty`, `skipSummarization,omitempty`, `transferToAgent,omitempty`, `escalate,omitempty`, `compaction,omitempty`.
- **Compaction** (`EventCompaction`, session.go:340-398): `{"startTimestamp":…,"endTimestamp":…,"compactedContent":{...genai.Content...},"excludedEvents":[{"invocationId","timestamp"}]}` — attached to a new Event's `Actions.compaction`; covered events stay in file, prompt assembly skips them.
- **Partial/streaming events are never persisted** — `AppendEvent` returns early on `event.Partial` (store.go:415-417).

pi-go's own types: `Meta` (store.go:48-77), `PlanContext` (:29-34), `AgentContext` (:87-98: `agentID`, `agentType`, `parentSessionID`, `runID`, `specName`, `slice`, `cycle`, `worktree`, `branch`, `status`), `BranchInfo`/`branchState` (branch.go:11-22: `{"active":"main","branches":{"main":{"name":"main","head":139,"parent":null,"forkPoint":0}}}`), `CompactionAction` (compaction.go:34-43: None/Shed/Summarize), `AutoCompactOutcome` (autocompact.go:13), `ShedResult` (compaction_shed.go:16-19).

## 2. Store API — `FileService` (`internal/session/store.go`)
Backs ADK's `session.Service` (`var _ session.Service = (*FileService)(nil)` :1324).

| Purpose | Signature | Location |
|---|---|---|
| constructor | `NewFileService(baseDir string) (*FileService, error)` | store.go:116 |
| ID gen | `GenerateSessionID() string` — `yymmdd-hhmm-xxxxx-xxxxx` (sortable) | store.go:129 |
| create | `Create(ctx, *session.CreateRequest) (*session.CreateResponse, error)` | store.go:150 |
| load | `Get(ctx, *session.GetRequest)` — supports `NumRecentEvents`/`After` → `filteredSession` | store.go:226 |
| list (ADK) | `List(ctx, *session.ListRequest) (*session.ListResponse, error)` — lightweight, no events | store.go:273 |
| list (meta) | `ListMeta(appName, userID string) ([]Meta, error)` — newest-first by UpdatedAt | store.go:321 |
| last | `LastSessionID(appName, userID string) string` — resume/`--continue` | store.go:835 |
| append | `AppendEvent(ctx, session.Session, *session.Event) error` — drops partials, filters `temp:` state keys, updates meta + branch head | store.go:408 |
| delete | `Archive(ctx, *session.DeleteRequest)` / `Delete` — moves to `archive/YYYY/MM/DD/<id>/` | store.go:356, 404 |
| meta setters | `SetSessionModel`, `SetSessionProvider(sessionID, provider, baseURL)`, `SetSessionTitle` (sanitized, cap 200), `SetSessionWorkDir`, `GetSessionTitle` | store.go:599, 633, 660, 686, 752 |
| contexts | `UpdatePlanContext`/`GetPlanContext`, `UpdateAgentContext`/`GetAgentContext` | store.go:774, 821, 791, 807 |
| static readers | `SessionModel(baseDir, id) string`, `SessionBackend(baseDir, id) (provider, baseURL, ok)` — meta.json only | store.go:~1035-1060 |
| compaction | `Compact(sessionID, app, user, Summarizer, CompactConfig)`; `AutoCompact(sessionID, app, user, bodyTokens, windowSize, AutoCompactConfig, Summarizer) (AutoCompactOutcome, error)`; `ClearEvents(...)` | store.go:1139, autocompact.go:60, store.go:1221 |
| tokens | `EstimateTokens(sessionID, app, user) (int, error)` — chars/4 heuristic | store.go:1252 |
| ATIF | `ATIFWriter(sessionID) *atif.Writer` | store.go:587 |
| branching | `CreateBranch`, `SwitchBranch`, `ListBranches`, `ActiveBranch` | branch.go:24, 70, 101, 129 |
| merge | `MergeRemoteSessions(localDir, remoteDir, MergeOptions) (MergeReport, error)` | merge.go:52 |

Internals: `fileSession` (cache entry; LRU at `maxCachedSessions = 20`, :103), `liveSession`/`filteredSession` (store.go:882-930), `rewriteEvents` (atomic JSONL rewrite, :1298), `readEvents` (:1079), `readMeta`/`writeMeta`.

## 3. Session metadata capture
- **Create** (:174-186): `Meta{ID, AppName, UserID, WorkDir: os.Getwd(), Model: UnknownModel ("unknown"), CreatedAt, UpdatedAt, Host: &HostEnv}`. `HostEnv` (hostenv.go:15-33) snapshots os/arch/cpus, RAM, disk of session dir.
- **Agent tree context**: `AgentContextFromEnv()` (agentenv.go:36); `Agent.recordNewSessionMeta` (internal/agent/agent.go:641-672) writes AgentContext, model, workDir, default title (git repo name / cwd basename) via optional recorder interfaces (`modelNamer`, `workDirRecorder`, `titleNamer` — agent.go:580,668).
- **Resume**: `resolveDeferredSession` (internal/cli/interactive.go:605-640) calls `SetSessionModel(sessionID, llm.Name())` + unconditional `SetSessionProvider(sessionID, providerName, baseURL)`; `applyResumedModel` (cli.go:1066-1090) restores via static `pisession.SessionModel(dir, id)`.
- **Title lifecycle**: TUI `applySessionTitle`, print-mode `derivePrintTitle` (cli.go:1569); cap `MaxSessionTitle = 200`, OSC-0-sanitized (`sanitizeSessionTitle` :710, `truncateTitle` :739).
- meta.json rewritten on every `AppendEvent` (updatedAt bump), atomically.

## 4. Session enumeration
- **ACP `session/list`**: `internal/acp/server/agent.go:285-308` → `SessionStore.List` → `FileSessionStore.List` (`internal/acp/server/store.go:93-105`) → **`FileService.ListMeta(piagent.AppName, piagent.DefaultUserID)`** mapped to `SessionSummary{ID, Cwd: m.WorkDir, Title, UpdatedAt}`; optional cwd filter. Advertised only when a store exists (agent.go:135-141); nil store → `MethodNotFound`.
- **ACP `session/load`** (load.go:21-39): `bindSession` + `store().Replay(ctx, sid, updater)`.
- **CLI**: `--continue` → `resolveResumeSession` (cli.go:1041-1058) → `LastSessionID`; TUI listing uses same `ListMeta`. `App = "pi-go"`, `DefaultUserID = "local"` (internal/agent/agent.go:45; confirmed on disk).
- ACP session-ID → disk-ID: `StoreSessionID` (acp/server/store.go:44-52) — safe ids pass through; hostile ids hashed to `acp-<sha256[:16]>`.

## 5. Transcript reconstruction from events
- **ACP history replay** (`internal/acp/server/replay.go`): `replayEvents(ctx, iter.Seq[*session.Event], SessionUpdater)` maps parts → ACP updates: `FunctionCall` → completed `StartToolCall` (id fallback `replay_<n>`, `WithStartStatus(ToolCallStatusCompleted)`, RawInput=Args); `FunctionResponse` → `UpdateToolCall(Completed, RawOutput)` paired by `fr.ID` else last-call-by-name; `Thought` → `AgentThoughtText`; user → `UserMessageText`; else `AgentMessageText`. `Partial` skipped.
- **TUI resume restore** — `internal/tui/session_restore.go:15-77` `restoreTranscript(events) []message`: same part walk; thinking skipped; consecutive assistant text merged; tool results attached to call card by ID. `restoreSession()` (:79-120): `svc.Get(...)` → `Events().All()` → restore + `EstimateTokens`.
- **Print mode**: `runPrint` (cli.go:1604) consumes live events, not disk; resume reconstruction uses the same ADK `Get`+`Events()` path.
- **Compaction record**: normal event, `Author:"pi"`, `Content.role` unset, `Actions.compaction` carries summary; prompt assembly skips covered ranges minus `excludedEvents`.
- **ATIF sidecar**: `internal/atif/writer.go` maintains `trajectory.atif.json` (ATIF-v1.6) incrementally; `AppendEvent` per persisted event, batched `AppendEvents` on load (store.go:576-580); subagent trajectories via `SetSubagentRef`/`LinkSubagentTrajectories` (store.go:461).

## Noteworthy
- **No explicit event-kind enum in the JSONL**: everything is an ADK `session.Event`; message/tool distinctions live in `Content.role` + part shape + `Author`. `"message"/"tool_call"` string kinds found in the repo belong to other logs (codex events, logger, subagent spawner), not `events.jsonl`.
- `AppendEvent` updates `branches.json` head on every append (:470-477); `SwitchBranch` rewrites root `events.jsonl` so ADK reads the active branch (branch.go:100-130).
- Compaction rewrites the whole JSONL atomically via `rewriteEvents` (also `SwitchBranch`); `ShedSupersededToolResults` (compaction_shed.go:41) stubs payloads ≥400 bytes while keeping call/response pairing.