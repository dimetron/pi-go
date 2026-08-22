# Tool-coverage eval scenarios

This package is the **tool-coverage suite** for the pi coding agent: one
declarative scenario per tool family, run headlessly through the real `pi`
binary, graded deterministically, and rolled up into a coverage matrix over
every tool the agent can register. It extends the `/run` eval harness in
`internal/eval` (see `../eval.md`, section "Tool-coverage eval").

```bash
make build
make eval-tools                          # whole suite
PI_EVAL_SCENARIO=git,edit make eval-tools  # a subset
PI_EVAL_JUDGE_MODEL=claude-sonnet-4-6 make eval-tools-judge
```

Reports land in `eval-reports/eval-tools-<ts>.{json,md}`. Knobs: `PI_EVAL_MODEL`
(default: `config.Defaults()`), `PI_EVAL_THINKING` (default `none` — the
cheapest level and the only one every model accepts), `PI_EVAL_TIMEOUT` (per
scenario, default 5m), `PI_EVAL_OUT`, `PI_EVAL_STRICT=1`, `PI_EVAL_SERIAL=1`,
`PI_EVAL_OFFLINE=1`, `PI_EVAL_NO_VISION=1`, `PI_EVAL_JUDGE_MODEL`.

## What a scenario is

`scenarios.go` holds the table. Each `eval.Scenario` has:

| field | meaning |
|---|---|
| `Name` | id used in reports, `PI_EVAL_SCENARIO` and `-run 'TestEvalTools/scenario/<name>'` |
| `Tools` | tools the agent **must** call, each with ≥1 non-error result. `"grep\|ripgrep"` lists alternatives (the content-search tool is named `ripgrep` when `rg` is on PATH) |
| `Requires` | host capabilities: `lsp` (a language server on PATH, probed), `network` (opt out with `PI_EVAL_OFFLINE=1`; otherwise the runner probes the scenario's `LLMS` URLs from the test process and skips with the cause when one is unreachable), `vision` (skip with `PI_EVAL_NO_VISION=1`). Unmet → scenario **skipped**, not failed |
| `Files` / `Git` / `Modified` | fixture files; `Git` commits them; `Modified` is written after the commit so `git-*` tools see a diff |
| `Memory` | seeds observations into the memory store and turns memory on for the run (it is off otherwise — the memory worker spawns compressor subagents after every tool call) |
| `LLMS` | llms.txt sources to configure, which registers `fetch_docs` |
| `Args` | extra `pi` flags (`--lsp full`) |
| `Prompt` | the single user turn |
| `Checks` | deterministic assertions (below) |
| `Timeout` | per-scenario override of `PI_EVAL_TIMEOUT` (default 5m) |

### Check vocabulary

| kind | asserts |
|---|---|
| `file_exists` / `file_absent` | path (relative to the workdir) exists / does not |
| `file_contains` / `file_not_contains` | file content contains / lacks `Text` |
| `tool_arg_contains` | some call to `Tool` has argument `Arg` (or any argument) containing `Text` |
| `tool_result_contains` | some result of `Tool` contains `Text` (matched on its JSON/text rendering) |
| `tool_called_at_least` | `Tool` called ≥ `N` times |
| `subagent_spawned` | an observation links a nested subagent trajectory (a child `pi` really ran) |

A scenario **passes** when every target tool has at least one non-error result
and every check holds. A result "looks like an error" when it is
`{"error": "..."}` (how the ADK runner reports a tool failure), carries a
non-empty `error` field, or is a bash result with exit code ≠ 0/−1, or — for
plain-string results — matches the legacy marker heuristic.

## How a scenario runs

`tools_e2e_test.go` (`TestEvalTools`, build tag `e2e`, gated by
`PI_EVAL_TOOLS=1`) does, per scenario:

1. Creates an isolated `HOME` and a workspace dir; seeds fixtures, git history,
   memory and `$HOME/.pi-go/config.json` (`eval.ScenarioConfig`: the eval model
   on every role, memory/palace off unless asked, no hooks/MCP/A2A).
2. Runs `pi --mode print [Args] "<Prompt>"` in the workspace with `HOME`
   redirected and git signing forced off, under a timeout.
3. Loads every `trajectory.atif.json` under `$HOME/.pi-go/sessions` — root
   session plus any subagents it spawned — and grades with
   `eval.EvaluateScenario`.

Scenarios run as parallel subtests (`-parallel 4` from the Makefile;
`PI_EVAL_SERIAL=1` to serialize). Outcomes are **reported, not asserted** —
the suite is a measurement, and LLM runs are not deterministic — unless
`PI_EVAL_STRICT=1`, which fails the subtest on a scenario FAIL/ERROR and fails
the run on a non-empty coverage gap.

After all scenarios: `eval.ComputeCoverage` over the **full inventory**
(`eval.Inventory`), tools/token metrics, optional LLM judge, report.

## The coverage gate

`eval.Inventory` enumerates every tool by constructing them through the same
constructors the CLI uses (`tools.CoreTools`, `BashControlTools`,
`SubagentTools`, `MemoryTools`, `LSPToolsFor(full)`, `LLMSTools`, `A2ATools`,
`palace.PalaceTools`, the Gemini grounding tool). It is not a hand-kept list.

`TestScenarios_CoverInventory` (a plain unit test, runs in `make test`) fails
when any inventoried tool has neither a scenario targeting it nor an entry in
`Exclusions`, when a scenario targets a tool that does not exist, or when an
exclusion is stale. Adding a tool to the agent therefore requires either a
scenario here or an explicit, reasoned exclusion.

MCP tools are not in the inventory (they are whatever the user's MCP servers
advertise); calls to them show up in the report under "called but not
inventoried".

## Scenario → tool map

| scenario | tools | requires | key assertions |
|---|---|---|---|
| `explore` | `ls`, `tree`, `find`, `grep\|ripgrep` | | grep pattern has the needle; grep result names `notes.md`; find pattern has `.md` |
| `read` | `read` | | read result contains `8471` |
| `write` | `write` | | `hello.txt` contains `hello from pi` |
| `edit` | `read`, `edit` | | `greet.go` has `"Howdy"`, not `"Hello"`, other const untouched |
| `bash` | `bash` | | `count.txt` contains `5`; bash command contains `wc` |
| `bash-background` | `bash`, `bash_wait`, `bash_kill` | | bash_wait result contains `FINISHED-OK`; bash_kill called with a handle (slow: ≥2 min by design) |
| `git` | `git-overview`, `git-file-diff`, `git-hunk` | | overview lists `main.go`; diff/hunk called on `main.go`; diff shows `version 2` |
| `subagent` | `subagent` | | a nested trajectory is linked; result mentions `gamma` |
| `session-stats` | `session-stats` | | called, no error |
| `memory` | `mem-search`, `mem-get`, `mem-timeline` | | mem-search result contains `rotation` |
| `read-image` | `read_image` | vision | called, no error |
| `fetch-docs` | `fetch_docs` | network | url has `llmstxt.org`; result contains `llms.txt` |
| `lsp` | `lsp-symbols`, `lsp-diagnostics` | lsp | symbols contain `Alpha`; diagnostics mention `unusedValue` |
| `lsp-full` | `lsp-hover`, `lsp-definition`, `lsp-references`, `lsp-workspace-symbol`, `lsp-code-action` | lsp | workspace-symbol query `Alpha`; references name `main.go` |

Excluded (see `Exclusions` in `scenarios.go`): `a2a` (remote A2A server),
`palace-*` (populated memory palace + embedding model), `google_search`
(Gemini provider built-in).

`fetch_docs` cannot be pointed at a local `httptest` server: the tool enforces
`https://` and rejects private hosts, so the scenario uses a real public
`llms.txt` and is tagged `network`.

## Adding a scenario

1. Add a constructor in `scenarios.go` and list it in `Suite()`.
2. Name the tools it must exercise in `Tools`; keep prompts explicit about
   *which* tool to use and tell the model not to fall back to bash.
3. Prefer checks on files and tool arguments/results over prose in the reply.
4. Run `go test ./internal/eval/...` — the mapping, seeding and
   satisfiability tests run without a model — then `PI_EVAL_SCENARIO=<name>
   make eval-tools` for a live pass.
