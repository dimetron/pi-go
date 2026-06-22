# Research — Parity Fixtures

## Source

Verified reads of `/Users/dimetron/p6s/pi-dev/pi-go/internal/tools/testdata/parity/`
and `/Users/dimetron/p6s/pi-dev/pi-go/tmp/headroom/tests/parity/fixtures/`.
Direct `diff -rq` comparison.

## 1. Counts & Sizes

- pi-go copy: **85 JSON files**, **436K**, 4 subdirectories:
    - `content_detector/` — 21 files (84K)
    - `diff_compressor/`   — 27 files (124K)
    - `log_compressor/`    — 20 files (112K)
    - `smart_crusher/`     — 17 files (116K)
- upstream: 171 files, 8 subdirectories
- 4 upstream-only dirs not in pi-go: `cache_aligner/` (20),
  `ccr/` (25), `codex_openai_contracts/` (1, a JSON Schema not a recording),
  `tokenizer/` (40)
- **`diff -rq` returns 4 lines, all "Only in upstream" — zero file-level
  diffs across the 4 shared subdirs. The pi-go copy is byte-identical to
  upstream for what it has.**

## 2. Canonical Schema (`tests/parity/recorder.py` lines 13–24)

```json
{
  "transform":      "log_compressor",
  "input":          "<original input>",
  "config":         { "max_total_lines": 100, ... },
  "output":         { ... serialized result fields ... },
  "recorded_at":    "2026-04-23T00:00:00Z",
  "input_sha256":   "<hex digest of canonicalized input>"
}
```

Fixture filename is `sha256({transform, input, config, fn})[:16].json`.

Smart-crusher has an extended schema (extra `"label"` field + nested
`"input": {content, query, bias}`):

```json
{
  "transform":   "smart_crusher",
  "label":       "dict_array_100_sequential",
  "input":       { "content": "[{...}]", "query": "", "bias": 1.0 },
  "config":      { ... 15 keys ... },
  "output":      { "compressed": ..., "original": ..., "was_modified": true, "strategy": "smart_sample(100->15)" },
  "recorded_at": "2026-04-27T06:59:40.005010+00:00",
  "input_sha256": "b91d1fff5ba3..."
}
```

## 3. Per-Directory Schema Details

### `content_detector/` (21 files)

```json
{
  "transform": "content_detector",
  "config": {},
  "input": "<raw text/diff/HTML/JSON>",
  "output": { "confidence": 1.0, "content_type": "diff|html|source_code|json_array|search|text|build",
              "metadata": { ... per-type stats ... } },
  ...
}
```

**Config is empty** — content_detector takes no config.

### `diff_compressor/` (27 files)

```json
{
  "transform": "diff_compressor",
  "config": {
    "always_keep_additions": true, "always_keep_deletions": true,
    "enable_ccr": true, "max_context_lines": 2, "max_files": 20,
    "max_hunks_per_file": 10, "min_lines_for_ccr": 50
  },
  "input": "<unified diff string>",
  "output": {
    "compressed": "<...>",
    "original_line_count": 13, "compressed_line_count": 13,
    "files_affected": 0, "additions": 0, "deletions": 0,
    "hunks_kept": 0, "hunks_removed": 0, "cache_key": null
  },
  ...
}
```

Output uses **9 fields**: `compressed`, `original_line_count`,
`compressed_line_count`, `files_affected`, `additions`, `deletions`,
`hunks_kept`, `hunks_removed`, `cache_key` (str|null).

### `log_compressor/` (20 files)

```json
{
  "transform": "log_compressor",
  "config": {
    "dedupe_warnings": true, "enable_ccr": true, "error_context_lines": 3,
    "keep_first_error": true, "keep_last_error": true,
    "keep_summary_lines": true, "max_errors": 10, "max_stack_traces": 3,
    "max_total_lines": 100, "max_warnings": 5, "min_lines_for_ccr": 50,
    "stack_trace_max_lines": 20
  },
  "input": "<raw log text>",
  "output": {
    "compressed": "...", "original": "...",
    "original_line_count": 5, "compressed_line_count": 5,
    "compression_ratio": 1.0, "format_detected": "generic",
    "cache_key": null, "stats": {}
  },
  ...
}
```

