# Research: Config, feature toggles, and conditional tool registration

## Verdict: there is no feature-flag system in pi-go

- `grep -rni "experimental|feature|flags"` over `internal/config/*.go` (non-test)
  returns one incidental hit — the word "flag" in a doc comment
  (`internal/config/env_lookup.go:13`).
- A flags map is only **proposed**: `specs/research/003-improvements/GAP_ANALYSIS.md:660-666`
  ("**Gap**: No feature flag system", with a `FeatureFlag` struct sketch) and
  `specs/research/003-improvements/ARCHITECTURE.md:455,780,798` (future/unchecked).
- `config.Config.Tools map[string]any` — `internal/config/config.go:110` — is
  declared but **read nowhere**. Every `cfg.Tools` hit elsewhere refers to
  `agent.Config.Tools` (`[]tool.Tool`), a different type.
- The only `Experiment`-like field is unrelated: `internal/codex/protocol.go:130`
  `ExperimentalAPI bool json:"experimentalApi"` (Codex capability flag).

## The established convention: per-subsystem `*bool Enabled`

| Sub-struct | Field | Anchor |
|---|---|---|
| `MemoryConfig` | `Enabled *bool json:"enabled,omitempty"` | `config.go:40` |
| `PalaceConfig` | `Enabled *bool` | `config.go:141` |
| `CompactorConfig` | `Enabled *bool` | `config.go:159` |
| `AutoCompactConfig` | `Enabled *bool` | `config.go:170` |

**Why `*bool`** — documented for the analogous `*int` in `RateLimitConfig`
(`config.go:64-66`): "Fields are pointers so that 'not configured' and
'explicitly unlimited' stay distinguishable: a missing field inherits the
built-in default for the provider, and an explicit 0 turns that budget off."

**Defaults resolve at the reader, not in the struct.** `Defaults()`
(`config.go:237-246`) sets no `*bool` and no sub-struct — `cfg.Memory`,
`cfg.Palace` etc. are all nil by default (pinned by `TestMemoryConfigNilWhenNotSet`,
`config_test.go:542`). The reader supplies the policy:

```go
// internal/cli/cli.go:1189-1192
// memoryEnabled reports whether the observation memory subsystem is on.
// It defaults to on: only an explicit false in config, or --memory-off,
// disables it.
func memoryEnabled(cfg config.Config) bool {
    return !flagMemoryOff && (cfg.Memory == nil || cfg.Memory.Enabled == nil || *cfg.Memory.Enabled)
}
```
`palaceIsEnabled` (`:1195-1197`) is identical in shape.

## Config loading and precedence

No viper, no YAML/TOML — `encoding/json` only (`config.go:4`).

- `Defaults() Config` — `config.go:237-246`: `Roles{"default":{Model:"gpt-5.6-sol"}}`,
  `DefaultProvider:"openai"`, `ThinkingLevel:"high"`, `Theme:"default"`.
- `Load()` → `LoadFrom(".")` → `loadConfigFiles` (`config.go:363-376`).
  Precedence: **Defaults → `~/.pi-go/config.json` → nearest project
  `.pi-go/config.json`** (project wins). Merge is `json.Unmarshal(data, cfg)` in
  `loadFile` (`config.go:663-669`) — overlay only, so a project file overrides only
  the keys it declares.
- `findNearestProjectFile` (`config.go:454-471`) walks up from cwd.
- Env overrides exist per-subsystem, not generically: `APIKeys()`
  (`config.go:673-696`, env only), `BaseURLs()` (`:701-720`), merged by
  `ResolveBaseURLs()` (`:727-738`) where **env wins** and an empty env var does not
  mask a configured value; `ResolveRateLimits` (`internal/config/ratelimit.go:15-26`).
- `.env` loading is separate and mutates the process env: `cli.LoadDotEnv()`
  (`internal/cli/cli.go:1978-1981` → `loadDotEnv` `:1985-2010`), called from
  `cmd/pi/main.go:54`. Note file values **overwrite** the inherited shell env
  (`cli.go:2012-2041`) — a different precedence rule from `LookupEnvFrom`
  (`env_lookup.go:39-42`, process env first).

## Env-var reading

No helper library; direct `os.Getenv`, plus:
- `config.LookupEnv(names ...string) (value, source string)` — `env_lookup.go:31`
  (process env → project `.pi-go/.env` → project `.env` → `~/.pi-go/.env`).
- `config.mergeEnvFile` — `config.go:524-540` (for `${VAR}` substitution).
- `cli.loadDotEnvFile` — `cli.go:2012-2041`.

Truthy-token parsing for capability toggles: `PI_NO_GROUNDING`
(`internal/agent/grounding.go:185,192-198`), `PI_XAI_TOOLS` / `PI_NO_XAI_TOOLS`
(`internal/provider/xai_tools.go:15-16,26-34,60-70`).

