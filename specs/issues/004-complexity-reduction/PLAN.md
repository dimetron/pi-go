# pi-go Complexity Reduction & Quality Improvement Plan

> Synthesized from four exploration reports covering `internal/cli`, `internal/tui`,
> `internal/session` + `internal/agent`, and `internal/provider` + `internal/tools` + `internal/subagent`.
> Scope: the entire `internal/` tree at HEAD. No code changes — execution plan only.

---

## 0. TL;DR

- **Two real data races** in `internal/session/store.go:329-388` (`AppendEvent`) and
  `internal/agent/agent.go:267-283` (`RebuildWithInstruction`). Both reproducible with
  `go test -race`. Phase 1 fixes both.
- **~30 % LoC reduction in `internal/provider`** by collapsing the Anthropic Beta/non-Beta
  fork, unifying OpenAI Completions/Responses content conversion, and extracting
  `extractSystemInstruction` (≈ 45 LOC), `buildFinalResponse` (≈ 250 LOC), and a generic
  `toolCallBuffer[T]` (≈ 70 LOC).
- **`internal/session` collapses from 891 → 3 × ~250 LOC** by splitting the monolithic
  `FileService` into `storage` (JSONL I/O), `policy` (compaction/branching), and the ADK
  adapter. Side effect: the O(N²) cold-load re-append loop disappears and
  `atifWriter.Close()` finally gets called.
- **`WithRetry` becomes context-cancellable**, the hand-rolled JSON escapers in
  `expandChainTemplate` and elsewhere are replaced with `strconv.Quote` / `json.Marshal`.
- **TUI becomes testable**: export `New(Config) (tea.Model, error)`, move `os.Hostname()`,
  `exec.Command("git", …)`, and `auth.SetDebugLogger` behind interfaces, delete the
  73 KB `coverage_boost_test.go` brute-force reflection test in Phase 6.
- **CLI runtime shrinks ~20 %** by extracting `ProviderResolver` (eliminates 3 copies of
  the baseURL ladder), introducing a `Renderer` interface, consolidating ANSI colors,
  replacing the triple-nested `lastLoggedError` reverse loop with a streaming scan, and
  fixing the pprof goroutine that currently makes `go test -race ./internal/cli/...`
  time out at 90 s.
- **Sentinel errors** added throughout `internal/session` and `internal/agent` so callers
  can use `errors.Is` to detect "session not found", "branch already exists", etc.
- **`Orchestrator` decomposed** from 14 fields / 4 mutexes to ~3 collaborators; the 3
  subagent mode handlers collapse behind one `consumeEvents` helper; the dead
  `consumeAgentEvents` is removed.
- **State machines in TUI become typed** (`run.phase`, `commit.phase`, `login.phase`,
  `m.mode`, `message.role`); the 200-line `handleKey` and the 22-arm
  `handleSlashCommand` switch become dispatch tables.

---

## 1. Cross-Cutting Findings

