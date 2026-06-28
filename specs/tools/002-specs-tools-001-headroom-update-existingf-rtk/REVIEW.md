# Spec & Delivery Review — Headroom RTK Compactor Port

**Reviewer:** Cursor CLI subagent (`subagent[cursor]`)  
**Date:** 2026-06-22  
**Spec:** `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/`  
**Verdict:** Implementation **not delivered** — SUMMARY.md "failed" verdict is **confirmed**.

---

## Delivery Status

### Confirmed failed — 0% of spec implementation landed

Independent verification matches SUMMARY.md. No headroom port code exists in `internal/tools/`.

| Expected artifact                                                                                | Status                                                                            |
|--------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------|
| `ccr.go`, `content_type.go`                                                                      | ❌ Missing                                                                         |
| `transform.go`, `orchestrator.go`                                                                | ❌ Missing                                                                         |
| `signals.go`, `adaptive_sizer.go`                                                                | ❌ Missing                                                                         |
| `log_compressor.go`, `diff_compressor.go`, `diff_noise.go`                                       | ❌ Missing                                                                         |
| `search_compressor.go`, `smart_crusher.go`                                                       | ❌ Missing                                                                         |
| `log_template.go`, `json_minifier.go`                                                            | ❌ Missing                                                                         |
| `ccr_retrieve.go`                                                                                | ❌ Missing                                                                         |
| Types: `ReformatTransform`, `OffloadTransform`, `CCRStore`, `ComputeOptimalK`, `KeywordDetector` | ❌ Zero matches in `internal/`                                                     |
| Restructured `CompactorConfig`                                                                   | ❌ Still flat — see `compactor.go:12–37`                                           |
| `BuildCompactorCallback` content-type routing                                                    | ❌ Still routes by tool name (`compactToolResult` switch on `toolName`)            |
| Slice 10/11 integration (metrics, TUI `/rtk stats`)                                              | ❌ `CompactRecord` still has `Techniques []string`, no `ContentType` / `CacheKeys` |
| `retrieve_compacted` / `CoreToolsWithCCR`                                                        | ❌ Not present                                                                     |

**What did land:** 85 parity fixture JSON files committed in `4d4db3f` (bundled with an unrelated Ollama provider fix).
No Go source, no tests, no harness.

**Gates:** `go build ./...` and `go test ./...` pass vacuously — there is no new code to compile or test. The prior
agent's gate-pass claims are misleading.

### `CompactorConfig` — still pre-port

```12:37:internal/tools/compactor.go
type CompactorConfig struct {
	Enabled               bool   `json:"enabled"`
	StripAnsi             bool   `json:"strip_ansi"`
	AggregateTestOutput   bool   `json:"aggregate_test_output"`
	FilterBuildOutput     bool   `json:"filter_build_output"`
	CompactGitOutput      bool   `json:"compact_git_output"`
	AggregateLinterOutput bool   `json:"aggregate_linter_output"`
	GroupSearchOutput     bool   `json:"group_search_output"`
	SmartTruncate         bool   `json:"smart_truncate"`
	SourceCodeFiltering   string `json:"source_code_filtering"` // "none", "minimal", "aggressive"

	MaxChars         int `json:"max_chars"`
	MaxLines         int `json:"max_lines"`
	// ... 11 more Max* integer limits ...
}
```

This contradicts requirement #8 and design.md's nested per-algorithm config.

---

## Spec Quality Assessment

### Strengths

1. **Thorough research phase** — `research/headroom-architecture.md` and `research/pi-go-current-compactor.md` give a
   credible gap analysis with LOC estimates, orchestrator flow, and algorithm inventories.
2. **Actionable design** — `design.md` defines concrete Go interfaces, type signatures, data-flow diagrams, and
   acceptance criteria. An implementer can start from it without re-reading Rust.
3. **Detailed plan** — `plan.md` breaks each slice into files, implementation notes (regex patterns, keyword lists,
   scoring formulas), verify commands, and explicit dependency notes (buried but present).
