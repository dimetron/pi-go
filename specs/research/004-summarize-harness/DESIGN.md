# Iterative Chunked-Read Summarization Harness

**Status:** Draft / Research
**Date:** 2026-04-18
**Owner:** dimetron
**Track:** `specs/features/SUB` (subagent) + `internal/tools`

## TL;DR

When a file exceeds pi-go's Read budget (default line cap = 2000 lines for non-source files, plus a 256 KB safety net in
`internal/tools/truncate.go`, plus AfterToolCallback compaction in `internal/tools/compactor_read.go`), the parent agent
today must manually paginate with `offset`/`limit` and stitch the results together in its own context. That is
expensive, lossy, and error-prone for files that are hundreds of thousands of lines (large logs, generated code,
vendored JSON, captured traces).

This document proposes a **subagent-backed iterative chunked-read harness** that:

1. Exposes a new tool `read_summary` in `internal/tools`.
2. Internally dispatches a dedicated `file-summarizer` subagent with its own isolated context window.
3. Drives a deterministic chunk-by-chunk traversal using opaque **continuation tokens**, so either the caller or the
   subagent can decide when to stop.
4. Preserves **fidelity** as the primary tradeoff — the subagent emits a structured running summary (sectioned outline +
   anchor line ranges) that can be cheaply re-queried rather than a single lossy compression.

The harness is layered on top of the existing `Read` tool and `subagent.Orchestrator`, not a replacement for them.

---

## 1. Problem

### 1.1 Current behaviour

- `internal/tools/read.go` caps non-source files at `defaultReadLimit = 2000` lines and truncates with
  `"... (truncated: showing N of M lines, use offset/limit to read more)"`.
- `internal/tools/truncate.go` enforces a 256 KB hard cap on output bytes.
- `internal/tools/compactor_read.go` runs a 4-stage pipeline (ANSI strip → optional source-code filtering →
  smart-truncate → hard-truncate) *inside* the AfterToolCallback, so even an explicit offset/limit window can be
  re-truncated before it reaches the model.

### 1.2 Failure modes observed

| Scenario                     | Today                                                   | Consequence                                                                                   |
|------------------------------|---------------------------------------------------------|-----------------------------------------------------------------------------------------------|
| 50 k-line JSON trace         | Read returns first 2000 lines + note                    | Parent loops `offset += 2000` until context fills; summary quality degrades late in the file. |
| Vendored minified JS         | `sourceCodeExts` skips the line cap, 256 KB hits first  | Hard-truncation mid-token; the smart-truncate stage has no structure to anchor on.            |
| Large log with 5 incidents   | Compactor smart-truncates                               | First incident survives, remaining 4 are dropped silently.                                    |
| Many large files (repo scan) | Each read burns ~24 k compacted chars in parent context | Parent OOMs on context before the question is answered.                                       |

### 1.3 What we actually need

A way to consume a file whose size is unbounded from the parent agent's perspective, where:

- the parent pays a **bounded, predictable token cost** per call,
- the summary is **faithful** (no silent drops of middle/tail content),
- the process is **resumable** — the parent can stop early, or drill deeper into a section, without restarting,
- it slots into the existing tool registry and AfterToolCallback pipeline with **no changes** to read.go's semantics.

---

## 2. Goals & Non-Goals

### Goals

1. A single tool the parent calls: `read_summary(file_path, question?, cursor?)`.
2. Bounded per-call output: parent sees ≤ ~8 KB regardless of file size.
3. Fidelity: every byte of the file is examined by the summarizer subagent at least once before the first summary is
   returned.
4. Resumable: opaque `cursor` returned to the parent lets it (or a future call) drill into a specific section.
5. Deterministic chunking: chunk boundaries are computed, not prompted — so tests can assert on them.
6. Integrates with the existing `subagent.Orchestrator` and `BuildCompactorCallback`.

### Non-Goals

- Replacing `Read` for small files. Parent still calls `read` directly when it can.
- Full-text semantic index. This harness is *per-call*, not a persistent store.
- Cross-file reduce. Callers that need repo-wide digests compose this tool with the existing parallel subagent mode.
- Guaranteeing token-perfect budgets. We target *bounded*, not *minimal*.