| ID  | Package             | Severity | Hotspot                                                                                                                                                                                       | File:Line                                                                                                           | Why it matters                                                                                                                |
|-----|---------------------|----------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| R1  | `internal/session`  | High     | `AppendEvent` mutates session state under only `s.mu`; live readers use `sess.mu`                                                                                                             | `store.go:329-388`                                                                                                  | Real data race; torn events slice header under load. Reproduce with `-race`.                                                  |
| R2  | `internal/agent`    | High     | `RebuildWithInstruction` swaps `a.runner` without a mutex; `Run` reads it concurrently                                                                                                        | `agent.go:267-283`                                                                                                  | Real data race on `*runner.Runner` pointer; `/plan` triggers this from the TUI message loop.                                  |
| R3  | `internal/provider` | High     | Anthropic Beta path is a near-verbatim copy of non-Beta                                                                                                                                       | `anthropic.go:644-726, 236-274, 805-814`                                                                            | ≈ 150 LOC of mechanical duplication; one source of truth fixes bugs in both.                                                  |
| R4  | `internal/provider` | High     | 8 streaming/non-streaming pairs share identical usage/finish-reason boilerplate                                                                                                               | `anthropic.go:524,594,644,729`; `openai_completions.go:236,286`; `openai_responses.go:242,411`; `ollama.go:291,376` | ≈ 250 LOC collapsible into one `buildFinalResponse` helper.                                                                   |
| R5  | `internal/provider` | High     | `extractSystemInstruction` boilerplate duplicated 4× verbatim                                                                                                                                 | `anthropic.go:288-298`; `ollama.go:115-125`; `openai_completions.go:50-60`; `openai_responses.go:103-114`           | ≈ 44 LOC → 1 helper.                                                                                                          |
| R6  | `internal/provider` | Medium   | 3 different `toolCallAcc` shapes (string, `map[string]any`, typed struct)                                                                                                                     | `anthropic.go:467-471`; `openai_completions.go:169`; `openai_responses.go:210,217-219`                              | ≈ 70 LOC collapsible to one generic `toolCallBuffer[T]`.                                                                      |
| R7  | `internal/provider` | Medium   | `AdvisorModel` / `AdvisorMaxUses` / `AdvisorCaching` on global `LLMOptions` but Anthropic-only                                                                                                | `LLMOptions` (5 fields)                                                                                             | Pollutes every provider's config; should be per-provider options.                                                             |
| R8  | `internal/session`  | High     | `loadSession` re-appends every event to a brand-new `atif.Writer` (O(N) disk writes)                                                                                                          | `store.go:425-436`                                                                                                  | Cold load is O(N²); ATIF writer should be append-only.                                                                        |
| R9  | `internal/session`  | High     | `atifWriter` created on cache miss and `Create` but `Close()` is never called                                                                                                                 | `store.go:541, 443-450`                                                                                             | fd leak per session; `Close()` should be in a `defer` chain.                                                                  |
| R10 | `internal/session`  | Medium   | Zero sentinel errors; all errors are inline `fmt.Errorf`                                                                                                                                      | `store.go:114,334,399,465,481,497,825`; `branch.go:46,95,135`                                                       | Callers can't `errors.Is` for "session not found", "branch exists", etc.                                                      |
| R11 | `internal/agent`    | Medium   | `WithRetry` calls `time.Sleep(delay)` and ignores `context`                                                                                                                                   | `retry.go:127`                                                                                                      | Cancellation is silently dropped mid-retry.                                                                                   |
| R12 | `internal/session`  | Medium   | `Compact` cannot be cancelled; 5 `FileService` methods take `context.Context` and ignore it                                                                                                   | `store.go:98,171,218,264,316, 750-815`                                                                              | Cancellation contract is a lie; SIGINT during compaction has no effect.                                                       |
| R13 | `internal/subagent` | High     | 3 near-identical "consume events and emit" closures                                                                                                                                           | `subagent.go:238-265, 365-397, 507-538`                                                                             | ≈ 60 LOC + dead `consumeAgentEvents` (`subagent.go:602-618`); one `consumeEvents` helper.                                     |
| R14 | `internal/subagent` | Medium   | `expandChainTemplate` hand-rolls a JSON escaper                                                                                                                                               | `subagent.go:579-593`                                                                                               | Missing edge cases (nested quotes, escapes). Use `strconv.Quote`.                                                             |
| R15 | `internal/subagent` | High     | `Orchestrator` god-object: 14 fields, 4 mutexes, 7 responsibilities                                                                                                                           | `orchestrator.go` (645 LOC)                                                                                         | Decompose into `Orchestrator` + `AgentRegistry` + `RecentTaskCache` + `ACPLogger`.                                            |
| R16 | `internal/subagent` | Medium   | `pumpACPSession` mixes 4 concerns behind a stringly-typed "you are subagent" preamble                                                                                                         | `spawner_acp.go:46-48, 130-212`                                                                                     | Substring-completion protocol is fragile; replace with structured `acpResult` type.                                           |
| R17 | `internal/tui`      | High     | `handleKey` is a 200-line if-tree, `handleSlashCommand` is a 22-arm switch, state machines are plain `string` fields                                                                          | `tui.go:519-721`; `commands.go:26-107`; `run.go:40, commit.go:18, login.go:19, tui.go:47, chat.go:95`               | The 7-call-site "mutate trailing message" pattern (see §4 TUI report) becomes a `MessageStore.UpdateTrailing(role, fn)`.      |
| R18 | `internal/tui`      | High     | `Run` is the only constructor; no exported `New(Config) (tea.Model, error)`; `View` calls `os.Hostname()` and `exec.Command("git", …)`; `auth.SetDebugLogger` is a process-global side effect | `tui.go:228-229, 921, 1200, 1247, 1263, 1288, 1325`; `commit.go:61,70,94,163`                                       | TUI is untestable from outside the package; 73 KB `coverage_boost_test.go` exists to brute-force `Update` arms by reflection. |
| R19 | `internal/cli`      | High     | `runNonInteractive` is 308 LOC with 9 phases and ≥ 20 cyclomatic                                                                                                                              | `cli.go:411-718`                                                                                                    | Split along mode boundaries (`output_print.go`, `output_json.go`, `runtime.go`, `dotenv.go`).                                 |
| R20 | `internal/cli`      | Medium   | Provider resolution ladder duplicated 3 ways (baseURL + role + headers)                                                                                                                       | `cli.go:168-279, 1037-1085`; `ping.go:321-341, 558-678, 683-790`                                                    | Extract one `ProviderResolver`.                                                                                               |
| R21 | `internal/cli`      | Medium   | `lastLoggedError` is a triple-nested reverse loop scanning `~/.pi-go/log/` on every invocation                                                                                                | `cli.go:806-856`                                                                                                    | Replace with a streaming read of the active log file with early exit.                                                         |
| R22 | `internal/cli`      | Medium   | Pprof server goroutine spawned in `runRoot` with no lifetime tracking                                                                                                                         | `cli.go:281-336, 290-295`                                                                                           | Wrap pprof in a guarded helper that owns its own `Shutdown(ctx)`.                                                             |