4. **Clear integration target** — Slice 10 describes how `BuildCompactorCallback`, `CoreToolsWithCCR`, config, and
   metrics should wire together.
5. **Executable PROMPT** — Per-slice verify regexes (`-run "CCR|ContentType"`, etc.) and gate commands are
   copy-pasteable.
6. **Reference source available** — `tmp/headroom/crates/headroom-core/src/` contains the Rust transforms (
   `log_compressor.rs`, `diff_compressor.rs`, `smart_crusher/`, `adaptive_sizer.rs`, etc.).

### Weaknesses

1. **Scope vs. "compact port" framing** — Research estimates ~14.5k LOC of Rust across 18+ smart_crusher files,
   live_zone, tag_protector, etc. The spec ports the compression pipeline but understates integration surface (config
   migration, ADK callback signature change, tool registration, TUI).
2. **False parallelism claim** — PROMPT/outline assert slices 1–9 are "fully independent" and parallelizable. `plan.md`
   contradicts this: slices 4–7 depend on 1+3, slice 5 on 2, slice 8 on 2, slice 9 on 1. Only slices 1–3 are truly
   foundational-isolated.
3. **"Independent" definition is internally inconsistent** — Plan says slices compile "without other slices' files being
   present" but also admits Slice 2 imports Slice 1's `ContentType` and algorithm slices import `CCRStore`. In a single
   Go package, you cannot merge slice 4 without slice 1+3 on the branch.
4. **CompressionContext plumbing is underspecified** — `CompressionContext.Query` is critical for SearchCompressor and
   SmartCrusher scoring, but only slice 10 mentions it ("from last user prompt, if available"). No design for how an
   `AfterToolCallback` obtains the current user query from ADK session state.
5. **Parity target is ambitious relative to deliberate deviations** — PROMPT mandates SHA-256 and `strings.Contains`
   instead of BLAKE3 and aho-corasick, yet requires byte-for-byte parity. Design correctly notes hash algorithm doesn't
   affect fixture assertions — but SmartCrusher fixtures embed CCR markers like `<<ccr:89f81e97033e 15_rows_offloaded>>`
   in expected `compressed` output, which are BLAKE3-derived from the Python/Rust recorder. Implementers must understand
   which fields are stable vs. hash-dependent.
6. **No fixtures for several algorithms** — SearchCompressor, LogTemplate, JsonMinifier, orchestrator, and CCR store
   have no parity fixtures; only table-driven tests are specified. This is fine but weakens the "full parity"
   requirement for those components.
7. **Subagent execution model is risky** — SUMMARY.md's likely-cause analysis is credible: parallel subagents in
   isolated worktrees can produce fixture copies without Go sources. The spec encourages this failure mode.

---

## Contradictions / Gaps

### Document contradictions

