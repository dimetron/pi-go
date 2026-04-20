# pi-go Codebase Review

> Multi-agent review conducted by Cursor, Claude, and Gemini on the `feature/acp-client-server` branch.

---

## Overall Verdict

**No blockers for release.** The codebase is mature, well-tested, and production-ready. The multi-provider abstraction
is a highlight — adding a new LLM requires implementing one interface. Main risks are file-length growth, hardcoded
model catalogs, and string-matched transient error detection that will rot as SDKs evolve.

| Reviewer | Duration | Grade                                                 |
|----------|----------|-------------------------------------------------------|
| Gemini   | 1m 51s   | Excellent — clean layered architecture                |
| Cursor   | 2m 52s   | Well-layered with modern Go and thoughtful safeguards |
| Claude   | 4m 36s   | Strong — production-ready, 12 quick wins identified   |

---

## 1. Architecture & Structure

**Shape:**
```
cmd/pi/main.go              # thin entrypoint (15 lines) — delegates to internal/cli
internal/
  agent/        ADK runner wrapper + retry logic
  provider/     LLM abstraction (Anthropic, OpenAI, Azure, Gemini, Mistral, Ollama)
  tools/        Sandbox + core tools (read/write/edit/bash/grep/find/git/lsp/mem)
  session/      File-backed JSONL session service with ATIF trajectories
  acp/          ACP client (subprocess runner) + server (agent adapter)
  tui/          Bubble Tea v2 interactive interface
  cli/          Cobra entry, mode dispatch (interactive/print/json/rpc)
  subagent/     Concurrency pool, git worktree isolation, orchestration
  lsp/          Language server manager, JSON-RPC client, diagnostics
  extension/    Hooks, skills (SKILL.md), MCP tool integration
  memory/       SQLite + AI compression (marked not-yet-production-ready)
  guardrail/    Usage limits and daily rollover
  palace/       (advanced memory)
```

**Strengths:**

- `internal/` correctly hides implementation from external importers
- Provider system abstracts 6+ LLM backends through a single `model.LLM` interface
- Sandbox uses Go 1.24+ `os.Root` — a real OS-level filesystem jail, not a path-prefix check
- Session persistence uses atomic `tmp+rename` for safe rewrites
- Streaming-first: `iter.Seq2[*session.Event, error]` iterators throughout
- Subagent orchestration with git worktree isolation is advanced engineering
- Hidden character scanner in skills auditing is a unique prompt-injection defense
- `coercingTool` in `registry.go` elegantly handles LLM type hallucinations (e.g. stringified numbers)

**Weak points — file size / function length:**

| File                             | Lines | Concern                                                  |
|----------------------------------|-------|----------------------------------------------------------|
| `internal/tui/run.go`            | ~1195 | God-file candidate                                       |
| `internal/tui/tui.go`            | ~1083 | Borderline god-file                                      |
| `internal/cli/cli.go`            | ~1053 | `runNonInteractive` alone is ~340 lines                  |
| `internal/provider/anthropic.go` | ~814  | Streaming + non-streaming + beta advisor variants inline |

`runNonInteractive` (`cli.go:271-621`) initializes sandbox, tools, subagents, memory, palace, LSP, MCP, A2A, hooks,
skills, session, and callbacks in one sequential block. Recommend extracting `buildToolChain()`, `initMemory()`,
`initPalace()`, `buildCallbacks()` helpers.

---

## 2. Code Quality & Go Best Practices

**Good:**

- Error wrapping uses `%w` consistently at all boundaries
- Package docs on key packages (`agent`, `session`, `sandbox`)
- Compile-time interface checks: `var _ session.Service = (*FileService)(nil)`
- `iter.Seq2` idiom adopted pervasively for streaming
- Contextual cancellation threaded through bash/LLM calls
- Modern `errors.AsType[E]` (Go 1.26) used over manual `errors.As` + cast

**Concerns:**

1. **`agent.RebuildWithInstruction` duplicates `New` logic** (`agent.go:248-282`). Extract a private
   `buildRunner(cfg) (*runner.Runner, error)` and call it from both.
2. **Hardcoded model fallback / user-agent** in `anthropic.go:20-21, 111`:
   ```go
   const anthropicOAuthUserAgent = "claude-cli/2.1.75"   // drifts with upstream
   modelName = "claude-opus-4-7"                         // buried default
   ```
   Move to config or constants with a comment tying them to an upstream version.
3. **`KnownModels` maintenance** (`provider.go:94-170`): a hand-curated snapshot requiring a code change per new model.
   Consider moving to `configs/models.yaml` or replacing with "warn-on-unknown" and trusting provider 400 errors.
