# Requirements — RTK Compactor Phase 2

## Context

`specs/research/000-rtk-hooks-optimizer/` shipped the Go-native output compactor as an ADK `AfterToolCallback`. The
12-step plan is complete; all 9 stages land in `internal/tools/compactor_*.go`. `requirements.md` of the original spec
explicitly deferred **command rewriting (BeforeToolCallback)** to a later phase and limited scope to output compaction.

This Phase 2 spec addresses the **remaining drift** between the shipped compactor and the original spec, plus a few
high-value gaps identified by a follow-up audit against upstream `rtk-ai/rtk` (`tmp/rtk/`).

## Out of Scope (Deferred)

- **Command rewriting before execution** (`BeforeToolCallback`). The original spec deferred this; we keep it deferred.
  Would mean rewriting `cat foo.go` → `read foo.go`, `ls -la` → `tree`, etc. Substantial new surface (shell tokenizer,
  rewrite registry, suggestion UX). Track in a future Phase 3 spec.
- **`learn` module** (repeated CLI-mistake detection). Geniunely new product, not parity work.
- **TUI compaction indicator on per-message tool output** — metrics are recorded; the per-message badge was in the
  original design but never rendered. Trivial to add, scoped into a later slice.
- **JSON / log-dedup filters** — large standalone filter; not on the highest-value path.
- **Subagent / hook to invoke external `rtk` binary** — we don't ship a CLI proxy and have no plans to.

## Drift Items from Original Spec

| Item                      | Original spec (000)                             | Current                                              | Gap                                                                       |
|---------------------------|-------------------------------------------------|------------------------------------------------------|---------------------------------------------------------------------------|
| Test aggregation          | go + pytest + jest + vitest + cargo             | go-only regex (others fall through to hard-truncate) | pytest / cargo output not aggregated                                      |
| Source filter             | language-aware via `detectLanguage()`           | single naive `//` and `/* */` rule                   | wrong for Python/Ruby/Lua/Haskell; strips Go doc comments in minimal mode |
| Read pipeline             | skip files <80 lines, respect offset/limit args | operates on whole content unconditionally            | may over-compact a targeted read                                          |
| TUI per-message indicator | `compactInfo` field on TUI message              | metrics recorded; field not rendered                 | user can't see savings per tool call                                      |

## New High-Value Gaps (Audit of `tmp/rtk/`)

| Gap                                                            | Why it matters                                                                                                                                                                                               |
|----------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Bash `compactBash` only detects test/build/git/linter commands | `ls`, `cat`, `find`, `env`, `docker`, `kubectl`, `pnpm`, `vitest`, `playwright`, `curl`, `df`/`du`/`ps`/`wc`, `mypy`, `ruff`, `prettier` all hit the same generic pipeline (ANSI strip + hard truncate only) |
| `never_worse` token-aware guard                                | current `compSize >= origSize` is byte-only; upstream uses `bytes/4` token estimate, which catches "filter added metadata overhead" bugs that byte checks miss on short outputs                              |
| Tee overflow for hard truncation                               | when we drop 95% of output, the user often needs the raw form. Upstream writes overflow to `~/.local/share/rtk/tee/<slug>.log` and prints a one-liner pointer                                                |
| `deps` filter for read of lockfiles                            | `read package.json` / `read Cargo.toml` / `read go.mod` is a common pattern; current filter doesn't help                                                                                                     |
| Per-command metrics in `/rtk`                                  | current stats aggregate per tool only; the actual command (`git log -10`) is not recorded, so user can't see which commands dominated savings                                                                |

## Q1: Scope Priority

We have 5–6 candidate items (drift + new gaps). Ship all in one spec, or phase them?

- **A) All in one spec** — single 8–10 step plan, all merged together.
- **B) Two specs** — Spec 1 ships the high-ROI bash command detection expansion + language-aware source filter + tee
  overflow (the three with the biggest token savings). Spec 2 ships the rest (per-command metrics, `deps` filter,
  pytest/cargo aggregation, `never_worse`).

**A1:** Two specs. Spec 1 = highest token savings, ship in ≤3 weeks. Spec 2 = quality-of-life, ship later. This spec =
Spec 1.

---

## Q2: Language-Aware Source Filter

Upstream's `core/filter.rs` has a `Language` enum and per-language comment patterns. How aggressive should we be?

- **A) Full parity with upstream** — Rust, Python, JS, TS, Go, C, C++, Java, Ruby, Shell, Lua, Haskell, Lisp, data
  formats (skip stripping). ~250 LOC + tests.
- **B) Top 5 only** — Go, Python, JS/TS, Rust, Shell. ~120 LOC.
- **C) Extension only** — keep current filter, but add Lua/Haskell/Ruby via opt-in config.

**A2:** Full parity (A). The marginal cost is small and the data-format branch (JSON/YAML/TOML/etc.) is a free win —
never strip anything from data files.

---

## Q3: Tee Overflow

When hard-truncation drops >50% of bytes, should we write the raw output to disk and print a pointer?