---

## 2. Phased Roadmap

### Phase 1 — Stop the bleeding: concurrency safety & shared helpers

**Goal.** Eliminate the two real data races and the most dangerous code smells in
`internal/session`, `internal/agent`, `internal/provider`, and `internal/subagent`.
Each step is small, surgical, and tested.

**Prerequisites.** None — this is the entry point.

**Tasks.**

1. **Fix the `AppendEvent` race.** Acquire `sess.mu` after `s.mu` in
   `FileService.AppendEvent` (`store.go:329-388`) and audit every other method that
   mutates `*fileSession` to follow the same `s.mu → sess.mu` order. Add a test that
   hammers `AppendEvent` + `Events()` + `State()` from multiple goroutines.
2. **Fix the `RebuildWithInstruction` race.** Add `a.runnerMu sync.RWMutex` to
   `Agent`; guard the swap in `RebuildWithInstruction` (`agent.go:267-283`) and reads
   in `Run`/`RunStreaming` (`agent.go:315, 321`). Add a test that interleaves `Run`
   and `RebuildWithInstruction`.
3. **Make `WithRetry` respect context.** Replace `time.Sleep(delay)` (`retry.go:127`)
   with `select { case <-ctx.Done(): …; case <-time.After(delay): }`. Wire `WithRetry`
   into `Agent.Run` so callers don't have to wrap manually.
4. **Close `atifWriter`.** Add `Close()` calls (and a `defer`) at every site that
   creates an `*atif.Writer` in `FileService` (`store.go:443-450, 541`). Add a test
   that verifies no fd leak across `Create`/`Delete` cycles.
5. **Replace hand-rolled JSON escapers.** Swap `expandChainTemplate`
   (`subagent.go:579-593`) and any other sites that build JSON by string concatenation
   to use `strconv.Quote` (for single-string fields) or `json.Marshal` (for struct
   values).
6. **Extract `extractSystemInstruction(cfg) string`.** Delete the 4× duplicated
   11-line block at `anthropic.go:288-298`, `ollama.go:115-125`,
   `openai_completions.go:50-60`, `openai_responses.go:103-114`.
7. **Extract `buildFinalResponse(...) (*genai.GenerateContentResponse, error)`.**
   Delete the duplicated usage/finish-reason boilerplate from the 8
   streaming/non-streaming pairs listed in R4.
8. **Extract `toolCallBuffer[T]` generic.** Replace `antToolUseAcc`
   (`anthropic.go:467-471`), `oaiStreamState.toolCalls` (`openai_completions.go:169`),
   and `responsesStreamState.toolCalls` (`openai_responses.go:210`) with one typed
   accumulator.
9. **Add sentinel errors.** Introduce `var ErrSessionNotFound = errors.New(...)`,
   `ErrSessionExists`, `ErrBranchNotFound`, `ErrBranchExists` in `internal/session`
   and use them at `store.go:114, 334`, `branch.go:46, 95`. Add an
   `agent.ErrRebuildInProgress` if needed.
10. **Accept `context.Context` in `Compact` and respect cancellation** in the 5
    `FileService` methods that currently take `ctx` and ignore it
    (`store.go:98, 171, 218, 264, 316, 750-815`).

**Verification.**

```bash
go test -race ./internal/session/... ./internal/agent/... ./internal/subagent/... ./internal/provider/...
go vet ./...
golangci-lint run
```

New tests for: race in `AppendEvent` + `Events()`, race in `RebuildWithInstruction` +
`Run`, `WithRetry` cancellation, ATIF fd leak, `json.Marshal` round-trip parity for
`expandChainTemplate`.

