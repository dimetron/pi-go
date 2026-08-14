# Requirements

## Scope

Repair the existing memory subsystem so it records what it was designed to
record. No new memory features, no data-model changes, no parity work against
upstream MemPalace beyond the two items called out in R8 and R9.

## Acceptance criteria

### R1 — Every after-tool callback runs

**Given** a session with the default callback set (hooks, OTel tracing, LSP,
compactor, dedup, memory recorder)
**When** any tool call completes successfully
**Then** all six callbacks execute, in registration order, and the tool result
the model sees is the one produced by the last transforming callback.

- A callback returning `nil` must be treated as "no change", not "stop".
- A callback returning a map must feed that map to the next callback.
- A callback returning an error must abort the chain and propagate — unchanged
  from today.
- Regression test asserts all of: an observation is enqueued, the compactor
  truncated the output, and the deduper saw the call — for one tool call.

- A callback that transforms a result must have that transformation visible to
  the next callback. ADK does not do this: it passes the original `fResult` to
  every callback, so the compactor-then-dedup ordering has never composed.

**Rationale:** this is the whole outage. See `research/findings.md` § F1, which
records both defects.

### R2 — Observations survive a short session

**Given** a `--mode print` run that makes one tool call and exits immediately
**When** the process shuts down
**Then** the observation is stored, and no `sql: database is closed` error is
logged.

- The store must not close until the worker has drained or the drain deadline
  expires.
- The drain deadline must be configurable and default to a value larger than one
  compression (≥ 30 s).
- Dropped work must be counted and logged at shutdown: "N observations dropped".

### R3 — Compression does not serialise the pipeline

**Given** a session producing tool calls faster than compression completes
**When** the queue is under load
**Then** compressions proceed concurrently and enqueue never silently discards.

- Configurable worker concurrency, default > 1.
- Per-compression timeout ≤ 60 s, not the current 600 s.
- A dropped enqueue increments a counter surfaced in the shutdown log.

### R4 — Compression is affordable

**Given** the default configuration
**When** an observation is compressed
**Then** it does not spawn a `pi` child process per tool call.

- Compression runs in-process against the resolved `smol` role.
- `smol` resolves to an actual small model; if no `smol` role is configured, the
  fallback must be logged once per session, not silently billed at frontier
  prices.
- The existing `Compressor` interface is the seam — `SubagentCompressor` remains
  available and selectable.

### R5 — One palace path

**Given** any pi-go entry point — `pi memory *`, the TUI sidebar, or an agent
session
**When** it resolves the palace database
**Then** all of them resolve to the same file for the same working directory.

- Single exported resolver; no entry point computes the path itself.
- Precedence: `--db` flag → `cfg.Palace.DBPath` → project `<root>/.pi-go/palace.db`
  → `$HOME/.pi-go/palace.db`.
- Same rule for the embedding-model directory; `pi memory model download`,
  `pi memory model status` and the session embedder must agree.

### R6 — Palace works in the TUI

**Given** an interactive session in a project with a palace
**When** the session starts
**Then** palace tools are registered, wake-up context is injected, and the
observation bridge is wired — exactly as in the headless path.

- Absent palace remains a silent no-op, not an error.
- The sidebar reads the same database the session opened.

### R7 — Wake-up is scoped to the project

**Given** a palace containing drawers from several projects
**When** a session in project X wakes up
**Then** only wing X's drawers appear in the L1 essential story.

- The wing derivation used by wake-up and by `ObservationBridge` must be one
  shared function.

### R8 — Search input is sanitised

**Given** a drawer whose content contains instruction-shaped text
**When** it is retrieved and injected into the prompt
**Then** it is delimited as untrusted data.

- Query text is neutralised before it reaches FTS5 beyond quote-stripping.
- Retrieved drawer bodies are fenced in the injected context.

### R9 — Retrieval is measured

**Given** a committed fixture corpus
**When** `make test-memory-bench` runs
**Then** it prints recall@5 and asserts a floor, so a regression fails CI.

- Fixture corpus committed, small, no network, no API key.
- A test asserts the end-to-end path: tool call → observation → drawer →
  retrievable by `mem-search`. This is the test that would have caught the
  outage on day one.

## Constraints

- No change to the `drawers`, `observations`, `sessions`, `triples` or
  `entities` schemas. Additive migrations only if unavoidable.
- Memory and palace stay best-effort: any failure degrades to "no memory" with a
  warning and never blocks a turn.
- Existing public surfaces stay: `memory.Store`, `memory.Compressor`,
  `palace.PalaceStore`, `palace.PalaceTools`, all `pi memory` subcommands.
- Gate for every slice: `make test`, `make vet`, `make lint`.
- `internal/cli` tests bind local listeners and must be run outside the sandbox
  (`CLAUDE.md` § "Two environment traps").

## Out of scope

AAAK dialect, entity detection/disambiguation, repair/export/onboarding
commands, the MCP surface, pluggable vector backends, per-message sweeping,
verbatim (non-compressed) storage. Catalogued in `research/comparison.md`.