---

## 3. Design

### 3.1 High-level flow

```
parent agent ──► read_summary(file_path, question?, cursor?)
                      │
                      ▼
        ┌─ small file? ─► call readHandler directly, return content
        │
        ▼
  spawn file-summarizer subagent (single mode, isolated context)
        │
        ├─ chunk planner: stat file, compute deterministic chunks
        │
        ├─ for each chunk:
        │     • subagent calls read(file_path, offset, limit) internally
        │     • appends an outline entry to a running summary buffer
        │     • emits a progress event to the parent's TUI
        │
        ├─ final pass: subagent answers `question` (if provided) using
        │              the running summary + targeted re-reads of anchor
        │              line ranges
        │
        ▼
  return SummaryOutput{ summary, outline, cursor, stats }
```

The subagent's **context** is sacrificial: it can fill up across many chunks because nothing in it flows back to the
parent — only the compact `SummaryOutput` does.

### 3.2 Go API

Add under `internal/tools/read_summary.go`:

```go
// ReadSummaryInput is the input to the read_summary tool.
type ReadSummaryInput struct {
    FilePath   string `json:"file_path"`                   // absolute path, required
    Question   string `json:"question,omitempty"`          // optional focus question
    Cursor     string `json:"cursor,omitempty"`            // opaque continuation token
    MaxChunks  int    `json:"max_chunks,omitempty"`        // safety cap; 0 ⇒ unbounded
    Detail     string `json:"detail,omitempty"`            // "outline" | "standard" | "deep"
}

// ReadSummaryOutput is what the parent agent sees.
type ReadSummaryOutput struct {
    Summary    string         `json:"summary"`              // ≤ ~4 KB of prose
    Outline    []OutlineEntry `json:"outline"`              // sectioned map of the file
    Cursor     string         `json:"cursor,omitempty"`     // non-empty ⇒ more to read
    Stats      SummaryStats   `json:"stats"`
    Truncated  bool           `json:"truncated,omitempty"`  // MaxChunks hit before EOF
}

// OutlineEntry is a structural anchor into the original file.
type OutlineEntry struct {
    Title     string `json:"title"`                         // short label
    StartLine int    `json:"start_line"`                    // 1-based inclusive
    EndLine   int    `json:"end_line"`                      // 1-based inclusive
    Kind      string `json:"kind"`                          // "section","error","config",...
    Digest    string `json:"digest"`                        // 1–2 line description
}

type SummaryStats struct {
    TotalLines    int           `json:"total_lines"`
    TotalBytes    int64         `json:"total_bytes"`
    ChunksRead    int           `json:"chunks_read"`
    ChunksTotal   int           `json:"chunks_total"`
    SubagentID    string        `json:"subagent_id"`
    Duration      time.Duration `json:"duration"`
}
```

Registration (sketch) in `internal/tools/registry.go`:

```go
func newReadSummaryTool(sb *Sandbox, orch *subagent.Orchestrator, onEvent SubagentEventCallback) (tool.Tool, error) {
    return newTool("read_summary", readSummaryDesc, func(ctx tool.Context, in ReadSummaryInput) (ReadSummaryOutput, error) {
        return readSummaryHandler(resolveContext(ctx), sb, orch, in, onEvent)
    }, map[string]string{"path": "file_path", "prompt": "question"})
}
```

### 3.3 Chunk planner (deterministic)

```go
type chunkPlan struct {
    chunks     []chunkRange // [{start_line, end_line}, ...]
    lineCount  int
    byteCount  int64
}

// planChunks walks the file once with a line scanner and emits chunks that
// target ~chunkTargetBytes (default 180 KB) but snap to the nearest blank
// line / structural boundary within ±20% slack. This keeps semantic units
// intact (log incident, JSON object, Go function) across chunk boundaries.
func planChunks(sb *Sandbox, path string) (chunkPlan, error) { ... }
```

Key properties:

