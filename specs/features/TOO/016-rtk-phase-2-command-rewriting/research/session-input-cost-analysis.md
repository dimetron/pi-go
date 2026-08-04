# Session Input-Cost Analysis

Data source: `~/.pi-go/sessions/260804-*/events.jsonl` (pi-go session logs for 2026-08-04).
Per-call `UsageMetadata.promptTokenCount` / `candidatesTokenCount` aggregated per session.

## Top-line

30 non-empty sessions, **~31.3M prompt (input) tokens** vs **~0.2M output tokens** — input dominates by ~150:1.
Input (not output) is where the token budget goes.

## Top sessions by input cost

| Session      | Prompt tokens | Output tokens | LLM calls | Max context | Task / workdir                                        |
|--------------|---------------|---------------|-----------|-------------|-------------------------------------------------------|
| `1243-b7764` | 14,577,064    | 38,784        | 174       | 128,522     | "verify in test for skill dynamic activation" — pi-go |
| `1457-42d3c` | 6,582,935     | 37,324        | 68        | 136,674     | "fix linter errors" — pi-go                           |
| `1444-c1dbf` | 5,204,635     | 16,906        | 45        | 173,330     | "/plan qmd-go.md standalone CLI spec" — ai-eng-course |
| `1430-dffaa` | 1,157,742     | 9,307         | 13        | 151,711     | rtk implementation review — pi-go                     |
| `0914-16fad` | 851,747       | 3,573         | 14        | 91,380      | —                                                     |

- The single `1243` session is ~47% of the day's total input spend; the top 4 sessions are ~88%.
- All top sessions use `minimax-m3` (cloud or local).

## Root cause: monotonic context replay

Each LLM call re-sends the entire accumulated conversation (all prior user turns + all prior tool results),
so prompt size ratchets upward with every tool call. In `1243` it climbs steadily from ~9K → 128K over 174 calls;
every step re-bills the full history. Sessions that push 130–170K and keep going are where spend explodes.

## Tool-output contribution (context bytes by tool type)

| Session      | `read`            | `bash`          | `ripgrep`  | `subagent` |
|--------------|-------------------|-----------------|------------|------------|
| `1243-b7764` | 102 KB (52 calls) | 70 KB (83)      | 59 KB (3)  | 2.9 KB     |
| `1457-42d3c` | 141 KB (29)       | 128 KB (71)     | 6.9 KB (1) | —          |
| `1444-c1dbf` | 7.4 KB (2)        | **402 KB (39)** | —          | —          |
| `1430-dffaa` | **324 KB (17)**   | 36 KB (14)      | 35 KB (1)  | —          |
| `0946-8accd` | 120 KB (48)       | 223 KB (108)    | 71 KB (3)  | 61 KB (2)  |

- **`read`** — biggest contributor in pi-go sessions; avg per call up to ~19 KB (`1430`), some whole-file dumps.
- **`bash`** — biggest in `1444`: 402 KB / 39 calls (avg ~10 KB) from build/test log output dumped into context.
- **`ripgrep`** — worst bytes-per-call ratio (~20–35 KB per invocation); results not capped/filtered.
- **`subagent`** — can be heavy (30 KB+ result in `0946`).

## Cost levers

1. Cut tool output re-entering context, especially `read` (use `offset`/`limit`) and `bash` (trim long build/test logs).
2. `ripgrep` — check whether results are capped/filtered; it's the worst bytes-per-call.
3. Compaction/summarization earlier — bounding the context ratchet at ~50–100K would cap per-call input size and total
   spend.

**Bottom line:** input cost is conversation replay (full-history re-billing every turn); the fuel is large
`read`/`bash`/`ripgrep` tool outputs in long, single-topic pi-go sessions (`1243`, `1457`, `1430`) plus one
`/plan` session (`1444`). This motivates the compaction work that Phase 2 builds on.
