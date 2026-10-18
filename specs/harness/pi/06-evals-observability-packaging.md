# 06 — Evals, Observability, Packaging & Docs

## Side-by-side summary

| | pi-go | pi |
|---|---|---|
| Eval philosophy | system behavior over trajectories | paired pass-rate lift |
| Eval LOC | 2,849 + scenarios | 1,446 |
| Eval in CI | ✗ | ✗ |
| Coverage gate | unit test, runs in `make test` | withheld-number rule |
| Vulnerability gate | ✓ vulngate | △ daily npm audit |
| SBOM | ✓ syft | ✗ |
| Provenance attestation | ✓ Sigstore | ✗ |
| PGO | ✓ recorded | ✗ |
| pprof | ✓ | ✗ |
| Prometheus metrics | ✓ 6 gauges | ✗ |
| Startup profiling | pprof | ✓ profile:tui/rpc |
| Windows test CI | ✓ | ✗ |
| Custom checkers | 1 (golangci) | 9 |
| User docs | ~40, not shipped | 40, shipped in tarball |

## 6.1 Evals

### pi-go — two harnesses, not one

| Harness | Driver | Question |
|---|---|---|
| `/run` orchestration (`eval-run`, `eval-pin`, `eval-judge`) | `internal/tui/run_eval_e2e_test.go`, tag `e2e`, gate `PI_EVAL_RUN=1` | How well does `/run` orchestrate? |
| Tool coverage (`eval-tools`, `eval-tools-judge`) | `internal/eval/scenarios/tools_e2e_test.go`, gate `PI_EVAL_TOOLS=1` | Does the agent use every tool it advertises? |

Both are **manually run**, need `make build` + a live API key, and are
**reported not asserted**. `internal/eval/eval.md:20-22` says so explicitly.

**Three measured axes** (`eval.md:5-13`): trajectory (every
`trajectory.atif.json` incl. nested subagents + depth), subagent concurrency
(pool budget, top-level agents over time, nested fan-out), tools efficiency
(per-tool calls/results, wasted, duplicates, error-looking results, avg result
size + latency).

The driver (`eval.md:25-46`) runs 8 steps: resolve binary → temp `HOME` + seeded
`config.json` → temp git worktree at pinned `eval/base` tag → poll
`orch.List()` every ~50 ms → pump the `/run` bubbletea chain → diff artifacts vs
golden → optional LLM judge → write JSON+MD to `eval-reports/`.

**Metrics** (`metrics.go`, 808 lines): `RunReport` `:23`, `RunOutcome`/`GateResult`/
`GoldenFile` `:55`/`:71`/`:80`, `ConcurrencyMetrics` `:131` (max/mean + sparkline;
`childBudget = max(1, budget/2)` `:408`), `ToolsMetrics`/`ToolStats` `:148`/`:168`
(including `AvgResultBytes`, `AvgLatency`), `ComputeTrajectoryMetrics` `:214`
(builds depth via `subagentRefs`), `ComputeToolsMetrics` `:430`,
`TokenMetrics` `:662`.

Error detection is **structured** (`looksLikeError` `:608` / `mapLooksLikeError`
`:621`): `{"error":...}`, non-empty `error` field, bash `exit_code ∉ {0,−1}`.

**LLM judge** (`judge.go`): `Judge(ctx, complete, model, r, digest)` `:96` grades
1–5 on `outcome_correctness`, `trajectory_quality`, `concurrency_use`,
`tools_efficiency`. `TrajectoryDigest` `:251` condenses a one-line-per-call
timeline capped by `PI_EVAL_JUDGE_MAX_CALLS` (default 60). **Advisory, never
asserted** (`eval.md:118-121`): *"A judge that cannot run… degrades to a one-line
note in the report rather than failing the eval."*

**Coverage matrix** (`coverage.go`, 252 lines) — and this is the part that is in
`make test`.

`Inventory(dir)` (`inventory.go:48`) constructs tools through **the same
constructors the CLI uses** (`tools.CoreTools` with `WithWebSearch()`,
`BashControlTools`, `SubagentTools`, `MemoryTools`, `LSPToolsFor`, `LLMSTools`,
`A2ATools`, `palace.PalaceTools`). The comment at `:40-47` explains why.

Statuses (`coverage.go:29-45`), best→worst: `ok` (called, ≥1 non-error result),
`errors-only`, `not-called` (has a scenario, no trajectory called it),
`excluded` (documented), `unmapped` (neither — **the state the test exists to
prevent**).

`ComputeCoverage` `:83`; `UnmappedTools` `:166` and `UnknownTargets` `:181` back
`TestScenarios_CoverInventory`. Successful call **beats** exclusion
(`coverageStatus` `:122`).