- Runs in the **subagent**, so the parent never sees the raw walk.
- Pure function of `(file bytes, chunkTargetBytes)` — snapshot at start.
- Snap points are language-agnostic (blank line, `}` at col 0, `---` YAML marker, top-level `{` / `[` for JSON).
- Safe under append-only files: planner records `(totalBytes, mtime)` into the cursor; a resume with a changed file
  yields `ErrFileChanged` to the caller rather than silently drifting.

### 3.4 Continuation-token format

The `cursor` field is **opaque to the parent** but deterministically decodable:

```
cursor = base64url(gzip(json({
  "v": 1,
  "path": "/abs/path",
  "size": 123456789,
  "mtime_ns": 1713456789012345678,
  "plan_hash": "sha256:...",   // hash of the chunkPlan
  "next_chunk": 7,             // index into plan.chunks
  "question": "original question, if any",
  "detail": "standard",
  "outline_sofar": [...]       // digests already emitted (bounded: last 32)
})))
```

Rationale:

- Self-contained: the resuming call does not need server-side state.
- Tamper-evident: `plan_hash` + `size` + `mtime_ns` detect file change.
- Bounded: `outline_sofar` is capped so the cursor stays under 4 KB.
- Versioned: `"v": 1` lets us evolve the scheme without breaking older cursors.

### 3.5 Subagent protocol

A new bundled agent `internal/subagent/bundled/file-summarizer.md`:

```markdown
# file-summarizer

You are a fidelity-first file summarizer. You receive:
- `file_path`: absolute path to read
- `plan`: a precomputed list of (start_line, end_line) chunks
- `start_index`: the chunk index to begin at
- `question`: optional focus; if empty, produce a neutral outline
- `detail`: "outline" | "standard" | "deep"

For each chunk in order:
1. Call `read(file_path, offset=start_line, limit=end_line-start_line+1)`.
2. Append to your running OUTLINE an entry per semantic section in this chunk:
   - start_line, end_line, kind, 1–2 line digest
3. If `detail=="deep"`, additionally quote any line that directly answers `question`.
4. Never drop a chunk. If a chunk is boring (blank lines, base64, minified),
   emit ONE outline entry covering its whole range with kind="opaque".

When all chunks are processed, emit one JSON object:
{ "summary": "...", "outline": [...], "followups": [...] }

Hard rules:
- Do not hallucinate line numbers. Every OutlineEntry must come from a chunk
  you actually read.
- If read returns fewer lines than requested (EOF), stop cleanly.
- If read returns a checksum-mismatched chunk (file changed), abort with
  `{"error":"file_changed"}`.
```

The wrapper in `readSummaryHandler` turns the subagent's final JSON into `ReadSummaryOutput`, fills `Cursor` if
`MaxChunks` was hit, and records `SubagentID`/`Duration` from `AgentResult`.

### 3.6 Small-file fast path

```go
if stat.Size() < smallFileThreshold { // e.g. 96 KB
    out, err := readHandler(sb, ReadInput{FilePath: in.FilePath})
    if err != nil { return ReadSummaryOutput{}, err }
    return passthrough(out), nil
}
```

This keeps the common case cheap: no subagent spawn, no chunk plan, same cost as today's `read`.

### 3.7 Compactor interaction

`read_summary` results flow through `BuildCompactorCallback` like any other tool. We add one case in `compactor.go`'s
switch:

```go
case "read_summary":
    return compactReadSummary(result, cfg)
```

`compactReadSummary` should be **mostly a pass-through**: the output is already bounded and structured. It only applies
`hardTruncate` on `summary` as a last-resort safety net (the subagent misbehaving) and leaves `outline` alone —
truncating outline entries would re-introduce the silent-drop problem we are solving.

### 3.8 Events & TUI

Reuse the existing `SubagentEventCallback`. The harness emits:

- `spawn` when the summarizer starts
- `tool_call`/`tool_result` per chunk read (lets the TUI show a progress bar: `chunks_read / chunks_total`)
- `done` with final stats

No new event kinds. `PipelineID` is set so the TUI can group this under a synthetic "read_summary" pipeline rather than
as a raw subagent invocation.