4. **`registry.go coercingTool.Run` uses a runtime interface assertion** (`registry.go:268-274`). Drop the fallback and
   assert at construction time — the impossible branch creates misleading error messages.

---

## 3. Error Handling

**Strong:**

- `agent/retry.go isTransient` catches `Timeout()`/`Temporary()` interfaces in addition to string patterns — good
  defense in depth
- `WithRetry` correctly **does not retry** after partial events have been yielded — prevents duplicate tool calls
- Bash tool maps `exec.ExitError`, timeouts, and other errors distinctly

**Weak:**

1. **String-match transient detection** (`retry.go:43-62`) is fragile across provider versions. Add provider-specific
   typed errors (e.g. `anthropic.RateLimitError`) via `errors.As` as a primary path, falling back to strings.
2. **Bash timeout path discards stderr** (`bash.go:75-79`):
   ```go
   return BashOutput{Stdout: ..., Stderr: "command timed out", ExitCode: -1}, nil
   ```
   The original `stderr.String()` is dropped — users lose diagnostic output when a command is killed. Fix:
   `"command timed out\n" + stderr.String()`.
3. **ACP client callbacks return generic errors** (`runner.go:78-112`). Use `acp.NewMethodNotFound(...)` instead of
   `fmt.Errorf` so peers can distinguish "not implemented" from transient failures.
4. **Dropped events on full channel** (`session.go:190-195`) — silently discarded with no log. At minimum, add a `WARN`
   log line.

---

## 4. Potential Bugs

1. **`session.AppendEvent` holds global lock across disk I/O** (`store.go:329-387`): `appendEventToFile`, `writeMeta`,
   and `saveBranches` are synchronous disk writes under the service-wide mutex. A slow disk blocks every other session
   operation. Fix: use the per-session `sess.mu` already present and release the service lock sooner.

2. **`sandbox.shouldSkipPath` operator precedence ambiguity** (`sandbox.go:467`):
   ```go
   if strings.HasPrefix(base, ".") && base != "." && !agentDirs[base] || base == "node_modules" || ...
   ```
   Go binds `&&` tighter than `||` — probably correct, but add parentheses to prevent future edits from breaking it.

3. **`agent.LoadInstruction` has no size bound** (`agent.go:331-369`):
   ```go
   data, err := os.ReadFile(agentsFile)
   instruction += "\n\n# Project Rules\n\n" + string(data)
   ```
   A 10MB `AGENTS.md` goes straight into every system prompt. Add a 128KB cap with a warning.

4. **Grep regex cache eviction is O(n) in `put`** (`grep.go:60-74`). Cache maxes at 50 entries — fine today, but switch
   to a proper LRU if it grows.

5. **`BashTimeout` max clamp is silent** (`bash.go:48-50`). An LLM requesting a 15-min timeout gets 10 min with no
   signal. Return a warning in `BashOutput`.

6. **Session `GenerateSessionID` fallback** (`store.go:86-92`): falls back to nanosecond-based non-random IDs if
   `crypto/rand.Read` fails. On Linux this essentially never fails — if it does, something is badly wrong; consider
   failing loudly instead.

---

## 5. Security

**Solid:**

- **`os.Root` filesystem sandbox** (`sandbox.go`) — symlink escape, `..` escape, absolute-path escape all blocked at OS
  level
- **Secret redaction** on bash stdout/stderr (`redact.go`) covers `sk-*`, `ghp_*`, `gho_*`, `sk-ant-*`, Bearer tokens,
  `KEY=value` env patterns
- **OAuth PKCE and device-code flows** with keys stored in `~/.pi-go/.env` at mode `0o600`
- **Hidden character auditing** in skills prevents prompt injection via Unicode tricks

**Gaps:**

1. **pprof binds to all interfaces** — code says `":"+ flagPprofPort` but startup message says "localhost". **Fix:
   listen on `127.0.0.1` only.**
2. **`redact.go` misses common patterns**: JWT tokens (`eyJ...`), GCP service-account keys, AWS `AKIA[0-9A-Z]{16}`.
3. **`.pi-go/.env` is readable by the agent** — sandbox explicitly un-blocks `.pi-go/` to allow skill files. Consider
   denylisting `.env` specifically within the sandbox.
4. **`--insecure` / `InsecureSkipVerify: true`** has `nolint:gosec` comment but no runtime warning. Emit a startup
   warning to stderr when active.
