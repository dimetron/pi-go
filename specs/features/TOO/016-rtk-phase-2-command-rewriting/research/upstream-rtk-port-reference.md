# Phase 2 Reference Research

This directory indexes the upstream `rtk-ai/rtk` reference files that inform the Phase 2 design. Local copies are in
`tmp/rtk/` (read-only, not under version control for the spec).

## Upstream Files to Port

| Upstream file                         | Lines | What we port                                                                                          | Target file                                    |
|---------------------------------------|-------|-------------------------------------------------------------------------------------------------------|------------------------------------------------|
| `tmp/rtk/src/core/filter.rs`          | 600+  | `Language` enum, `CommentPatterns`, `FilterLevel`, `FilterStrategy` trait, `Language::from_extension` | `internal/tools/compactor_lang.go`             |
| `tmp/rtk/src/core/guard.rs`           | ~80   | `estimate_tokens` (bytes/4), `never_worse` invariant                                                  | `internal/tools/compactor_guard.go`            |
| `tmp/rtk/src/core/tee.rs`             | ~250  | Slug sanitisation, file rotation, env-var override, default path                                      | `internal/tools/compactor_tee.go`              |
| `tmp/rtk/src/cmds/system/ls.rs`       | ~300  | `compact_ls` directory grouping, `NOISE_DIRS` list                                                    | reference only — our implementation is simpler |
| `tmp/rtk/src/cmds/system/env_cmd.rs`  | ~150  | `compact_env` categorisation (PATH/LANG/cloud/tool)                                                   | reference only — port the categorisation logic |
| `tmp/rtk/src/cmds/system/find_cmd.rs` | ~300  | `compact_find` directory grouping, max_results                                                        | reference only — port the grouping logic       |

## Port Decisions

- **filter.rs**: port the `Language` enum, `Language::from_extension`, and `CommentPatterns` types and functions. **Do
  not** port the `FilterStrategy` trait (over-engineered for our use case — we have one filter function with a level
  parameter).
- **guard.rs**: port verbatim. `estimate_tokens` and `never_worse` semantics are clean and well-tested upstream.
- **tee.rs**: port the slug-sanitisation and rotation logic. Replace env-var override with our config field. Default
  path is `~/.pi-go/sessions/<id>/tee/` (per-session) instead of `~/.local/share/rtk/tee/` (global).
- **ls.rs / env_cmd.rs / find_cmd.rs**: read for categorisation/grouping logic; write Go-native versions that fit our
  existing detector/filter pattern. Don't port the Rust-specific `regex`/`LazyLock` patterns.

## Things We Don't Port

- **`tmp/rtk/src/discover/rules.rs`** — registry of 87 command rewrite rules. We do **not** implement command rewriting
  in Phase 2 (deferred to Phase 3).
- **`tmp/rtk/src/discover/lexer.rs`** — shell tokenizer (quote-aware, escape-aware, sed-aware). Would be needed for
  Phase 3 only.
- **`tmp/rtk/src/cmds/system/json_cmd.rs`** — JSON compact with `--max-depth` and `--keys-only`. Deferred to a later
  spec; high-value but a standalone filter.
- **`tmp/rtk/src/cmds/system/log_cmd.rs`** — log dedup with timestamp/UUID/hex/path normalisation. Deferred.
- **`tmp/rtk/src/learn/`** — repeated CLI-mistake detection. Different product surface; not in scope.
- **`tmp/rtk/src/analytics/`** — SQLite-backed usage tracking. We use JSONL-in-session-dir; no need for a separate
  analytics system.

## Local Snapshot

The `tmp/rtk/` directory is a read-only clone of `rtk-ai/rtk` at v0.42.4 used for reference. It is **not** part of the
pi-go spec; it exists purely so the implementing agent can read upstream patterns without a network round trip.

If `tmp/rtk/` is ever removed from the working tree, the implementing agent should fetch upstream directly:

- Repo: https://github.com/rtk-ai/rtk
- Tag: v0.42.4 (or newer within the same major)
- Files of interest: `src/core/filter.rs`, `src/core/guard.rs`, `src/core/tee.rs`