---

## 4. Alternatives Considered

### A. Extend the compactor pipeline (rejected)

Add an LLM-summarization stage in `compactor_read.go` between smart-truncate and hard-truncate.

- **Pro:** no new tool, parent's mental model unchanged.
- **Con:** AfterToolCallback runs on *already-truncated* input (256 KB cap fires first). You cannot summarize what you
  never saw. Also couples every `read` call to an LLM round-trip, which is a regression for small files and for agents
  that want byte-exact output.

### B. Pure heuristic outline extraction (rejected)

Scan the file with AST-aware splitters (tree-sitter, JSON streaming) and emit a structural outline deterministically, no
LLM.

- **Pro:** fast, reproducible, zero cost.
- **Con:** no answer to `question`, no cross-section digest, no recovery on malformed files, and the set of supported
  syntaxes is a permanent maintenance burden. Worth building later as a **planner helper**, not as the whole harness.

### C. Map-reduce inside a single call (rejected as default)

Spawn N parallel summarizer subagents over N chunks, then reduce.

- **Pro:** lower wall-clock latency.
- **Con:** loses sequential context (chunk N+1's meaning depends on chunk N for logs/traces). Fidelity suffers. Keep
  parallel map-reduce available as an explicit `mode: "parallel"` opt-in, but the **default is sequential** because the
  chosen tradeoff is fidelity.

### D. Client-side pagination only (status quo, rejected)

Make the parent smarter at calling `read(offset, limit)` in a loop.

- **Pro:** zero new code.
- **Con:** burns parent context; the whole point is to offload that. This is what we have today and what the user is
  asking to replace.

---

## 5. Failure Modes & Mitigations

| Failure                            | Detection                                                                         | Mitigation                                                                   |
|------------------------------------|-----------------------------------------------------------------------------------|------------------------------------------------------------------------------|
| File changes mid-traversal         | `mtime_ns`/`size`/`plan_hash` in cursor vs. current stat                          | Return `truncated=true` with `cursor=""`; surface `ErrFileChanged`.          |
| Subagent hangs                     | Orchestrator timeout (reuse `internal/subagent/timeout.go`)                       | Cancel, return partial `outline_sofar` from cursor if available.             |
| Subagent hallucinates line numbers | Wrapper re-validates every `OutlineEntry.EndLine ≤ TotalLines`                    | Drop offending entries, log, return rest.                                    |
| File is binary / not UTF-8         | Planner detects via first 4 KB sniff                                              | Short-circuit to `summary="binary file, N bytes, sha256=..."`.               |
| `max_chunks` hit                   | Counter in handler                                                                | Return `cursor` so caller can resume.                                        |
| OOM on very large files            | Planner is streaming; running summary bounded at ~32 outline entries before flush | Oldest entries are demoted to `kind="archived"` with a single merged digest. |

---

## 6. Integration Points in pi-go

1. `internal/tools/read_summary.go` — new file; handler + planner.
2. `internal/tools/registry.go` — register tool (needs `Orchestrator` passed in; matches how `NewSubagentTool` is
   wired).
3. `internal/tools/compactor.go` — add `case "read_summary":` and a minimal `compactReadSummary` in a new
   `compactor_read_summary.go`.
4. `internal/subagent/bundled/file-summarizer.md` — new bundled agent; add to `embed.go` embed list.
5. `internal/subagent/types.go` — no changes; reuse `SpawnInput`.
6. `specs/features/SUB/summarize-large-file/` — move this doc here once it graduates from research.

Touching AfterToolCallback is optional and additive; if we skip step 3, the parent sees a bounded `summary` but no
compactor stats — acceptable for v1.

---

## 7. Test Plan

Follow the `go-tdd` skill: stdlib-first, table-driven, subtests.

### 7.1 Unit — planner (`read_summary_plan_test.go`)

| Case                                                 | Assertion                                               |
|------------------------------------------------------|---------------------------------------------------------|
| Empty file                                           | `len(chunks)==0`, no error                              |
| 1-line file                                          | `len(chunks)==1`, chunk covers it                       |
| 10 KB uniform text                                   | `len(chunks)==1` at default target                      |
| 1 MB with explicit blank-line boundaries every 50 KB | chunks snap to blank lines, no chunk splits a paragraph |
| 5 MB of minified JSON (no newlines)                  | falls back to fixed-byte cuts, total coverage == size   |
| mtime changes between plan and verify                | `ErrFileChanged`                                        |

### 7.2 Unit — cursor codec

- Round-trip: `encode(x)` then `decode` equals `x`.
- Mutated cursor (flip one byte) returns `ErrCursorInvalid`, never panics.
- Cursor size bound: 95th percentile < 4 KB on synthetic inputs with 32-entry outlines.

### 7.3 Integration — handler with fake subagent

Use an `Orchestrator` seeded with a stub `file-summarizer` agent (driven by a scripted responder in
`internal/subagent/spawner_test.go` style). Assert:

- Small file path does not spawn a subagent (counter at zero).
- Large file path spawns exactly one subagent and consumes the entire plan.
- `MaxChunks=2` on a 5-chunk file returns `Truncated=true` and a non-empty `Cursor`.
- Resuming with that `Cursor` and `MaxChunks=0` finishes the remaining chunks and returns `Cursor==""`.

### 7.4 Integration — full stack with real ADK + bundled agent

One guarded test (`//go:build e2e`) that exercises the real summarizer on a checked-in large fixture (
`internal/tools/testdata/large_log.txt`, ~5 MB synthetic). Asserts:

- `stats.ChunksRead == stats.ChunksTotal`
- Every `outline[i].EndLine ≤ stats.TotalLines`
- `len(summary) ≤ 4 KB`

### 7.5 Fuzz — outline validation

`FuzzOutlineValidation` — random `OutlineEntry` slices; the validator must never panic and must drop any entry violating
invariants.

### 7.6 Regression

Add a golden-file test that confirms the existing `read` tool's behaviour is **unchanged** (line cap, base64 stripping,
256 KB net). The harness must be purely additive.