- **A) Always on** — any time `hardTruncate` triggers, also tee.
- **B) Threshold** — only when `origSize > maxChars * 2` (i.e. we're dropping at least 50%).
- **C) Opt-in via config** — disabled by default, users enable in `compactor.tee_enabled`.
- **D) Skip** — too speculative.

**A3:** Threshold + opt-out (B with a config flag). Default `tee_enabled: true`, fires when dropping >50%. Path:
`~/.pi-go/sessions/<session-id>/tee/<slug>-<ts>.log`. Pointer line appended to compacted output. Rotate oldest, keep 20
files per session.

---

## Q4: Bash Command Detection Expansion

Current `compactBash` only routes 4 command categories. New detectors:

- `isLsCommand` → group by directory, drop noise dirs (`.git`, `node_modules`, `target`, `__pycache__`, `vendor`,
  `.next`, `dist`, `venv`, `.venv`, `build`)
- `isCatCommand` → for `.go`/`.py`/`.js`/`.ts`/`.rs` files, apply source filter; for `.json`/`.jsonl`, apply JSON
  compact; for `.log`, apply log dedup
- `isFindCommand` → group by directory, limit to 100 results
- `isEnvCommand` → categorise PATH/LANG/cloud/tool vars, truncate long values to 50 chars + length
- `isDockerCommand` → for `docker ps/images/logs`, summarise columns; for `docker logs`, tail-style dedup
- `isKubectlCommand` → for `get -o wide`, drop READY/STATUS columns; for `logs`, tail dedup
- `isCurlCommand` → if response is JSON, apply JSON compact; if HTML, strip tags; default = hard truncate
- `isPackageManagerCommand` → for `pnpm install`, `npm install`, `cargo install`, keep only final summary line
- `isProcessCommand` → for `ps aux`, drop `RSS`/`VSZ` columns; for `df`, drop `Use%`; for `du`, top-N dirs
- `isMypyCommand`, `isRuffCommand`, `isPrettierCommand` → dedicated per-linter filters (better than generic aggregation)

All new detectors respect existing `compactor.*.enabled` toggles plus a per-detector `compactor.detect_<name>` toggle
defaulting on.

**Q4:** Ship all of the above in this spec? Or split?

- **A) All** — single big bash-pipeline PR.
- **B) Top 5** — `ls`, `cat`, `find`, `env`, `docker`. Defer kubectl/curl/process/myky/ruff/prettier to Spec 2.
- **C) Top 3** — `ls`, `cat`, `find` only. Defer everything else.

**A4:** Top 5 (B). The remaining 5 are smaller wins and easy to add later.

---

## Q5: TUI Per-Message Compaction Indicator

Spec 000 called for `compactInfo string` on the TUI `message` struct. We never landed it. Add now?

- **A) Yes, ship it.** — Render as a small suffix in the TUI tool output: `[compacted 85% · ansi,test-agg]`. Not in the
  LLM-bound result.
- **B) Defer** — `metrics.compactInfo` is already accessible; render in a later UI pass.

**A5:** Yes (A). ~40 LOC, matches the original spec.

---

## Q6: `never_worse` Guard

Replace byte-only `compSize >= origSize` with token-aware check (`bytes/4`)?

- **A) Yes** — port upstream `core/guard.rs::never_worse`. Apply to every compaction function return.
- **B) No** — current byte check is "good enough"; the false-positive rate is low.

**A6:** Yes (A). Catches a real class of bugs (e.g. smart-truncate on a 50-line output that adds 30 lines of "lines
omitted" markers). ~30 LOC.

---

## Q7: Read Tool — Skip Short Files and Respect Args

Spec 000 Step 7 said "skip files <80 lines, explicit range reads (offset/limit in args)". Add now?

- **A) Both** — skip if `len(content) < 80 lines` AND if `args` has `offset` or `limit` keys, pass through (don't
  compact targeted reads).
- **B) Short files only** — skip if short; always compact if long, even with offset/limit.

**A7:** Both (A). Targeted reads (offset/limit) should never be compacted — the user asked for specific lines.

---

## Q8: Naming / Backwards Compatibility

All new functionality lives under `internal/tools/compactor_*.go`. New config keys under `compactor.*` in `config.json`.
New tee dir under existing `~/.pi-go/sessions/<id>/tee/`. No slash-command or TUI renaming. Acceptable?

- **A) Yes, no naming changes.**
- **B) Rename `/rtk` to something more descriptive** (e.g. `/compact-stats`).

**A8:** A. Keep `/rtk`. The 011 spec already renamed `ollama-com-direct-api` so renaming is fine when warranted; here it
isn't.

---

## Q9: Test Coverage Bar

`compactor_test.go` is currently 1436 LOC, ~30 test functions. New code:

- **A) Maintain ratio** — new code ships with ≥1 test per public function and ≥3 test cases per filter.
- **B) Higher bar** — every new filter ships with both positive and negative fixtures + a benchmark.

**A9:** B. Compaction correctness is the whole point; new filters that aren't measured aren't trusted.

---

Requirements clarification complete. Spec scope is locked: drift fixes (Q2, Q7, Q5), high-ROI bash detectors (Q4 = top
5), tee overflow (Q3), `never_worse` guard (Q6).