**Risk.** Low. Each task is mechanical and localized; the only cross-cutting risk is
the lock-ordering fix in #1, which can mask existing deadlocks if the existing
`s.mu → sess.mu` callers at `store.go:460+467, 476+483` are not all reviewed.
**Mitigation:** add a code comment specifying the lock order and audit every existing
call site.

**Estimated impact.** ~150 LOC deleted in `internal/provider` (this phase only); 2
data races eliminated; `WithRetry` becomes correct; ATIF no longer leaks.
**Effort: S.** **Public API change: no.**

---

### Phase 2 — Provider unification

**Goal.** Collapse the Beta/non-Beta and Completions/Responses forks in
`internal/provider`; move advisor fields to per-provider options; net ~30 % LoC
reduction in the package.

**Prerequisites.** Phase 1 (helpers `extractSystemInstruction` and
`buildFinalResponse` exist).

**Tasks.**

1. **Unify Anthropic Beta and non-Beta paths.** Delete `antRunStreamingBeta`
   (`anthropic.go:644-726`) and `antRunNonStreamingBeta`; keep one streaming + one
   non-streaming path parameterized by a `beta bool` flag (or by a tiny `antConfig`
   struct). Delete `antGenaiToolsToBetaAnthropic` (`anthropic.go:236-274`) and
   `antStopReasonToGenaiBeta` (`anthropic.go:805-814`) by promoting the union of their
   behaviors to a single function. Add `antThinkingConfig` and `antThinkingConfigBeta`
   to the same struct.
2. **Unify OpenAI Completions/Responses content conversion.** Factor a shared
   `oaiContentBlocksToText(c genai.Content) (string, []genai.Part)` (or whatever the
   canonical projection is) that both `oaiContentsToMessages`
   (`openai_completions.go:50-60`) and `oaiContentsToResponsesInput`
   (`openai_responses.go:103-114`) call. Verify `mistral.go:54` (which already reuses
   `oaiContentsToMessages`) still works.
3. **Move `AdvisorModel` / `AdvisorMaxUses` / `AdvisorCaching` to per-provider
   options.** Replace the fields on the global `LLMOptions` with an
   `AnthropicOptions{AdvisorModel, AdvisorMaxUses, AdvisorCaching}` struct that the
   Anthropic provider reads and other providers ignore. Update the 1–2 call sites in
   `internal/cli` and `internal/tui`.
4. **Consolidate token accounting.** Replace the 8 `int64 → int32` casting sites
   with one `safeInt32(int64) int32` helper.
5. **Consolidate the `oaiFunctionResponseContent` reuse** by promoting it to a
   single shared function (it's already shared; just make sure it's not re-implemented
   in the Responses path).
6. **Convert the 3 finish-reason mappers** (`oaiFinishReasonToGenai`,
   `ollamaFinishReasonToGenai` / `mistralFinishReasonToGenai`,
   `antStopReasonToGenai` / `antStopReasonToGenaiBeta`) into one shared mapper
   parameterized by provider enum.

**Verification.**

```bash
go test ./internal/provider/...
go test -race ./internal/provider/...
go test ./...   # full regression — provider changes are high-blast-radius
```

New table-driven tests for: streaming+tool-calls parity between Beta and non-Beta
Anthropic, Responses vs Completions on a fixed prompt set, advisor-only-on-Anthropic
behavior.

**Risk.** Medium. Provider behavior differences (Beta header, Responses input shape)
are subtle; regressions in streaming could go unnoticed without a real model in the
loop. **Mitigation:** keep one provider-level integration test per provider
(`-tags=integration`) gated by an env var; preserve golden trace files for the Beta
path; record the prompt→output bytes for the Responses path.

**Estimated impact.** ~800 LOC deleted in `internal/provider` (~30 % of the ~3 500
LoC of provider glue). **Effort: M.** **Public API change: yes** (`LLMOptions` loses 3
fields; `AnthropicOptions` is added). **Mitigation:** keep the 3 fields on
`LLMOptions` as deprecated pass-throughs for one minor version.

---

### Phase 3 — Session/Agent cleanup

**Goal.** Split `FileService` (891 LOC) into focused sub-packages, decouple ATIF,
fix the O(N²) cold-load, and turn `LoadInstruction` into its own prompt package.

**Prerequisites.** Phase 1 (sentinel errors, `atifWriter.Close()`).

**Tasks.**

1. **Split `FileService` into three sub-packages:**
    - `internal/session/storage`: pure JSONL I/O (`appendEventToFile`,
      `rewriteEvents`, meta read/write, ID gen). No ADK dependency, no ATIF
      dependency.
    - `internal/session/policy`: compaction, branching, plan-context CRUD,
      summarizer dispatch.
    - `internal/session` (kept): ADK adapter that wires `storage` + `policy` +
      `*atif.Writer` and implements `session.Service`. Roughly 200 LOC.