---

## 8. Rollout

1. Land planner + cursor codec + tests (no tool registration). Merge behind a build tag if desired.
2. Land the bundled `file-summarizer` agent and a `spawner_test.go`-style fake. Unit tests green.
3. Land the tool, register it, wire the compactor case.
4. Ship with `Enabled=false` via a new config flag `ReadSummaryEnabled`. Dogfood on a handful of real large-file cases
   before flipping default on.
5. Retire ad-hoc offset/limit loops from agent prompts (`bundled/explore.md`, `discovery.md`) once the tool is stable.

---

## 9. Open Questions

1. Should `read_summary` write its outline into `FileContentCache` so subsequent `read(offset, limit)` calls can hit a
   warm byte cache? Pro: saves re-IO when the parent drills in. Con: cache invalidation semantics change.
2. Does `detail: "deep"` need its own subagent (with a different prompt) or is it a parameter into one agent? Leaning
   toward parameter to keep the bundled-agent count low.
3. How should this compose with `a2a.go` when pi-go is running as a remote agent? Likely: expose `read_summary` over A2A
   the same way `read` is exposed; cursors survive the wire because they're base64 strings.
4. Do we want a TUI-side shortcut for "drill into outline entry #3"? Follow-up UX work, not required for v1.

---

## 10. Appendix: Why "fidelity-first" changes the design

If the tradeoff were **token budget**, the right answer is (A) — one more compactor stage that aggressively summarizes.
It is cheap and ships fast, at the cost of silent drops.

If the tradeoff were **latency**, the right answer is (B) — deterministic structural extraction with no LLM in the loop.

Fidelity flips both choices:

- We must see every byte *before* summarizing, which forces us out of the AfterToolCallback (input is already truncated
  there) and into a driver that owns the read loop.
- We must preserve structural anchors (line ranges) so the parent can verify or drill in, which forces a structured
  `outline` output rather than a blob of prose.
- We must fail loudly on file change, which forces cursor-based resumability with a tamper-evident plan hash.

The subagent-backed chunked-read harness is the minimum design that satisfies all three.