Output uses **8 fields**: `compressed`, `original`, `original_line_count`,
`compressed_line_count`, `compression_ratio`, `format_detected`,
`cache_key`, `stats`.

`format_detected` is one of: `pytest`, `npm`, `cargo`, `jest`, `make`,
`generic`.

### `smart_crusher/` (17 files)

Config has **15 keys**:

```
enabled, dedup_identical_items, factor_out_constants, first_fraction,
last_fraction, include_summaries, max_items_after_crush, min_items_to_analyze,
min_tokens_to_crush, preserve_change_points, similarity_threshold,
toin_confidence_threshold, uniqueness_threshold, use_feedback_hints,
variance_threshold
```

Output uses **4 fields**: `compressed`, `original`, `was_modified`,
`strategy`. The `strategy` field is one of:
`smart_sample(N->M)`, `cluster_sample(...)`, `top_n(...)`, `passthrough`,
`time_series(...)`, `factor_constants`.

The smart_crusher `compressed` field contains the
`{"_ccr_dropped":"<<ccr:HASH N_rows_offloaded>>"}` marker format when CCR
is used.

## 4. Upstream Test Harnesses (Python)

- `tests/test_transforms/test_diff_compressor_rust_parity.py` — 120 LOC
- `tests/test_transforms/test_smart_crusher_rust_parity.py` — 92 LOC
- `tests/parity/recorder.py` — 850 LOC (canonical schema enforcer)
- `tests/parity/record_smart_crusher.py` — 213 LOC (smart_crusher schema variant)

**No upstream `test_log_compressor_rust_parity.py` or
`test_content_detector_rust_parity.py`** — log_compressor and
content_detector fixtures are recorded but only have ad-hoc validation
(integration via the SmartCrusher / full pipeline tests).

## 5. Upstream-Only Fixture Directories (NOT in pi-go copy)

| Dir                       | Files | Size |      Recording type?       |  Useful for pi-go port?  |
|---------------------------|------:|-----:|:--------------------------:|:------------------------:|
| `cache_aligner/`          |    20 |  80K |  yes (transcript in/out)   | no — out of scope for v1 |
| `ccr/`                    |    25 | 100K | yes (tool def list in/out) | no — out of scope for v1 |
| `codex_openai_contracts/` |     1 |  16K |    **no — JSON Schema**    |            no            |
| `tokenizer/`              |    40 | 160K |      yes (text → int)      | no — out of scope for v1 |

None of these are needed for the planned 6-algorithm port. They correspond
to pipeline features (`cache_aligner`, `ccr_tool_injector`, tokenizer
adapter) that the previous spec explicitly excluded from v1.

## 6. CCR Marker Format (verified)

Two formats observed:

1. **In-line within JSON output** (smart_crusher):
   ```json
   {"_ccr_dropped":"<<ccr:1d0dd94cf2cd 85_rows_offloaded>>"}
   ```

2. **At end of compressed text** (diff_compressor, log_compressor):
   ```
   [N lines compressed to M. Retrieve more: hash=HASH]
   ```

The Go port should emit the same marker strings. Headroom uses `marker_for`
which formats `<<ccr:{hash}>>`. The Go equivalent (per Q4) prepends the
retrieval header `<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>`.

## 7. Other pi-go Testdata Directories

```
$ ls /Users/dimetron/p6s/pi-dev/pi-go/internal/tools/testdata/
parity/
```

`parity/` is the **only** subdirectory. No `diff/`, `logs/`, or other
fixture categories.

## 8. No Go Code Consumes the Fixtures Today

`grep -rln "testdata/parity" internal/` → no matches.
`grep -rln "testdata" internal/tools/ --include="*.go"` → no matches.

**No Go test file currently references `testdata`.** The 85 fixtures are
present but unreferenced by Go code. Wiring them into Go tests is a
prerequisite for byte-for-byte parity.

## 9. Fixture File Lists

### `content_detector/` (21 files)

```
247811aecfdec556.json
37c86907c057e293.json
3a0b14d2dea7c876.json
3ba74a1f57c5351e.json
4efe946303371867.json
53400da2b7dd2428.json
552d30c30c2a6793.json
5e8e1bc29f6c71c5.json
628c8985c40adb7d.json
67f4b1a1697c8aa9.json
70fbb444a583fe3c.json
71d648a39e151a72.json
79e6b68a05a1c639.json
7e145153192c11a1.json
7fde660489bc47aa.json
837b272f1604eaaa.json
9fb76321d31ed64c.json
bc4488ce189d1c86.json
bf9b9ceb4b70dcd0.json
cabc55445af63e2f.json
d100a9ac7a3671da.json
```