**Scenarios** (`scenarios/`, 16 + 3 exclusions): `explore`, `read`, `write`,
`edit`, `bash`, `bash-background`, `git`, `subagent`, `session-stats`, `memory`,
`read-image`, `fetch-docs`, `lsp`, `lsp-full`. Excluded: `a2a`, `palace-*`,
`google_search`.

Check vocabulary (`scenarios/README.md`): `file_exists`, `file_absent`,
`file_contains`, `file_not_contains`, `tool_arg_contains`, `tool_result_contains`,
`tool_called_at_least`, `subagent_spawned`.

**The gate is a plain unit test** that runs in `make test` (`eval.md:206-211`):
`TestScenarios_CoverInventory` fails when a tool has neither scenario nor
reasoned exclusion, when a scenario targets a nonexistent tool, or when an
exclusion is stale.

### pi — paired lift, blocked on missing

`packages/evals` (1,446 LOC, `private: true`).

Two eval kinds: `*.docs.eval.ts` runs each case in **isolated `without_docs` and
`with_docs` containers** and reports **pass-rate lift**; other `*.eval.ts` are
ordinary vitest-evals suites.

`without_docs` omits coding-agent `README.md`, `CHANGELOG.md`, `docs/`,
`examples/` **and removes the Pi documentation-routing section from the default
system prompt** (`harness.ts:494`). `with_docs` uses the unchanged prompt.

7 suites: `acme-server`, `configured-runtime`,
`documentation-audit.docs.eval.ts`, `extensions.docs.eval.ts`,
`models.docs.eval.ts`, `openai-provider.docs.eval.ts`, `smoke.eval.ts`,
`tui.docs.eval.ts`.

**Runner**: `harness.ts` (537) requires `PI_PROVIDER` **and** `PI_MODEL`
(`resolveModelSelection` `:70`); `applyIsolatedEnvironment` `:82` redirects
`HOME`/`USERPROFILE`/`PI_CODING_AGENT_DIR` and **deletes every `PI_EVAL_*` var**;
`verifySystemPrompt` `:257`. `DOCUMENTATION_EVAL_TOOLS = ["read","write","edit",
"grep","find","ls"]` `:485` — **no shell, no web search**.

`plan.ts:56-57` **alternates variant order by run number** to reduce order bias.

**The blocking rule** — the sharpest thing in either repo (`report.ts:249-270`):
a pair contributes to lift **only when both arms produce exactly one score**.
Missing, duplicate, skipped, pending, unscored, or errored arms **block** the
pair. If any pair is blocked, headline pass rates are **withheld** and the
process exits nonzero (`:379`). Missing telemetry stays `null`, never zero —
`OperationalMetricTotal = {availableRuns, total: number|null}` `:60`.

Flags emitted (`:51`): `no-lift`, `negative-delta`, `control-saturated`,
`treatment-saturated`, `flaky`.

Container isolation (`docker.ts`): two images built from an ephemeral mount,
packed via the consumer-install machinery; identical cohorts required in both;
every arm a fresh container with a new home, agent dir, workspace, session dir,
and filesystem; **eval definitions/helpers/fixtures/Vitest config are root-owned
and unreadable** after the harness drops to an unprivileged UID.

### Review — evals

**The philosophies are opposite and each is right for its question.**

pi-go measures *system behavior over real trajectories*: wasted calls, duplicate
calls, per-tool latency, concurrency sparklines, nesting depth, subagent fan-out.
It accepts noise and handles that with a **pinned git base** (`eval/base`) for
comparability plus an advisory LLM judge. This is the right instrument for "is my
orchestrator efficient?"

pi measures *pass-rate lift from one variable* with **block-on-missing**
determinism. This is the right instrument for "does the documentation actually
help?" — and the block-on-missing rule is the most important line of code in
either eval harness. A missing score is not zero. Treating it as zero would let a
flaky eval look like a regression and block a real improvement.

**pi-go's coverage gate is the one thing in either repo that is both cheap and
enforced.** `TestScenarios_CoverInventory` runs in `make test` with no binary and
no model. It fails when a tool has neither a scenario nor a reasoned exclusion —
which is exactly the guard that would have caught a tool being added and never
exercised. pi has no equivalent, because its surface is small enough that the
question does not arise. With 29 tools it very much arises.

**The one thing pi-go's eval reports but never gates** is `AvgResultBytes` — and
AGENTS.md's own "never trust a big win without a proof check" section documents
why: seven compactor pipelines were credited with 49–76% savings on payloads of
200 commits while the tool actually runs `git log --oneline -10`. The metric
exists; nothing asserts it against a baseline.