## The subagent env family (three vars, all env-only)

| Env var | Controls | Default | Resolved by |
|---|---|---|---|
| `PI_SUBAGENT_CONCURRENCY` | Semaphore pool size every spawn must `Acquire` | `DefaultPoolSize = 3` (`orchestrator.go:28`) | `ConcurrencyFromEnv()` (`concurrency.go:26`) |
| `PI_SUBAGENT_TIMEOUT_MS` | Absolute wall-clock cap per subagent | `DefaultAbsoluteTimeout = 20m` (`timeout.go:22`) | `ResolveTimeout()` (`timeout.go:44`) |
| `PI_SUBAGENT_INACTIVITY_MS` | No-output window before judged wedged | `DefaultInactivityTimeout = 5m` (`timeout.go:32`) | `ResolveTimeout()` (`timeout.go:44`) |

**There is no `SubagentConfig` in `config.json`.** This subsystem never migrated
into config — it is the natural home for a new subagent flag.

`ConcurrencyFromEnv` (`concurrency.go:26-49`) is the defensive style to copy:
unset → default; `Atoi` error → warn + default; `n < 1` → warn + default (a
zero-size pool "would block the first Acquire forever, turning a misconfiguration
into a hang rather than a slow run"); `n > 64` → clamp to `maxConcurrencyBudget`.

`ResolveTimeout` precedence: **frontmatter > env > default** (`timeout.go:42`),
and a per-call `input.Timeout` beats all of it (`orchestrator.go:461-464`).

**Env propagation to children:** `PI_` is a forwarded prefix in
`DefaultEnvAllowlist` (`environ.go:44`), so `PI_SUBAGENT_*` reaches spawned agents.
`ChildEnv(parentBudget)` (`environ.go:101`) **strips and rewrites only**
`PI_SUBAGENT_CONCURRENCY` → `childConcurrency(parent)` = `parent/2`, floor 1
(`:105-113`, `concurrency.go:62`). Rationale (`concurrency.go:62-76`): a child is
its own pi process with its own pool, so inheriting unchanged makes total
concurrency `depth × budget` — "that is what put eight agents in flight against a
per-minute token limit that only tolerated a few." Consumers: pi spawner
`spawner.go:142`, codex `spawner_codex.go:115`, plus `Orchestrator.spawnEnv`
(`orchestrator.go:556-569`) adding `PI_SANDBOX_ROOT`/`PI_WORKTREE_ROOT`.

**Consequence:** a new `PI_SUBAGENT_ASYNC` would pass through to children
unchanged (only `PI_SUBAGENT_CONCURRENCY` is rewritten), so nested spawns see it.

## Conditional tool registration

`CoreTools(sandbox *Sandbox, opts ...CoreOption) ([]tool.Tool, error)` —
`registry.go:44`. `CoreOption` is `func(*coreConfig)` (`registry.go:18`) and there
are **exactly two**: `WithBashSupervisor` (`:30-32`) and `WithReadLedger`
(`:38-40`).

**`CoreOption` cannot add or remove tools** — `coreConfig` holds only pointers to
shared runtime state (`registry.go:20-23`). The tool list is a fixed literal slice
of 12 builders (`:56-69`) plus `session-stats` (`:80-85`).

**The conditional-registration idiom is: separate constructor + caller-side `if` +
`append`.** Gating precedents:

| Family | Gate | Anchor |
|---|---|---|
| memory (`mem_*`) | `deferredMemoryEnabled(cfg)` (`interactive.go:894-897`); non-interactive uses `memStore != nil` from `setupMemory` gated on `memoryEnabled` (`cli.go:1189-1191`); piagent `o.memoryEnabled` default **false** (`options.go:69,209`) | `interactive.go:526-535`, `cli.go:915-928`, `piagent/agent.go:260-267` |
| palace | `palaceIsEnabled(cfg)` → DBPath non-empty → `os.Stat` → `DrawerCount > 0` (`cli.go:1249-1258`) | `cli.go:1263-1319`, `piagent/build.go:268-307`; **never** in the interactive TUI |
| LSP | `lspMgr.AnyAvailable()` **and** mode != off | `interactive.go:479-482`, `cli.go:951-960`, `piagent/build.go:317-332` |
| MCP | `cfg.MCP != nil && len(cfg.MCP.Servers) > 0` | passed as `agent.Config.Toolsets`, not `coreTools` (`interactive.go:492-503`, `cli.go:1377-1390`) |
| A2A | `cfg.A2A != nil && len(cfg.A2A.Agents) > 0` | Toolset (`cli.go:1383-1385`); absent from interactive TUI |
| Gemini grounding | provider == `"gemini"` and `PI_NO_GROUNDING` untruthy | `grounding.go:205-211`; append at `interactive.go:281-283` |
| subagent | piagent: `if o.subagentEnabled` (`agent.go:249-255`); CLI/TUI/ACP append unconditionally | `agent.go:249`, `interactive.go:226-227`, `cli.go:720-726`, `acp/server/runtime.go:470-475` |

Constructors that return `(nil, nil)` when unusable — the pattern for a
conditionally-present tool: `MemoryTools` (`mem_search.go:209-212`),
`PalaceTools` (`palace/tools.go:9-12`), `LSPToolsFor(mgr, LSPOff)`
(`lsp.go:262-263`).

**Correction to a claim in the plan:** the `BashControlTools` doc comment says the
tools are deliberately not in `CoreTools` because they are "only worth advertising
when something can background" (`bash.go:244-249`) — but **every production call
site appends them unconditionally** (`interactive.go:418-422`, `cli.go:694-698`,
`piagent/agent.go:235-239`, `acp/server/runtime.go:452-457`,
`eval/inventory.go:69-73`). So bash is a precedent for *separate construction*,
not for *gating*. Its guard test (`bash_control_test.go:20-43`, asserting the
description names each control tool) **is** a precedent worth mirroring.

## "Tool exists but is unusable" precedents

No code path returns `{"status":"unsupported"}` — `grep -rn "unsupported"
internal/tools/` is empty. The closest shapes:

- Handler returns an `Error` string: `lsp.go:290` `"no language server configured
  for %s files"`, `lsp.go:297` `"%s not available"`; `a2a.go:112` `"unknown agent:
  %q (available: %v)"` returned as `result.Error` (`:159-163`).
- Description advertises absence: `a2a.go:393-395` `"…No A2A agents configured."`;
  `llms.go:539-541` `"No llms.txt sources configured."`.
- Behaviour no-op'd by config: compactor `if !cfg.Enabled || err != nil` (`compactor.go:81`).
- Provider-layer kill switches: `PI_NO_GROUNDING`, `PI_XAI_TOOLS`.
- Silently inert for unsupported providers: `read_image`'s vision injection
  (`extension/read_image.go:42-53`).

## `buildSubagentDescription` — dynamic text

`internal/tools/subagent.go:117-160`, called once from `NewSubagentTool` (`:90`).
Varying segments:
- Agent names + `" [worktree]"` marker — `:131-142`, driven by
  `orch.AgentNames()` + `LookupAgent` (errors skip the name, `:134-136`).
- `"At most %d task(s) per call. This process runs %d subagent(s) at a time"` —
  `:150-152`, with `maxParallelTasks = 8` (`:25`) and `orch.Concurrency()`.
- Branch on that number — `:153-157`: `concurrency <= 1` → "parallel mode gives no
  speed-up here, so prefer one task per call"; else "batches larger than %d queue
  rather than overlap".
- Static: mode menu (`:121-124`, hardcodes "max 8"), worktree note (`:126-128`),
  trailer (`:158`).

Pinned by `internal/tools/subagent_description_test.go:16-46` (`runs 4
subagent(s) at a time`, `queue rather than overlap`, `no speed-up`), with the env
seam `t.Setenv(subagent.ConcurrencyEnvVar, "4")` before `NewOrchestrator`
(`:15-21`). **This is the precedent for making the `subagent` description mention
async/`background`, and for testing that the description stays honest.**

## Config test patterns

- `testenv.SetHome(t, t.TempDir())` — `internal/testenv/testenv.go:19-25` (sets
  `HOME`/`USERPROFILE` so `os.UserHomeDir()` lands in the sandbox). Used at
  `config_test.go:344,392,431,490,1354`.
- `os.MkdirAll(dir/.pi-go)` + `os.WriteFile(config.json)` + `Load()` —
  `TestMemoryConfigFromJSON` (`config_test.go:489-540`, asserting
  `*cfg.Memory.Enabled == false`); `TestLoad_WithGlobalAndProjectConfig`
  (`:281-326`, asserting project wins).
- `t.Setenv` for env cases — `TestAPIKeys` (`:222-229`), `TestBaseURLs` (`:253-258`),
  `TestResolveBaseURLs_EnvOverridesConfig` (`:1322`).
- Defaults pinned explicitly: `TestDefaults` (`:13-28`), `TestMemoryDefaults`
  (`:473-487`), `TestMemoryConfigNilWhenNotSet` (`:542-548`).
- CLI-layer toggle test: `internal/cli/run_modes_test.go:130-148`
  (`resetGlobalFlags`, `testenv.SetHome`, `t.Setenv`, config file with
  `"memory":{"enabled":false}`, then `newRootCmd().Execute()`).

An env-var flag is therefore testable with plain `t.Setenv` and no temp-HOME
plumbing — simpler than the config-file route.