| Topic              | `requirements.md`                          | `design.md` / `PROMPT.md` / `plan.md`   | Resolution needed                                           |
|--------------------|--------------------------------------------|-----------------------------------------|-------------------------------------------------------------|
| CCR hash           | BLAKE3, 24 hex chars (req #6)              | SHA-256 truncated to 24 hex (stdlib)    | Design/PROMPT win in practice; **requirements.md is stale** |
| Keyword detection  | aho-corasick (Q1, req #5)                  | `strings.Contains`, no aho-corasick dep | PROMPT constraints win; **requirements.md is stale**        |
| Slice independence | "no cross-stage dependencies" (req #3, Q7) | Plan lists explicit deps for slices 4–9 | **Outline/PROMPT claim is wrong**; plan is accurate         |
| Tool name          | `headroom_retrieve`-style (req #7)         | `retrieve_compacted` (design, plan)     | Minor naming drift                                          |

### Technical gaps

1. **LLM query → `CompressionContext`** — SearchCompressor scores matches using `ctx.Query` word overlap; SmartCrusher
   uses query for anchor selection. Slice 10 says "from last user prompt (if available)" but does not specify:
    - Which ADK/session API provides the prompt
    - Whether compaction runs with `Query=""` when unavailable (fixtures use `"query": ""`)
    - Whether `TokenBudget` comes from model context window or a config field

2. **Offload wrapper types** — Slice 10 references `LogOffload`, `DiffOffload`, `JsonOffload`, `SearchOffload` wrapping
   the compressors, but these types are not defined in slices 4–7. Either slice 10 must define them or slices 4–7 must
   implement `OffloadTransform` directly.

3. **Fixture schema variance** — Log fixtures use `output.compressed`; SmartCrusher uses `input.content` +
   `input.query` + `output.compressed`; content_detector fixtures differ again. Plan should document a shared loader
   helper (the follow-up spec `features/TOO/006-...` addresses this more explicitly).

4. **Headroom is Python + Rust, not Rust-only** — `tmp/headroom/` is a hybrid repo (Python `headroom/` package + Rust
   `crates/headroom-core`). Parity fixtures were recorded from the Python parity harness (`tests/parity/recorder.py`).
   The spec says "Rust reference" but fixtures may reflect Python behavior. Only `diff_compressor` and `smart_crusher`
   have dedicated `*_rust_parity.py` tests upstream; log_compressor and content_detector do not.

5. **Breaking config migration** — Requirement #8 allows breaking changes but provides no migration path,
   default-mapping from old `MaxChars`/`MaxLines` to new per-algorithm configs, or example `config.json` diff.

### Parity strategy realism

**Partially realistic, with caveats:**

- **Realistic for:** log_compressor, diff_compressor, smart_crusher, content_detector — 85 committed fixtures with
  stable `compressed`/`type` fields, recorded from headroom's parity harness.
- **Not realistic as stated for:** full pipeline byte-for-byte (orchestrator gating, parallel phase ordering, CCR store
  lifecycle) — no fixtures exist.
- **Risk:** `strings.Contains` vs aho-corasick can change match boundaries on edge-case lines, breaking keyword-scored
  outputs even when core algorithms match.
- **Risk:** JSON minification field ordering — Go's `encoding/json` may emit different key order than Rust/Python for
  object arrays, affecting SmartCrusher `compressed` strings unless canonical ordering is enforced.

---

## Orphaned Artifacts

### `internal/tools/testdata/parity/` — confirmed orphaned

| Directory           | Fixture count | Consumer in `*_test.go` |
|---------------------|---------------|-------------------------|
| `content_detector/` | 21            | ❌ None                  |
| `log_compressor/`   | 20            | ❌ None                  |
| `diff_compressor/`  | 27            | ❌ None                  |
| `smart_crusher/`    | 17            | ❌ None                  |
| **Total**           | **85**        | **0 references**        |

- **Git status:** All 85 files are tracked (committed in `4d4db3f`), not untracked.
- **`grep -rln "testdata/parity" internal/`** → zero matches.
- **Size:** ~436 KB of dead testdata that implies implementation exists.
- **Sample fixture** (`smart_crusher/dict_array_30_*.json`): includes `input.query`, nested `input.content`, and
  `output.compressed` with embedded `<<ccr:...>>` markers — a harness must parse this schema before any parity test can
  run.

**Recommendation:** Either delete until slice 4+ lands, or land slice 1's `ccr_test.go` + a shared `parity_testutil.go`
in the same PR that first consumes fixtures.

### `tmp/headroom/` — reference only

- **Present:** Full headroom checkout with Rust crates at `crates/headroom-core/src/transforms/` (including
  `smart_crusher/` with 20+ `.rs` files) and Python package at `headroom/`.
- **Git status:** Untracked under `tmp/` (correct — reference material, not delivered work).
- **Usable for port:** Yes, but implementers should cross-check Python parity tests in
  `tmp/headroom/tests/test_transforms/` since fixtures were recorded from Python.

---

## Recommendations

### 1. Do not re-run as parallel subagents without revision

The failed attempt aligns with the spec's parallelism guidance. Re-attempting slices 1–9 in parallel worktrees without
explicit merge gates will likely reproduce fixture-only delivery.

### 2. Revise the spec before re-implementation (high value, low cost)

Minimum revisions:

| Item                                     | Action                                                                                                   |
|------------------------------------------|----------------------------------------------------------------------------------------------------------|
| `requirements.md`                        | Align BLAKE3→SHA-256 and aho-corasick→`strings.Contains` with PROMPT constraints                         |
| `outline.md` / PROMPT slice independence | Replace "fully independent" with the dependency graph from `plan.md`                                     |
| `design.md`                              | Add **CompressionContext wiring** section: ADK callback → session prompt extraction, fallback `Query=""` |
| `plan.md`                                | Add shared `parity_load_test.go` helper; define offload wrapper ownership (slice 5 vs 10)                |
| `SUMMARY.md`                             | Keep outcome as **failed** until Go sources exist                                                        |

Consider adopting the more detailed follow-up at
`specs/features/TOO/006-specs-tools-002-specs-tools-001-headroom-update/` — it adds `research/parity-fixtures.md` with
schema documentation and tighter slice gates.

### 3. Recommended implementation sequence

```
Phase A (serial, one branch):
  Slice 1 → Slice 2 → Slice 3
  Gate: parity loader + ccr/content_type tests

Phase B (can parallelize after A merges, same branch):
  Slices 4, 5, 6, 7, 8 — each PR adds algorithm + parity tests

Phase C (integration):
  Slice 9 (retrieve tool) → Slice 10 (config/callback) → Slice 11 (TUI)

Per-slice rule: no slice merges without its `-run` gate passing AND
parity fixtures wired (where applicable).
```

### 4. Handle orphaned fixtures

**Option A (preferred):** Keep fixtures; add `internal/tools/parity_testutil.go` in slice 1 or 4 that loads JSON and
asserts `output.compressed` / `type` fields. First green parity test proves the harness works.

**Option B:** Delete the 85 fixtures until algorithms land, to avoid false signals. Re-copy from
`tmp/headroom/tests/parity/fixtures/` when needed.

### 5. Scope reduction if full port is too large

If ~14.5k LOC Rust port is infeasible in one effort, consider a phased product rollout:

1. **MVP:** CCR store + content detection + LogCompressor + DiffCompressor (highest token savings, 47 parity fixtures)
2. **Phase 2:** SmartCrusher + SearchCompressor
3. **Phase 3:** Orchestrator bloat-gating + retrieve tool + config break

Document which acceptance criteria defer to each phase.

### 6. Verification checklist for "done"

Before marking SUMMARY **completed**, require:

- [ ] All 15+ new `.go` files from plan exist
- [ ] `go test ./internal/tools/... -run "CCR|ContentType|LogCompressor|Diff|SmartCrusher"` passes
- [ ] At least 85 parity subtests green (or documented exceptions)
- [ ] `CompactorConfig` nested structure in `compactor.go` and `internal/config/config.go`
- [ ] `retrieve_compacted` registered and session-scoped `CCRStore` wired in `cli.go`
- [ ] `/rtk stats` test expectations updated

---

## Summary

| Question                                | Answer                                                                                                                           |
|-----------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| Is SUMMARY.md "failed" verdict correct? | **Yes — confirmed.**                                                                                                             |
| Partial delivery?                       | **No** — only orphaned parity fixtures (~436 KB JSON).                                                                           |
| Is spec implementable?                  | **Yes**, with revisions to resolve requirements contradictions and CompressionContext gap.                                       |
| Are 11 slices truly independent?        | **No** — dependency graph is 1→2→{4,5,8}, 1+3→{4,5,6,7}, 1–9→10→11.                                                              |
| Is byte-for-byte parity realistic?      | **For 4 algorithms with fixtures, mostly yes**; for full pipeline and keyword edge cases, **risky without explicit exceptions**. |
| Best path forward?                      | **Revise spec → serial Phase A → algorithm slices with parity harness → integration slice 10.**                                  |