**pi-go's `eval.md:20-22` honesty** ("reported not asserted") and the judge's
graceful degradation are the right posture. An eval that fails the build because
a judge model was rate-limited trains people to ignore it.

**What pi-go should adopt:**

1. **Block-on-missing in the judge and metrics paths.** A missing trajectory
   should be `null`, not `0` — otherwise a crashed session reads as "the agent
   wasted no calls", which is the best possible score.
2. **A committed baseline for `AvgResultBytes` per tool**, asserted with a
   tolerance. This is the missing half of the compactor verification.

## 6.2 Quality gates

### pi-go — the Makefile

38 targets. The gates:

| Target | Line | Gates |
|---|---|---|
| `build` | 25 | `go build ./cmd/pi ./cmd/pi-sandbox`, `-ldflags -X ...BuildTag=$(git rev-parse --short HEAD)` |
| `cache-clean` | 35 | `golangci-lint cache clean` — prevents "no space left on device" |
| `test-unit` | 96 | `go test ./...` |
| `test-integration` | 99 | `-tags integration` |
| `test-e2e` | 102 | `-tags e2e` |
| `test-coverage` | 202 | `-coverprofile` + `-coverpkg=./internal/...` |
| `vulncheck` | 210 | govulncheck JSON → `hack/vulngate` |
| `check-cve` | 215 | `go mod tidy`, grype db update, govulncheck, `grype .` |
| `scan` | 224 | `grype .` (excludes tmp/, .worktrees/) |
| `sbom` | 232 | `syft scan dir:.` → `sbom.spdx.json` — **same invocation as release** |
| `lint` / `vet` | 239/242 | golangci-lint / go vet |

Also: `build-accel`/`deps-accel` (opt-in CGO + ONNX Runtime + CoreML, ~3×
faster `pi memory mine`), `record-pgo`, `sandbox-run`/`sandbox-log`, `fetch-models*`,
and six `eval-*` targets.

`record-pgo` merges **two** workloads (agent/tool loop + TUI render loop) because
a live profile attributed ~24% of CPU to `View()`-per-token rendering.

`make vulncheck` + the vulngate decision rule (`hack/vulngate/main.go:10-20`):

> A finding is **actionable** when its advisory names a **fixed version**. A
> finding with no fixed version cannot be acted on here at all… A check that is
> always red is a check nobody reads. So the rule is: **fail when a fix exists,
> report when it does not.**

Mechanism: streams concatenated JSON via `json.Decoder` `:107`, building
`advisories[id]`/`reported[id]`; `fixedVersion()` `:66` scans OSV
`affected[].ranges[].events[]` for a `"fixed"` key. **Safety default** (`:135-141`):
a finding whose advisory body never arrived is treated as **actionable** with
`fix: "unknown"` — *"silence here would be the wrong default for a security
gate."*

`hack/` (5 tools): `vulngate`, `sopcheck` (validates `specs/` dirs against SOP
artifact contracts without running `/plan`), `session-merge`,
`agentgateway` (docker-compose + k8s + `base-costs.json` 184 KB + `pi-aliases.json`
22 KB + a 23 KB README), `test` (LSP/MCP/tb2 fixtures).

`.githooks/` (6): `pre-commit` (check-large-files **first**, then gofmt + goimports),
`commit-msg` (adds `Signed-off-by`), `prepare-commit-msg`, `post-commit`
(**warns** on unsigned), `pre-push` (**hard-fails** on unsigned / missing-SoB /
oversized), `check-large-files`.

`pre-push` uses `git verify-commit`, not a `gpgsig` header test, because
rewriting an object preserves the header but invalidates the signature. It
**refuses to pass silently** when `gpg.ssh.allowedSignersFile` is unset.