5. **`AutoApproveOutcome`** (`acp/permissions.go`) falls back to the first option if no `allow_*` action is found. Add a
   godoc note that this is **unsuitable for multi-tenant or untrusted ACP servers**.

---

## 6. Performance

**Strengths:**

- Ripgrep-first, Go-fallback grep strategy
- `FileContentCache` with mtime-based invalidation
- Streaming LLM responses via `iter.Seq2`
- Deferred TUI initialization ensures immediate UI presence
- Token compaction for large bash/git outputs maximizes context window value
- Subagent concurrency pool with buffered event channel (size 256)

**Bottlenecks:**

1. **`FileService.AppendEvent` global lock + disk I/O** — see Bug #1 above.
2. **`session.List` walks disk under RLock** (`store.go:218-260`). With hundreds of sessions this is O(n) `os.Stat`
   calls on every `pi sessions list`. Add an in-memory index refreshed on write.
3. **`estimateEventTokens` walks every event on every call** (`store.go:833-855`). Trivially cached as a running total
   updated in `AppendEvent`.
4. **`toolFingerprint` hashes full JSON of args every call** — cheap vs LLM latency, but large payloads could show up in
   CPU profiles.

---

## 7. Test Coverage

- **~90% test-file ratio** (161 test files / 178 source files) — excellent
- **~78% statement coverage** overall
- Strong packages: `internal/audit` ~95%, `internal/guardrail` ~98%, `internal/acp` ~90%, `internal/agent` ~88%
- Weaker: `internal/acp/client/cursor` ~62%, `internal/cli` ~72%, `internal/provider` ~72% (expected for I/O-heavy code)
- TUI tested with `teatest_test.go` (1654 lines) and golden file snapshots
- E2E tests gated behind `e2e` build tag; dedicated shell scripts per provider

**Currently failing tests (block CI green):**

| Package         | Test                                        | Likely Cause                                                 |
|-----------------|---------------------------------------------|--------------------------------------------------------------|
| `internal/auth` | `TestStartManualCodeFlow_AnthropicProvider` | Provider registry vs. environment mismatch                   |
| `internal/lsp`  | `TestRuffDiagnostics`                       | Missing temp file — race in test setup                       |
| `internal/tui`  | Several `login_test.go` cases               | Expects anthropic/openai/gemini but runtime shows codex-only |

**Coverage gaps:**

- `cli.go runNonInteractive` — monolithic, no direct unit tests (only covered via e2e)
- `agent.go LoadInstruction` — no test for the missing size-cap behavior
- `retry.go isTransient` — string patterns should have property-style tests against real provider error types

---

## 8. Quick Wins

Sorted by effort vs. value:

| #  | File:Line                            | Change                                                                                  |
|----|--------------------------------------|-----------------------------------------------------------------------------------------|
| 1  | `internal/tools/bash.go:75`          | Include original stderr in timeout error output                                         |
| 2  | `internal/agent/agent.go:248`        | Extract shared `buildRunner` helper; remove ~35 LoC duplication                         |
| 3  | `internal/acp/client/runner.go:78`   | Return `acp.NewMethodNotFound(...)` from unimplemented callbacks                        |
| 4  | `internal/acp/client/session.go:190` | Log dropped events instead of silently discarding                                       |
| 5  | `internal/tools/sandbox.go:467`      | Add parentheses to mixed `&&`/`\|\|` expression                                         |
| 6  | `internal/agent/agent.go:343`        | Cap `AGENTS.md` file size at 128KB with a warning                                       |
| 7  | `internal/session/store.go:316`      | Move disk I/O out of service-wide lock (use per-session mutex)                          |
| 8  | `internal/provider/provider.go:29`   | Emit stderr warning when `InsecureSkipTLS` is active                                    |
| 9  | `internal/tools/redact.go`           | Add JWT (`eyJ...`) and `AKIA[0-9A-Z]{16}` redact patterns                               |
| 10 | `internal/cli/cli.go:271`            | Split `runNonInteractive` into `buildToolChain`, `initMemory`, `buildCallbacks` helpers |
| 11 | `internal/provider/anthropic.go:20`  | Move OAuth user-agent and default model to versioned `const` block                      |
| 12 | `internal/session/store.go:833`      | Cache token count as running total on `AppendEvent`                                     |

---

## 9. Additional Subagent Review — Duplicates, Large Files, Complexity

A focused follow-up review was run with **pi/code-reviewer** and **Claude** specifically targeting duplicate code,
oversized files, and complexity hotspots.

### Duplicates