### `diff_compressor/` (27 files)

```
066bc82007dc2dd4.json, 0cf5e189bd42fab1.json, 10f0106f40cbe2fa.json,
1a4e35c597c7a001.json, 1bdfa041108f0aad.json, 1eeb1f7ae64c7744.json,
2c07a365f52d6fa6.json, 2cd095b132f5da3f.json, 2eb4b13b37f376d6.json,
433b2d25303645ed.json, 4844a5f9605b8425.json, 5d950a94b8c22480.json,
649fd65ad3511191.json, 66c86f64baebfc3f.json, 88b4d4a7e93014e5.json,
97bde326d96a1165.json, 9b9a03215a7198bf.json, b0b83d16366edfed.json,
b605bd89e234cbff.json, c6b6d5c9fd7f1ad2.json, c72662c635d1defa.json,
d0ba5cae330722be.json, d0e62d2f1493bd72.json, d4ed44a6fc667358.json,
da9afe00e9846ce4.json, e9c505e42b2b2edc.json, ea99ab1ea7449acf.json
```

### `log_compressor/` (20 files)

```
012c7bbeb96d95e9.json, 1fbaf88e12686704.json, 23112ae8ae41ba59.json,
305987ea77c385e6.json, 349121068ecbb26e.json, 3bc015edc0a36387.json,
3bf3f8bd70c80dad.json, 5533a8264a04dd61.json, 566491aacda8af92.json,
629d38929748b41e.json, 65bd6228add102ac.json, 79b59341f8159b3e.json,
7cd0d60fe9f4aad2.json, 9655ffc91d79aa47.json, aa433e41aafed34e.json,
c472d986fe3b3192.json, cbf376c989bdc9be.json, d01455afa162c961.json,
d7e1352bc6d3df4d.json, ef61ab0e0029aa96.json
```

### `smart_crusher/` (17 files)

```
dict_array_100_sequential_b91d1fff5ba3.json
dict_array_30_b0ba1d26fd03.json
dict_array_30_bias_high_dc3b36e60560.json
dict_array_30_bias_low_a850512901bd.json
duplicate_dicts_40_40a0670dec17.json
empty_array_41ecf20098c5.json
mixed_array_3f929019b62f.json
nested_3deep_with_array_bb5036c153aa.json
nested_object_with_array_b66d43299ea1.json
non_json_passthrough_4322abf677f3.json
nulls_and_bools_06d6fd6adcab.json
number_array_40_changepoint_8cd416c24d53.json
short_array_passthrough_aeb16224b61f.json
small_object_passthrough_801afa36a32c.json
string_array_25_6fe4a307de81.json
time_series_50_25cd28df5a50.json
unicode_dict_array_4820ed6c0c9a.json
```

## 10. Outstanding Observations

- **The previous spec did not include `test_log_compressor_parity.go`** —
  upstream has no `test_log_compressor_rust_parity.py` either, but
  downstream parity can still be verified by writing a Go test that loads
  the fixtures the same way `test_diff_compressor_rust_parity.py` does.
- **No parity fixtures exist for SearchCompressor, LogTemplate, JsonMinifier,
  or DiffNoise** — these algorithms can only be verified by table-driven
  Go tests with hand-crafted inputs.
- **No parity fixtures exist for the orchestrator or CCR store** —
  `CompressionPipeline.run` and `CcrStore.put/get` are exercised
  end-to-end by the algorithm tests, not standalone.
- **The `log_compressor` fixtures all use the same config** (defaults) —
  only `input` varies. Useful for round-trip verification but doesn't
  exercise alternative configs.
- **The `smart_crusher` fixtures have rich label names** describing the
  scenario (e.g., `dict_array_30_bias_high`, `time_series_50`) — these
  are good documentation hooks for Go test names.
- **Hash algorithm deviation (BLAKE3 → SHA-256)** has no effect on parity
  because fixtures store `input_sha256` (SHA-256 of the input bytes), not
  CCR cache keys.