**CI** (`.github/workflows/ci.yml`, 8 jobs): `vscode` (bun `--frozen-lockfile`),
`lint` (golangci-lint v2.13, SHA-pinned), `vulncheck`, `test` (`-count=1` plus
`-race` excluding `/internal/acp/server$`), **`test-windows`** (windows-latest,
`go test -count=1 ./...` — *"the only job that actually executes the `//go:build
windows` files"*), `coverage`, `build` (linux/windows/darwin × amd64/arm64), plus
release jobs including `attest-build-provenance` over archives **and**
`dist/*/pi` binaries — *"the archive digest is not the digest anyone ends up
running"*.

### pi — `npm run check`

```
biome check --write --error-on-warnings .
  && check:pinned-deps      && check:runtime-deps
  && check:ts-imports       && check:entry-graphs
  && check:shrinkwrap       && check:install-lock:coding-agent
  && tsc --noEmit
  && check:browser-smoke
```

**Nine custom checkers** in `scripts/` (46 files):

| Checker | Enforces |
|---|---|
| **`check-entry-graphs.mjs`** | **"Entry points are cost contracts."** Walks the value-import graph of every declared `exports` entry and enforces per-entry budgets (`BUDGETS` `:36`) — e.g. `packages/ai#./models` `maxFiles: 15` + forbids `providers/`, `models.generated.ts`. Rationale `:1-9`: one stray `export *` made a 1-file pure function drag a ~37 MB module graph. Only *value* imports count |
| **`check-runtime-deps.mjs`** | Parses real TS AST; every non-relative, non-builtin import specifier must appear in the manifest's deps. Fails with `file:line` |
| **`check-pinned-deps.mjs`** | Every dep must be an **exact** `x.y.z` semver. Exempts workspace + non-registry specifiers |
| **`check-ts-relative-imports.mjs`** | TS-AST lint on relative imports |
| **`check-browser-smoke.mjs`** | esbuild-bundles 3 smoke entries and asserts no Node builtins leak; stubs unhydrated provider JSON so fresh checkouts pass |
| **`generate-coding-agent-shrinkwrap.mjs`** | Regenerates `npm-shrinkwrap.json`; `--check` in CI. Allowlist of exactly 3 install-script packages |
| **`generate-coding-agent-install-lock.mjs`** | Standalone install lock; `--check` in CI |
| `check-lockfile-commit.mjs` | `.husky/pre-commit` first gate |
| `coding-agent-consumer.mjs` | Packs and installs the tarball as an external consumer |

`.husky/pre-commit`: `check-lockfile-commit` → full `npm run check` →
**conditional** `check:browser-smoke` only when a staged file touches
`packages/ai/*`, `package.json`, or `package-lock.json` → re-stage formatted files.

`biome.json`: tabs, width 3, line 120; 6 explicit overrides
(`noExplicitAny: off`, `noNonNullAssertion: off`, …); a custom grit plugin for
model-type comparison.

**CI** (10 workflows): `ci.yml` (build-check-test + `mcp-conformance`),
`build-binaries.yml` (452 lines, 8 jobs incl. a **ubuntu/macos/windows** binary
smoke matrix), `npm-audit.yml` (daily `npm audit --omit=dev` **+
`npm audit signatures`** — registry signature verification), `pr-gate.yml`,
`approve-contributor.yml`, 4 issue-automation workflows,
`publish-model-catalog.yml`.

### Review — quality gates

**Both are rigorous and both refuse silent passes** — that is a genuine shared
value. pi-go's vulngate treats a missing advisory as actionable; pi-go's
`pre-push` refuses to pass when `allowedSignersFile` is unset; pi's report
withholds headline lift when pairs are blocked; pi's publish job refuses to
publish a partially-attached release.

**pi-go leads on security supply chain**: SBOM (syft, same invocation as
release), Sigstore provenance over both archives and the extracted binaries,
`grype` scanning, and OSV-based gating. pi has a daily `npm audit` (including
registry signature verification, which is nice) but no SBOM and no provenance.

**pi leads on static analysis rigor**: nine custom checkers, six of which enforce
project-specific invariants that a linter cannot express. `check-entry-graphs` in
particular has no Go analogue and no straightforward one — Go's linker already
dead-code-eliminates, so the bundle-size-contract problem does not exist.

**pi-go leads on platform coverage**: `test-windows` actually runs
`go test ./...` on `windows-latest`, which is the *only* way the `//go:build
windows` files ever execute. pi covers Windows only with a binary smoke test in
the release workflow. Given that AGENTS.md documents two distinct Windows-only
failure classes (open dir handles breaking `t.TempDir()`, and `rg` not being
installed), pi-go's CI choice is the correct one and worth keeping.

**pi-go's gap**: no lint gate on `scripts/` or `hack/` — only
`golangci-lint ./...`, which is Go-only. pi's `check` chain covers `scripts/` with
TS-AST analysis. pi-go's shell scripts are unlinted.

**Neither has a coverage threshold.** pi-go uploads to Codecov with
`fail_ci_if_error: false`; pi has no coverage reporting in `check` at all.

## 6.3 Observability

### pi-go — seven subsystems, concretely wired

```
cmd/pi/main.go:run()
  ├─ cli.LoadDotEnv()          ~/.pi-go/.env → process env (keys never in env by default)
  ├─ isOTELPortAvailable()     73500ms TCP probe; sets OTEL_TRACES_EXPORTER=none if unreachable
  ├─ cli.Execute()             → cobra tree; PersistentPreRun starts pprof/metrics/cpuprofile
  └─ otel.Shutdown(ctx)        flush traces before exit
```

| Subsystem | LOC | Role |
|---|---|---|
| `internal/otel` | 332 | Env-driven OTel. `Tracer()` `:88`, `initProvider` `:97`. **Silences the SDK's stderr logger** + installs `captureExportError` `:57` so exporter failures never paint over the TUI; recoverable via `ExportErrors()` `:66`. `IsAvailable` `:180` honors the endpoint's **host**, not just localhost. `httpTraceEndpointURL` `:269` appends `/v1/traces` for pathless HTTP endpoints |
| `internal/httplog` | 340 | Breaks an **ownership cycle**: transports are built *before* the session logger exists (`cli.go` builds LLM ~`:251`, logger ~`:568`), so the transport emits unconditionally and the CLI installs a sink later — same idiom as `auth.SetDebugLogger`. `Entry` `:39` carries `Exchange` (request↔response correlation), `Direction`, redacted `Headers`, truncated `Body`, `Duration`, `Err`. **Two different body caps**: OTel cap much smaller than JSONL |
| `internal/logger` | 257 | JSONL to `~/.pi-go/log/yyyy-mm-dd/session-HH-MM-SS.log`. `Entry` `:29` type ∈ user/llm_text/thinking/tool_call/tool_result/error/info/http_request/http_response. HTTP trace fields are on the **same** `Entry`, "so the transport trace interleaves with the tool calls and model output it caused, in one chronologically ordered file" |
| `internal/ratelimit` | 429 + 293 | 6 Prometheus gauges (`metrics.go:211-217`): `pi_ratelimit_requests_per_minute`, `…_peak`, `…_limit`, `pi_ratelimit_input_tokens_per_minute`, `…_peak`, `…_limit`. Keeps its **own** rolling-window ledger rather than reading `Limiter` internals — the request budget is a bare `rate.Limiter` with nothing to report a rolling count from. Labels are `{provider,model}` only — **no base URL**, which could leak a hostname |
| `internal/retry` | 305 + 64 | Message-based classification because providers hand over opaque strings (an SSE error is a JSON body, not a typed error) — so the *same rule* can live in `internal/provider` (one HTTP retry) and `internal/agent` (whole-run retry). `DefaultConfig`: 5 retries, 1s initial, **60s max** |
| `internal/guardrail` | 445 | `DefaultMaxDailyTokens` 50M. `FreshInputTokens` `:40` + `CacheHitRate` `:51` handle the Anthropic 0.1× discount; tracks context window. `model_wrapper.go` wraps the LLM |
| pprof / cpuprofile | — | `--pprof` on `:6060` from `PersistentPreRun` so subcommands are profilable too; `--cpuprofile` is PGO input |

**GC tuning** (`cmd/pi/main.go:31-40`): `GOMEMLIMIT=512 MiB`, `GOGC=200` from
measured pprof (heap 18–85 MB; `madvise` was 21% CPU on a 705 s aged capture;
~26 GC/sec at GOGC=100). Both env-overridable.

### pi — a port, not an exporter

`packages/telemetry` (935 LOC, 2 export entries).

`TelemetryContext` `:14` (`startSpan`), `TelemetrySpan` `:18`
(`addEvent`/`setAttributes`/`setStatus`), `NOOP_TELEMETRY_CONTEXT`.
`defineTelemetrySchema<T>()` `:72` is a typed identity helper over
`TelemetrySchemaDefinition` `:66`. Each span declares `parents` (`:52`),
`startAttributes`, `endAttributes`, `events`, `status.errorWhen`. Attribute
metadata carries `sensitive?` and `cardinality: "low"|"high"` `:28`.

Type machinery infers literal attribute types from `values`/`elementValues` and
marks `required` attributes as required keys (`InferStartAttributes` `:132`,
`ExactTelemetryAttributes` `:140`, `TypedSpanStarter` `:318`).

`InMemoryTelemetryContext` (`memory.ts`, 219), `noop.ts` (20), and
**`testing/conformance.ts`** — a runnable conformance suite for telemetry
implementations, so any vendor SDK can be validated against the contract.

**No OTel SDK, no Prometheus, no pprof, no retry classifier.** Telemetry is a
*port*; the vendor is the embedder's problem.

### Review — observability

**pi-go ships a product; pi ships an interface.** For an operator running pi-go
on their own machine, OTel + JSONL HTTP tracing + Prometheus gauges + pprof +
token guardrails is a complete, working story. pi's telemetry package is
well-designed but ships no exporter, so a pi user gets nothing out of the box.

**pi-go's httplog ownership-cycle break is the cleverest piece.** Transports are
constructed before the logger exists, so httplog emits unconditionally and the
CLI installs a sink later. That is the same idiom pi-go uses for
`auth.SetDebugLogger`. The alternative — threading a logger through every
constructor — is why many projects give up on HTTP tracing.

**The two-body-cap decision is correct and non-obvious.** OTel span attributes are
collector-message-size bounded; a megabyte attribute silently drops the span.
Using the same cap for both sinks would mean either losing traces or losing
spans. pi-go keeps them separate and documents why.

**pi-go's OTel stderr silencing matters more than it looks.** An exporter
failure writing to stderr paints over the TUI — a corrupt display is worse than a
lost span, and AGENTS.md has an entire section on never writing to stdout/stderr
from the TUI path. `captureExportError` is the direct implementation of that rule.

**pi's `testing/conformance.ts` is the better abstraction and pi-go has no
equivalent.** If pi-go ever wants to support multiple telemetry backends, the
vendor-neutral contract plus a conformance suite is the right structure. It is
also the piece of pi's design most worth stealing, because it is small (935 LOC)
and it makes the concrete implementation swappable without changing call sites.

**What pi-go lacks**: a metrics endpoint on the TUI path is present
(`--metrics-port 9464`) but there is no equivalent of pi's startup profiling —
`profile:tui` / `profile:rpc` measure startup to first-usable-state and to a real
`get_state` response respectively, with `--runs`/`--warmup`/`--no-offline`
(`PI_OFFLINE=1` + `PI_SKIP_VERSION_CHECK=1` so network variance does not pollute
the number). pi-go's `test-bench` and PGO recording cover steady-state, not cold
start, which is what a user actually experiences.

## 6.4 CLI surface

### pi-go — cobra, 13 subcommands

Root (`internal/cli/cli.go:115`) `Use: "pi [prompt]"`, `Args:
cobra.ArbitraryArgs`.

**Root flags** (`cli.go:209-265`): `--model`, `--mode`
(interactive|print|json|socket|rpc), `--json-deltas`, `--socket`
(default `/tmp/pi-go.sock`), `--no-themes` (**hidden**, accepted-and-ignored for
pi CLI compat), `--session`, `--url`, `--continue`, `--smol`/`--slow`/`--plan`,
`--system`, `--header` (stringArray `k=v`), `--insecure`, `--ca-cert`,
`--memory-off`, `--web-search-enabled`, `--lsp` (off|min|full, default `min`),
`--pprof`/`--pprof-port`, `--metrics`/`--metrics-port`, `--cpuprofile`,
`--trace-http`.

**Subcommands** (13): `ping`, `audit`, `serve`, `memory`, `model`, `login`,
`setup`, `acp-server`, `a2a`, `upgrade`, `session-stats`, `verify`, `plugin`.

`memory` has 11 children: `model download|status`, `init`, `status`, `mine`,
`search`, `kg query|add|timeline`, `wake-up`, `recent`, `clear`.
`model` has `list`. `plugin` has `marketplace add|list`, `install`, `list`,
`uninstall`, `update`.

Extra binaries: `cmd/pi-sandbox` (Seatbelt launcher), `cmd/pi-mermaid`,
`cmd/pi-acp-mock`.

### pi — hand-rolled parser, 8 subcommands, 40+ flags

`packages/coding-agent/src/cli/args.ts:82-240`. No commander/yargs.

Flags: `--help`/`-h`, `--version`/`-v`, `--mode <text|json|rpc>`, `--continue`/`-c`,
`--resume`/`-r`, `--provider`, `--model`, `--api-key`, `--system-prompt`,
`--append-system-prompt` (repeatable), `--name`/`-n`, `--no-session`,
`--session`, `--session-id`, `--fork`, `--session-dir`, `--models`,
`--no-tools`/`-nt`, `--no-builtin-tools`/`-nbt`, `--tools`/`-t`,
`--exclude-tools`/`-xt`, `--thinking <level>` (invalid → **warning**, not error),
`--print`/`-p`, `--export`, `--extension`/`-e`, `--no-extensions`/`-ne`,
`--skill`, `--prompt-template`, `--theme`, `--use-theme`, `--no-skills`/`-ns`,
`--no-prompt-templates`/`-np`, `--no-themes`, `--no-context-files`/`-nc`,
`--list-models`, `--tui-mode <regular|fullscreen>`, `--verbose`,
`--approve`/`-a`, `--no-approve`/`-na`, `--offline`, `--` sentinel.

An **`unknownFlags` Map** (`:240-255`) lets extensions register flags, rendered
by `printHelp(extensionFlags?)` `:261`.

Subcommands (8): `install`, `remove`, `uninstall`, `update`, `list`, `config`
(TUI, Tab switches scope), `auth <print-api-key|print-bearer-token>`, `mcp`.

Modes: `text` (default), `json`, `rpc`, plus auto-detect to print mode when
piped/redirected (`docs/cli-integration.md`). Plus `packages/ai/src/cli.ts`
(`bin pi-ai`): an OAuth-only credential helper.

### Review — CLI

**pi-go has more verbs, pi has more flags.** pi-go's 13 subcommands cover
operational tasks pi has no equivalent for — `serve` (browser TUI), `a2a`,
`acp-server`, `audit`, `verify` (attestation), `plugin`, and an 11-child `memory`
subtree. pi's 8 are all about extension/package/auth management.

**pi's extension-registered flags are the better mechanism.** The `unknownFlags`
Map means an extension can add a flag and it appears in `--help`. pi-go's
`--no-themes` being a *hidden, accepted-and-ignored* flag (`cli.go:220-221`) is
the same idea done badly: it exists only for CLI compatibility with pi and is
invisible.

**pi-go's `--lsp off|min|full` on the root command** is a feature pi has no
analogue for, and it pairs with the per-agent `lsp:` frontmatter field
(§5.4) to control the LSP surface per session *and* per agent.

**pi-go's `--json-deltas` and `--mode socket`** support the `jsonrpc` unix-socket
server for editor integration; pi has `rpc` mode over stdio only. pi's RPC is
richer (31 commands per `rpc-types.ts:20-75`) than pi-go's `pirpc`.

## 6.5 Packaging & release

### pi-go

GoReleaser v2 `.goreleaser.yaml`, SIGstore `attest-build-provenance` over archives
**and** `dist/*/pi`, `attest-sbom` with one shared SBOM, syft SBOM generated
**before** GoReleaser because `--clean` wipes `dist/`. Multi-arch
linux/windows/darwin × amd64/arm64 (windows-arm64 excluded). GHCR multi-arch for
the kagent harness with the **digest** output. Release stays a **draft** until
every asset attaches — *"immutability means the missing asset can never be added
afterwards."*

Also `vscode/` → VSIX, and a Nix flake (`flake.nix`/`flake.lock`).

### pi

Three channels:

1. **npm** — `@earendil-works/pi-coding-agent` v1.0.0,
   `bin: {pi: "dist/bundle/cli.js"}`; `files` ships `dist` (minus
   `client`/`experimental`), **`docs`**, **`examples`**, `containerization.md`,
   `CHANGELOG.md`, `npm-shrinkwrap.json`. `prepublishOnly` = clean → build →
   shrinkwrap
2. **Bun compiled binary** (`scripts/build-binaries.sh:132-134`):
   ```bash
   bun build --compile --no-compile-autoload-bunfig --target="$bun_target" \
     ./dist/bun/cli.js ./src/utils/image-resize-worker.ts ./src/extensions/codemode/worker.ts \
     --outfile "$OUTPUT_DIR/$platform/pi"
   ```
   Worker scripts are passed **explicitly** because *"Bun compiled executables
   only embed worker scripts when they are passed as entrypoints"* — otherwise
   they must not start before pi does (#7684). 6 targets.
3. **Source archive** — and `build-binaries.yml:69-79` re-runs
   `build-binaries.sh` **from the extracted archive**, so binaries are built from
   what users download, not from the working tree.

`scripts/release.mjs` has 10 steps: clean-tree check → verify every public
workspace package registered on npm → version bump → CHANGELOG `[Unreleased]`→
`[version]` → regenerate artifacts → check+test → commit+tag → new
`[Unreleased]` → commit → push.

npm publish uses **OIDC trusted publishing** (`environment: npm-publish`).

### Review — packaging

**pi-go's attestation covers the right artifact.** Signing the archives is
insufficient — *"the archive digest is not the digest anyone ends up running"* —
so provenance is attached to `dist/*/pi` as well. That is the detail most
release pipelines get wrong.

**pi's build-from-source-archive rule is excellent.** Verifying the released
binaries are reproducible from the released source catches a whole class of
supply-chain problem. pi-go does not do this; `make build` builds from the
working tree.

**pi's Bun worker-entrypoint constraint has no Go analogue** — Go binaries are
statically linked, so this entire class of problem (embedding a worker script)
does not exist. pi-go's advantage here.

**pi ships its docs in the npm tarball** (`files` includes `docs` and `examples`).
pi-go has no docs in its release artifact at all — there is no equivalent channel
for the 40 markdown files to reach a binary-only user.

**pi-go's Nix flake and VSIX have no pi analogue**; pi's `pi-ai` standalone CLI
has no pi-go analogue.

## 6.6 Documentation

### pi-go

`docs/`: `AGENTS-HOWTO.md`, `DESCRIPTION.md`, `DESIGN-COMPARISON.md`,
`complexity-refactor.md`, `kagent-harness.md`,
`performance-analysis-260711-0038.md`, `articles/` (5: spec-hierarchy-memory-
systems, spec-sandbox, web-search, acp-end-of-task, self-improving-coding-agent),
`screen/`, `superpowers/specs`.

`specs/` — 14 dirs including `eval-orchestrator/` (+ `golden/` + `artifacts/`),
`evaluations`, `features`, `memory-fixes`, `kagent`.

Top level: `AGENTS.md` (37 KB), `ARCHITECTURE.md` (33 KB), `CONTRIBUTING.md`,
`MODELS.md` (43 KB), `ROADMAP.md`, `ISSUES.md`, `MEMORY.md`, `README.md` (32 KB).

**The gap**: no `docs/` entry for `config.json`, the CLI flag surface, or the eval
harness. Those live in `internal/eval/eval.md`,
`internal/eval/scenarios/README.md`, and code comments.

`ARCHITECTURE.md` is stale — the DeepSeek review in this repo recorded that it
lists 17 packages, one of which (`internal/rpc/`) does not exist (actual:
`pirpc`), and omits 12 of 29.

### pi

**40 `.md` files** + `docs.json` + `images/` in
`packages/coding-agent/docs/`, shipped in the npm tarball.

Covering: `index`, `quickstart`, `usage`, `cli`, `cli-integration`, `json`, `rpc`,
`rpc-commands`, `rpc-extension-ui`, `configuration`, `settings`,
`environment-variables`, `models`, `providers`, `custom-provider`,
`virtual-models`, `sessions`, `session-format`, `message-types`, `compaction`,
`extensions`, `packages`, `skills`, `prompt-templates`, `themes`,
`slash-commands`, `keybindings`, `terminal-setup`, `tmux`, `windows`, `termux`,
`tui`, `security`, `containerization`, `shell-aliases`, `llama-cpp`, `mcp`,
`codemode`, `sdk`, `docs.json`.

### Review — documentation

**pi wins clearly.** 40 user-facing documents covering every surface a user
touches — install, configure, every setting, session format, extensions, MCP,
codemode, security posture, containerization, keybindings, tmux, Termux, Windows,
SDK embedding — and **ships them in the artifact**.

pi-go's documentation is heavier on *engineering* (AGENTS.md, ARCHITECTURE.md,
MODELS.md, specs/) and much lighter on *using*. The three biggest user-facing
surfaces — `config.json`, the 38 Makefile targets, and the CLI flags — are
documented only in code comments and `internal/eval/eval.md`.

**Two specific documents pi-go should write:**

1. **`docs/config.md`** — every key in `Config` (27 top-level + ~50 nested) with
   type, default, and purpose. `settings.md` is the model: pi documents each
   setting individually. pi-go's config is *more* operationally complex than
   pi's and less documented.
2. **`docs/security.md`** — the honest security posture. `internal/tools/sandbox.go`
   has a "SECURITY MODEL" comment that overstates the boundary; pi's
   `security.md` is the model for stating what is and is not protected, and it
   explicitly says *"Watching the transcript, using project trust, and reviewing
   changes do not create a security boundary."* pi-go has the better boundary and
   the worse description of it.

**`ARCHITECTURE.md` needs regenerating.** The DeepSeek review already identified
12 of 29 packages missing. pi solves this with generated doc graphs
(`gen-doc-graphs.ts`) plus a `verify-doc-refs` gate.

## Review summary — evals, observability, packaging, docs

**pi-go leads on**: security supply chain (SBOM + Sigstore provenance over the
actual binaries + grype + OSV gating), the coverage-matrix unit test that gates in
`make test`, a complete working observability story (OTel + JSONL HTTP trace +
Prometheus + pprof + guardrail), operational CLI verbs (13 subcommands including
`serve`, `a2a`, `audit`, `verify`), Windows test CI that actually executes
`//go:build windows`, Nix flake, and VSIX.

**pi leads on**: paired-lift eval design with block-on-missing determinism, nine
custom static checkers, vendor-neutral telemetry contracts with a conformance
suite, release built from the source archive, 40 shipped user docs, a
richer extension-flag mechanism, and a much larger flag surface.

**pi-go's three concrete fixes here:**

1. **Block-on-missing in eval metric aggregation.** A missing trajectory should be
   `null`, not `0` — otherwise a crashed session reads as "the agent wasted no
   calls", which is the best possible score.
2. **Write `docs/config.md` and `docs/security.md`.** pi-go's config is more
   complex than pi's and less than half as documented.
3. **Add a committed baseline for `AvgResultBytes` per tool**, asserted with a
   tolerance — the missing half of the compactor verification AGENTS.md says is
   required.

**Neither has a coverage threshold**, and pi-go has no lint gate on `scripts/` or
`hack/`.