2. **Make ATIF writer append-only.** Drop the per-event rewrite path in
   `atif/writer.go:56-69`. `loadSession` (`store.go:425-436`) no longer re-appends
   every event to a fresh writer — it opens in append mode and only writes the final
   state on `Close()`. This eliminates the O(N²) cold load.
3. **Add `Close()` to `FileService`.** Walk the `fileSession` cache, close every
   `*atif.Writer`, and `Close()` the cache itself. Wire it into a top-level
   `defer fs.Close()` at the call sites in `internal/cli/runRoot` and
   `internal/tui/run.go`.
4. **Decouple `fileSession` from `*atif.Writer`.** Pass the writer in/out of methods
   instead of stashing it on the struct. This makes `fileSession` mockable and
   removes the `store.go:535-542` coupling.
5. **Move `LoadInstruction` to a new `internal/prompt` package.** It currently lives
   at `agent.go:332-365` and pulls in `internal/extension` for skill discovery. The
   new package owns: `AGENTS.md` reading, skill discovery, instruction assembly.
   `Agent.New` calls `prompt.Build(cfg.InstructionDir, cfg.Skills)` and stores the
   result.
6. **Make `Agent.Config` smaller.** Move the 4 callback slices into a separate
   `Callbacks` struct (or into a typed `Lifecycle` interface). `Config` should not
   need to be retained on `Agent` once `New` returns; rebuilds can re-derive from
   the canonical `instructionDir` + `skills`.
