# Research: Existing Evaluation Harness (`hack/test/tb2/`)

The repo already contains a **Python** evaluation harness for Terminal-Bench located at
`hack/test/tb2/`. It has **no automated tests** of any kind.

## Files and Responsibilities

| File                         | Purpose                                                                                                                                   | Size      |
|------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| `collect.py`                 | Analytics collector: runs pi/maki/claude-code/opencode headless, parses streamed JSON, computes token usage + cost, appends rows to a CSV | 549 lines |
| `analyze.py`                 | Reads the CSV and prints tables (run summary, tool usage, token efficiency)                                                               | 221 lines |
| `compare.py`                 | Runs `collect.py` across multiple agents concurrently, merges per-agent CSVs, then calls `analyze.py`                                     | 183 lines |
| `tbench_pi_agent.py`         | Harbor `BaseInstalledAgent` wrapper (`PiAgent`) that runs pi-go inside Terminal-Bench containers via `harbor run`                         | 373 lines |
| `tool_token_analysis.py`     | Reads pi-go session JSONL files from `~/.pi-go/sessions`, extracts tool calls, prints token-distribution tables/histograms                | 436 lines |
| `providers/openai-oauth/`    | (empty) provider hook directory                                                                                                           | —         |
| `jobs/2026-04-26__02-01-54/` | One historical Harbor job run (task `terminal-bench/fix-git`)                                                                             | —         |

## Key Functions Worth Testing

### collect.py

- `AGENTS = ("pi", "maki", "claude-code", "opencode")`
- `PRICING` dict (per-1M-token rates); `lookup_pricing(model_id)` (strips `provider/` prefix, prefix-matches);
  `compute_cost(usage, pricing)`
- `build_cmd_pi/maki/claude/opencode(args)` — build subprocess argv
- `process_pi_stream(proc, meta)` — parse pi-go `--mode json` JSONL events (`message_start`, `text_delta`, `tool_call`,
  `tool_result`, `thinking_delta`, `message_end`) → `(summary, turn_usage, all_tool_calls, result_text)`; estimates
  tokens from tool calls (input = `len(json.dumps(input).split())*2`, output = 50/tool + `len(result_text.split())*2`);
  computes cost via `lookup_pricing`
- `process_claude_stream(proc, meta)` / `process_opencode_stream(proc, meta)` — parse Claude Code / opencode stream JSON
- `process_init`, `process_assistant`, `process_result` helpers
- `format_tool_summary(block)`, `format_tool_detail(block)` — human-readable tool summaries (TOOL_DISPLAY_KEY map)
- `opencode_usage(tokens)`
- `CSV_FIELDS`, `usage_fields(usage, prefix)`, `append_csv(csv_path, meta, summary, turn_usage, tool_calls)` — writes
  header on first append, splits per-turn usage evenly across tool calls in that turn (`Counter`), else emits a single
  empty-turn row
- `run(args)` — orchestrates: picks `STREAM_PROCESSORS[agent]`, spawns subprocess, parses, appends CSV, writes result
  text to stdout, returns returncode
- `parse_args()`

### analyze.py

- `load_csv(path)`, `unique_runs(rows)` (dedupe by `session_id`)
- `total_input(r)`, `run_cost(r)` (recompute cost from tokens when reported cost is 0), `turn_total_input(r)`
- `table_run_summary(rows)`, `table_tool_usage(rows)`, `table_token_efficiency(rows)`
- `fmt_num`, `fmt_pct`, `fmt_table`, `safe_div`
- `main()` — exits 1 if CSV missing or empty

### compare.py

- `agent_color`, `colored`, `fmt_duration`
- `strip_provider(model)`, `resolve_model(agent, model)` (claude-code→bare, opencode→`anthropic/…` or
  `zai-coding-plan/…`, pi→full spec)
- `build_collect_cmd(args, agent, output)`
- `merge_csvs(tmp_paths, output)`
- `main()` — spawns agents concurrently via ThreadPoolExecutor, merges, runs analyze

### tbench_pi_agent.py

- `parse_pi_stream_json(proc, meta)` and `parse_stream_json(log_text)` — parse pi-go JSONL →
  summary/turn_usage/tool_calls
- `build_cmd_pi(args)`, `run_pi(args, meta)`
- `PiAgent` class: `name()`, `get_version_command()`, `install(env)`, `run(instruction, env, ctx)`,
  `populate_context_post_run(ctx)` — reads pi log, fills token/cost, appends CSV via `collect.append_csv`
- Imports `collect.append_csv, compute_cost, lookup_pricing` — coupling to collect.py

### tool_token_analysis.py

- `CHARS_PER_TOKEN = 4`, `SESSION_DIR = ~/.pi-go/sessions`
- `estimate_tokens(text)`, `load_sessions()` (reads JSONL, tolerates parse errors)
- `extract_tool_calls(session)` — pairs `tool_call`/`tool_result` via pending map, estimates input/output chars & tokens
- `fmt_num`, `fmt_pct`, `bar`, `print_table`
- `analyze_tool_distribution(all_calls)`, `print_distribution_table`, `print_output_cost_table`,
  `print_input_cost_table`, `print_histogram`, `print_top_expensive_calls`, `print_batch_subtool_analysis`,
  `print_session_summary`
- `main()`

## Test Gaps

- No `pytest` setup, no test files, no `pyproject.toml`/`pytest.ini` in `hack/test/tb2/`.
- The scripts are plain `#!/usr/bin/env python3` with `if __name__ == "__main__"` guards, making them importable for
  testing.
- Dependencies: `collect.py` and `tbench_pi_agent.py` are coupled (tbench imports from collect). `tbench_pi_agent.py`
  imports `harbor.*` (optional at runtime only when running under Harbor).
- Python version available: 3.14.6 (`/opt/homebrew/bin/python3`); `uv` available.
- Repo already uses `uv`/pyproject in `hack/test/lsp/python/` as a Python project example.