- **ACP client runners are heavily duplicated across providers**:
    - `internal/acp/client/claudecode/*.go`
    - `internal/acp/client/gemini/*.go`
    - `internal/acp/client/cursor/*.go`

  Claude found these three runner implementations are near-identical large siblings, each carrying the same
  lifecycle/process-management logic, callback client methods, stderr handling, and helper functions, with only small
  provider-specific differences such as binary name and argument building. This is the strongest duplicate-code hotspot
  in the codebase and creates bug-drift risk because fixes need to be applied in multiple places.

- **Shared CLI/runtime helper logic is duplicated** between:
    - `internal/cli/cli.go`
    - `internal/acp/server/runtime.go`

  The pi reviewer called out duplicate helpers such as `providerEnvVar`, `mergeExtraHeaders`, `convertHooks`, and
  `detectGitRoot`. These should likely move into a shared internal package so behavior stays consistent.

- **Anthropic provider flow contains parallel implementation paths** in:
    - `internal/provider/anthropic.go`

  Claude noted repeated streaming/non-streaming and beta/non-beta execution paths. While not literal copy-paste in the
  same way as ACP runners, it has the same maintenance shape and is worth consolidating.

### Large Files

Largest implementation files highlighted by the reviewers:

- `internal/tui/run.go` — ~1195 LOC
- `internal/tui/tui.go` — ~1083 LOC
- `internal/cli/cli.go` — ~1052 LOC
- `internal/session/store.go` — ~891 LOC
- `internal/auth/auth.go` — ~887 LOC
- `internal/provider/anthropic.go` — ~814 LOC
- `internal/cli/ping.go` — ~749 LOC

These were consistently identified as maintainability risks because each mixes multiple concerns in one place, making
changes harder to reason about and review.

### Complexity Hotspots

- **`internal/cli/cli.go`**
    - pi reviewer flagged this as a major orchestration hotspot: config loading, model resolution, sandboxing, memory,
      palace, LSP, MCP, hooks, and session wiring all pass through one large startup path.

- **`internal/tui/tui.go`**
    - pi reviewer flagged `Update`, `handleKey`, and `View` as long state-machine-style handlers mixing rendering,
      input, modal flows, and agent lifecycle concerns.

- **`internal/tui/run.go`**
    - Claude highlighted it as one of the densest files in the repo, combining run-state management, gates, worktree
      merge logic, checklist parsing, and prompt/spec handling.

- **`internal/auth/auth.go`**
    - pi reviewer noted the file bundles parsing, OAuth/device flow, token exchange, persistence, TLS preflight, and key
      classification together, giving it too many responsibilities.

- **ACP runner lifecycle management**
    - Claude identified the runner/session process lifecycle, stdin/stderr coordination, cancellation, callbacks, and
      event fan-out as subtle logic duplicated in three places. This is both a duplication issue and a complexity issue.

- **Moderate growth hotspots in TUI support files**
    - `internal/tui/input.go`
    - `internal/tui/sidebar.go`

  pi reviewer noted repeated branching and rendering patterns here that are not yet critical, but are showing early
  feature-accretion pressure.

### Recommended Refactoring Priorities

1. **Unify ACP client runners behind a shared base implementation** with a small provider-specific adapter layer.
2. **Extract shared helper functions** now duplicated between CLI and ACP runtime paths.
3. **Split the TUI and CLI god files** by concern before adding more features:
    - `internal/tui/run.go`
    - `internal/tui/tui.go`
    - `internal/cli/cli.go`
4. **Decompose `internal/auth/auth.go`** into parsing, transport, and persistence responsibilities.
5. **Reduce anthropic flow duplication** by consolidating parallel execution paths where possible.

## 9. Consensus Summary

All three reviewers independently agreed on:

1. **Architecture is strong** — clean layering, good dependency direction, no circular imports
2. **`os.Root` sandbox** is modern and excellent — OS-level guarantee, not a path-prefix check
3. **Large files** (`tui/run.go`, `cli/cli.go`, `provider/anthropic.go`) are the main maintainability risk
4. **Test suite needs fixes** — failing tests undermine CI confidence before anything else
5. **No critical blockers** — the codebase is production-ready overall

**Recommended priority order:**

1. 🔴 Fix failing tests (`auth`, `lsp/ruff`, `tui/login`) so `go test ./...` is green
2. 🟠 Cap `AGENTS.md` size in `LoadInstruction` (silent correctness bug)
3. 🟠 Fix session global lock + disk I/O (latency under load)
4. 🟡 Bind pprof to `127.0.0.1` (security hardening)
5. 🟡 Include stderr in bash timeout output (UX/debuggability)
6. 🟢 Remaining quick wins from the table above