7. **Make `Compact` cancellable** (carry-over from Phase 1 #10). `Compact(ctx, …)`
   checks `ctx.Err()` between events and returns `ctx.Err()` if cancelled.

**Verification.**

```bash
go test -race ./internal/session/...
go test -race ./internal/agent/...
go test ./internal/prompt/...
```

New benchmarks: `BenchmarkFileService_Load` (cold), `BenchmarkFileService_Append`
(warm). Cold-load should drop from O(N²) to O(N). New test: `TestFileService_Close`
verifies no fd leak after `Close()`.

**Risk.** Medium. The split touches many call sites; ADK adapter signatures change.
**Mitigation:** keep the public `session.Service` interface stable; introduce the
sub-packages in a sub-tree first (`internal/session/v2/`) and re-export from
`internal/session` for one release.

**Estimated impact.** ~600 LOC deleted from `internal/session`; `internal/prompt` is
~120 LOC new; cold-load time goes from O(N²) to O(N); ATIF fd leak fixed.
**Effort: M.** **Public API change: no** (only internal package layout).

---

### Phase 4 — TUI decomposition

**Goal.** Make the root Bubble Tea model testable and decomposed; replace
stringly-typed state machines with typed enums; collapse the 200-line `handleKey`
and 22-arm `handleSlashCommand`.

**Prerequisites.** Phase 1 (the `MessageStore` helper introduced in §TUI report R17
should be a clean follow-up here).

**Tasks.**

1. **Promote state machines to typed enums.** Define
   `type runPhase int; const (runPhaseRunning runPhase = iota; runPhaseGating; …)`
   in `internal/tui/run.go`. Same for `commitPhase`, `loginPhase`, `mode`,
   `message.role`, `ev.Type`, `ev.kind`. Add `String()` methods for rendering.
2. **Split `handleKey` (`tui.go:519-721`) into per-mode handlers.** Each handler is
   a `func(msg tea.KeyMsg) (tea.Model, tea.Cmd)` that takes `*rootModel` and
   returns the next state. Top-level `handleKey` becomes a switch on `m.mode` (typed)
   and a `m.run.phase` / `m.commit.phase` / `m.login.phase` switch inside each mode.
3. **Extract `MessageStore` type.** Owns `[]message`; exposes `Append(m message)`,
   `UpdateTrailing(role messageRole, fn func(*message))`,
   `Find(role messageRole) *message`. Replace the 7-site back-walk pattern at
   `agent_loop.go:415-431, 596-601, 622-644`; `run.go:415-420, 449-454`;
   `tui.go:483-487, 1775-1778`.
4. **Replace `handleSlashCommand` switch (`commands.go:26-107`) with
   `map[string]func(tea.Model, …)`.** Each `/foo` handler becomes a method on a
   `command` struct. The dispatcher is a 3-line map lookup.
5. **Replace `formatToolResult`'s 12-arm if-else (`tool_display.go:439-578`) with
   `map[string]func(map[string]any) string`.** Each tool's renderer is a registered
   function; the dispatcher is a 3-line lookup.
6. **Decompose `Orchestrator` (`subagent/orchestrator.go`, 14 fields / 4 mutexes).**
   Split into:
    - `Orchestrator` (lifecycle + spawn)
    - `AgentRegistry` (lookup)
    - `RecentTaskCache` (status counts)
    - `ACPLogger` (acp session logs)

   Each has one mutex.
7. **Unify the 3 subagent mode handlers** behind one
   `consumeEvents(ctx, source, sink)`. Delete the dead `consumeAgentEvents`
   (`subagent.go:602-618`).
8. **Move OS / git calls behind interfaces.** Define
   `type GitRunner interface { Status(ctx) (string, error); Diff(ctx, ref string) (string, error); ... }`
   and `type Env interface { Hostname() string; Getenv(string) string }`. The
   default implementations wrap `exec.Command` and `os.Hostname` / `os.Getenv`.
   Inject via `Config`.
9. **Decouple `auth.SetDebugLogger` from TUI state** (`tui.go:228-229`). Pass the
   logger into the auth client at construction time.
10. **Replace `countAgentsByStatus` inline duplication** at `commands.go:231-244` and
    `472-486` with a method on `RecentTaskCache`.
11. **Add an `if log != nil { log.Info(...) }` helper** —
    `logInfo(log, "msg", k, v)` — to remove the 20+ nil-check sites.

**Verification.**

```bash
go test ./internal/tui/...
go test -race ./internal/tui/...
go test -race ./internal/subagent/...
```

New tests: state-machine enum tests (every transition has a defined next),
`MessageStore.UpdateTrailing` tests, `handleKey` dispatch tests per mode. New fuzz
test for `expandChainTemplate` (carry-over from Phase 1 #5).

**Risk.** High. The TUI is the most-coupled file in the repo; one wrong
message-type rename cascades through 21 custom `tea.Msg` types. **Mitigation:** do
the state-machine enum conversion first and verify all callers with `go vet`;
introduce `MessageStore` and `GitRunner` / `Env` interfaces as seams, then refactor
callers file by file.

**Estimated impact.** ~500 LOC deleted in `internal/tui`; ~150 LOC deleted in
`internal/subagent`; TUI becomes unit-testable; `coverage_boost_test.go` can be
deleted (deferred to Phase 6). **Effort: L.** **Public API change: no** (TUI is
internal).

---

### Phase 5 — CLI runtime cleanup

**Goal.** Split `cli.go` along mode boundaries, share the provider resolution
pipeline, and unblock `go test -race ./internal/cli/...` (currently times out at
90 s).

**Prerequisites.** Phase 1 (`ProviderResolver` is a natural extraction of the
duplication this phase addresses); Phase 2 (Anthropic advisor fields live on
`AnthropicOptions`).

**Tasks.**

1. **Split `cli.go` along mode boundaries** into:
    - `runtime.go` — `buildRootRuntime`, `buildCommitMsgFunc`, `ProviderResolver`
    - `output_print.go` — `runPrint` + format helpers
    - `output_json.go` — `runJSON` + JSON sink
    - `dotenv.go` — `LoadDotEnv`, `loadDotEnvFile`, `mergeExtraHeaders`
    - `session_log_scan.go` — `lastLoggedError`
    - `provider_setup.go` — baseURL ladder

   `cli.go` keeps only `Execute`, `RunPing`, and the `cobra.Command` tree.
2. **Extract `ProviderResolver`.** Used by `buildRootRuntime`
   (`cli.go:168-279`), `buildCommitMsgFunc` (`cli.go:1037-1085`), `runPing`
   (`ping.go:111-517`). One source of truth for: baseURL, role, auth headers,
   advisor options. Each of the 3 call sites goes from ~100 LOC to ~10 LOC.
3. **Introduce `Renderer` interface** for `runPrint` (`cli.go:902-940`) and
   `runJSON` (`cli.go:957-1033`). Shared scaffold is
   `consumeAgentEvents(ctx, agent, renderer) error`; only the renderer differs.
4. **Extract non-stream/stream model test loop** for `modelPing`
   (`ping.go:558-678`) and `ollamaPingFull` (`ping.go:683-790`). One
   `func pingModel(ctx, model, stream bool) (*PingResult, error)`.
5. **Consolidate ANSI colors.** Move the two definitions (`cli.go:875-880`,
   `ping.go:26-32`) into one `internal/cli/clicolor` package.
6. **Replace `lastLoggedError` triple-nested reverse loop** (`cli.go:806-856`) with
   a streaming read of the active log file: open `~/.pi-go/log/<today>.jsonl`, seek
   to end on the first call, and read new lines on each subsequent call. Early-exit
   on the first error line.
7. **Wrap lazy `MemoryStore` behind one wiring type.** The 3 wiring styles (eager
   `cli.go:430-474`, lazy proxy `interactive.go:420-530`, `excludedTools` map at
   `cli.go:557-583` and `interactive.go:540-586`) collapse to a single
   `type memoryWiring struct { eager bool; lazy bool; excludedTools map[string]bool }`.
8. **Move pprof startup out of `runRoot`** into a guarded `pprofHelper` that owns
   its own `Shutdown(ctx)`. This unblocks `go test -race ./internal/cli/...`
   (currently times out at 90 s due to the orphan goroutine).
9. **Document the `chan tui.InitEvent` protocol** with typed channels — replace the
   9-arg `deferredInit` signature (`interactive.go:115-418`) with a typed
   `deferredInitResult` struct.
10. **Replace `palaceConfigFromCLI` anonymous struct** (`cli.go:1201-1218`) with a
    named `PalaceOptions` in the palace package.
11. **Delete the public-but-unused `LoadDotEnv` wrapper** (`cli.go:1138-1140`); keep
    only `loadDotEnv`.
12. **Unify MCP config translation** in the 3 sites (`interactive.go:218-225,
    748-754`, `cli.go:594-602`) into one
    `func applyMCPConfig(cfg *MCPConfig, opts ...MCPOption)`.

**Verification.**

```bash
go test -race ./internal/cli/...   # must now finish under 30s, not time out at 90s
go test ./internal/cli/...
go vet ./...
golangci-lint run
```

New tests: `ProviderResolver` table-driven, `Renderer` interface contract,
`pprofHelper.Shutdown` goroutine-count test.

**Risk.** Medium. CLI is the public entry point; one wrong `cobra.Command` rename
breaks downstream scripts. **Mitigation:** keep all command names and flags
identical; only refactor internal call structure; run the full `go test ./...`
matrix after every file split.

**Estimated impact.** `cli.go` shrinks from 1 223 LOC to ~300 LOC; `ping.go` shrinks
from 812 to ~400 LOC; test time for `internal/cli/...` drops from 90 s+ to ~10 s.
**Effort: M.** **Public API change: no** (CLI surface preserved).

---

### Phase 6 — Quality gates

**Goal.** Now that the structure is sane, lock in testability and remove the
brute-force tests that exist only because the code was untestable.

**Prerequisites.** Phases 1–5.

**Tasks.**

1. **Export `tui.New(Config) (tea.Model, error)`.** Replace `Run(ctx, cfg)` as the
   test entry point. Add a fake `GitRunner` and `Env` in test files; add
   table-driven tests for `Update` on each `tea.Msg` type and `View` snapshots.
2. **Delete `coverage_boost_test.go` and `coverage_target_test.go`.** With the
   decomposition done, real tests should cover the same surface area without
   reflection. Verify with `go test -cover ./internal/tui/...` that coverage is not
   worse.
3. **Make `lastLoggedError` a true streaming read.** Persist the last-read offset
   in a small state file (`~/.pi-go/last_log_offset`) so subsequent invocations
   don't re-read the whole day's log. Replace the in-memory `~/.pi-go/log` scan.
4. **Add benchmark gates** to `internal/provider` for streaming throughput
   (tokens/s) and `internal/session` for cold load (ms per 1k events). Fail CI if a
   10 % regression is detected.
5. **Run `golangci-lint run` with the full ruleset** (per `.golangci.yml`). Fix any
   new findings; require zero warnings.
6. **Tighten `go test -race` to `./...`** in CI; the current
   `go test -race ./internal/cli/...` timeout should be gone.
7. **Add `staticcheck` and `govulncheck`** to the toolchain gate.
8. **Document the new package layout** in a top-level `ARCHITECTURE.md` (or extend
   `AGENTS.md`).

**Verification.**

```bash
go test -race ./...         # must finish under 60s
go test -cover ./...        # coverage not worse than before deletion of brute-force tests
golangci-lint run
staticcheck ./...
govulncheck ./...
```

New `TestTUI_Update` snapshot tests on a fixed set of `tea.Msg` inputs.

**Risk.** Low. Quality gates are additive. The main risk is coverage regression
after deleting the brute-force tests; **mitigation:** add 3–5 snapshot tests per
major handler before deletion.

**Estimated impact.** Test time for full `-race` suite drops below 60 s; brute-force
test files deleted (~80 KB of reflection-based test code); CI gates prevent future
regressions. **Effort: S–M.** **Public API change: no.**

---

## 3. Tooling & Conventions

- Follow existing project rules in `AGENTS.md` / `CLAUDE.md`.
- Build tags: stay consistent with existing files; do not introduce new build tags
  without maintainer sign-off.
- Error wrapping: `fmt.Errorf("context: %w", err)` — no bare `errors.New` for
  wrapped errors. Sentinel errors declared as `var ErrX = errors.New("…")`.
- No `init()` functions (the project convention is explicit constructors).
- Gates (run in this order on every PR):
    1. `go build ./...`
    2. `go vet ./...`
    3. `go test -race ./...` (full suite, must finish under 60 s after Phase 6)
    4. `golangci-lint run` (per `.golangci.yml`)
    5. (After Phase 6) `staticcheck ./...` and `govulncheck ./...`
- Reuse existing patterns:
    - The `newTool[TArgs, TResults]` generic factory in
      `internal/tools/registry.go:169-193` is the model for new typed factories
      (e.g., the proposed `toolCallBuffer[T]`).
    - The `coercingTool` decorator at `internal/tools/registry.go:295-434` is the
      precedent for layered wrappers.
    - The `session.Service` interface in `internal/session/store.go` is the
      precedent for keeping the public surface stable while refactoring internals.
- Prefer `strconv.Quote` for single-string JSON escaping; `json.Marshal` for
  struct values; **never** hand-roll a JSON escaper.
- Prefer typed enums (e.g., `type phase int; const …`) over `string` for state
  machines; add `String()` for rendering.
- Use table-driven tests with `t.Run` for any function with ≥ 3 cases (the
  project uses them pervasively).

---

## 4. Anti-Goals

- **Do not rewrite the Bubble Tea model tree from scratch.** Only decompose the
  parts that are demonstrably broken (200-line `handleKey`, 22-arm
  `handleSlashCommand`, 12-arm `formatToolResult`, the 7-site "mutate trailing
  message" pattern, the stringly-typed state machines). Leave working sub-models
  alone.
- **Do not introduce new external dependencies.** The refactor uses only stdlib +
  already-imported libraries (Bubble Tea, Lip Gloss, ADK, genai, otel).
- **Do not change the public CLI surface** (cobra command names, flags, exit
  codes) unless explicitly listed in a phase. Phases 2 and 3 touch `LLMOptions`
  and require a one-minor-version deprecation shim — that is the only acceptable
  public change.
- **Do not attempt a full migration to a typed schema system for tool results in
  Phase 1–2.** Phase 4's `formatToolResult` registry is a string-keyed dispatch
  table, not a typed schema. A typed schema migration is a separate effort.
- **Do not delete `coverage_boost_test.go` / `coverage_target_test.go` before
  Phase 4 is complete** — they are a safety net during the TUI decomposition.
  Phase 6 deletes them only after real tests cover the same surface.
- **Do not touch the ADK adapter surface in Phase 3** — only the internals
  (`storage`, `policy` sub-packages).
- **Do not backport `WithRetry` cancellation to the existing callers in one PR.**
  Wire it into `Agent.Run` first (Phase 1 #3); let callers migrate in follow-ups.

---

## 5. Open Questions

1. **Is the deprecation of `AgentEventCallback` shim (`subagent/agent.go:9-24`)
   acceptable in this milestone?** If yes, remove it in Phase 4; if not, keep it
   as a deprecated pass-through and add a `// Deprecated:` comment.
2. **Is moving the ATIF writer to append-only (`atif/writer.go:56-69`) acceptable
   given existing trajectory readers?** If downstream tools (e.g., the training
   data pipeline at `internal/atif/`) rely on the per-event rewrite, this change
   needs a versioned format bump.
3. **Is the deprecation of the 3 `LLMOptions` fields
   (`AdvisorModel` / `AdvisorMaxUses` / `AdvisorCaching`) acceptable?** Phase 2
   will keep them as pass-throughs for one minor version; confirm the deprecation
   timeline.
4. **Should `tui.New` be exported in a new `internal/tui/tuimodel` sub-package**
   (cleaner, no `tui` ↔ `tui` self-import), or in the existing `internal/tui`
   package? The latter is simpler; the former is more aligned with the Phase 3
   sub-package pattern.
5. **What is the target test time for `go test -race ./...`?** Phase 6 gates on
   a number; 60 s is a guess. Confirm with the maintainer before Phase 6 starts.

---

**Next step.** Start with **Phase 1, Task 1** — fix the `FileService.AppendEvent`
data race (`internal/session/store.go:329-388`). It's the highest-severity issue,
the smallest diff, and unlocks the rest of Phase 1. Verify with
`go test -race ./internal/session/...` and the new race-reproduction test before
moving to Task 2.
