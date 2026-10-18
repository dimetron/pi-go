# 99 — Verification

Every count and structural claim in this report, with the command that produced
it. Run from the repo root.

## Scale

```bash
# pi-go: non-test and test Go lines under internal/
find internal -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l
find internal -name '*_test.go' -exec cat {} + | wc -l

# pi-go: package count (internal/a2a is a namespace dir with no top-level .go)
ls -d internal/*/ | wc -l

# pi: non-test and test TS lines under packages/*/src
find packages -path '*/src/*' -name '*.ts' ! -name '*.test.ts' \
  -not -path '*/node_modules/*' -exec cat {} + | wc -l
find packages -path '*/test/*' -name '*.ts' -exec cat {} + | wc -l

# pi: workspace package count
ls -d packages/*/ | wc -l          # 13
```

Verified values: pi-go ~92k non-test / ~72k test Go lines across 41 `internal/`
directories; pi ~149k non-test / ~110k test TS lines across 13 packages.

## Provider count — pi-go has 10, not 8 and not 11

```bash
# Count the arms of the factory switch. This is the authoritative number.
cd /Users/dimetron/p6s/pi-dev/pi-go
sed -n '/^func NewLLM/,/^}/p' internal/provider/provider.go | grep -c 'case "'
# → 10
```

The 10: `ollama`, `gemini`, `openai`, `azure`, `anthropic`, `mistral`,
`openrouter`, `xai`, `opencode`, `agentgateway`.

Three related counts that are often conflated:

```bash
# a. The setup wizard lists 10 — includes azure and opencode
grep -c 'Provider{' internal/cli/setup.go

# b. `pi model list` lists 8 — excludes azure and opencode
sed -n '/allProviders/,/^}/p' internal/cli/model.go | grep -c '"'

# c. openai-codex is NOT a NewLLM arm. It is an OpenAI *mode*, selected by
#    JWT sniffing in BackendName:
grep -n 'IsCodexOAuthToken' internal/provider/provider.go
# → provider.go:177-189, BackendName returns "openai-codex-chatgpt"
```

`openai_codex.go` and `agentgateway.go` both carry comments citing
*"pi-mono's openai-codex-responses provider"* — pi-go tracks pi here.

## Provider count — pi has 42, not 43

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go/tmp/harness/pi

# Authoritative: count the factory calls in builtinProviders()
sed -n '/export function builtinProviders/,/^}/p' packages/ai/src/providers/all.ts \
  | grep -c 'Provider()'
# → 42
```

Two cross-checks that agree:

```bash
# The KnownProvider union has the same members
sed -n '/KnownProvider/,/;/p' packages/ai/src/types.ts \
  | grep -oE '"[a-z0-9-]+"' | wc -l
# → 44 raw string matches; 42 are provider ids (the extra 2 are in a
#   separate union on the same declaration)

# One .models.ts shard per provider
ls packages/ai/src/providers/*.models.ts | wc -l
# → 42
```

`radius` has **no static catalog entry** (`all.ts:50-56`), so counting `*.models.ts`
and counting `builtinProviders()` agree only because `radius` still has a
`.models.ts` that imports a generated JSON shard.

## Wire APIs

```bash
# pi: 10 KnownApi members
sed -n '/KnownApi/,/;/p' packages/ai/src/types.ts | grep -oE '"[a-z0-9-]+"' | sort -u

# pi-go: the opencode per-route family switch is pi-go's narrow version of this
sed -n '/opencodeGoModelCatalog\[modelName\]/,/^	}/p' internal/provider/opencode.go
# → opencode.go:69-79, families "chat", "responses", "messages"
```

## Tool counts

pi-go has **no name constants** — tool names are positional args to
`newTool(name, description, handler, aliases...)` (`registry.go:279`). The only
named constant is `grepToolName`.

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go

# Core tools built by CoreTools (registry.go:104-116) + session-stats
sed -n '/builders := /,/^	}/p' internal/tools/registry.go | grep -c 'return new\|, new'
# → 12 builders

# grep.go:131 is the only name constant in the package
grep -rn 'grepToolName *=' internal/tools/

# All tool names, by file (read.go:79, write.go:27, ...)
grep -rhoE 'newTool\("[a-z_]+"' internal/tools/*.go \
  | sort -u
```

Counted inventory: **16 core** (read, read_image, write, edit, bash, bash_wait,
bash_kill, ripgrep/grep, find, ls, tree, git-overview, git-file-diff, git-hunk,
session-stats, web_search) + **7 LSP** (`lsp.go:133-193`) + **3 memory**
(`mem_search.go:36,102,163`) + `subagent` + `a2a` + `fetch_docs` = **~29**.

```bash
# pi: exactly 8 built-ins, and they have a name union
sed -n '/export type ToolName/,/;/p' packages/coding-agent/src/core/tools/index.ts
# → read, bash, edit, write, grep, find, ls, powershell

# plus 4 extension-supplied tools
ls packages/coding-agent/src/extensions/
# → codemode, index.ts, llama, mcp, tool-search
```

## Slash command counts — both are 24

```bash
# pi-go: 27 registered, 3 hidden
grep -c 'name: "/' /Users/dimetron/p6s/pi-dev/pi-go/internal/tui/commands.go
# → 27; hidden are /skill-list, /skill-load, /skill-create
# → 27 - 3 = 24 visible

# pi: count entries in BUILTIN_SLASH_COMMANDS
sed -n '/BUILTIN_SLASH_COMMANDS/,/^];/p' packages/coding-agent/src/core/slash-commands.ts \
  | grep -c '{ name: "'
# → 24
```

## Docs counts

```bash
# pi: 40 user-facing .md files, shipped in the npm tarball
ls packages/coding-agent/docs/*.md | wc -l
# → 40   (briefs sometimes say 47; that counts docs.json + images/ + non-md)

# confirm they ship
grep -A8 '"files"' packages/coding-agent/package.json
# → dist, docs, examples, containerization.md, CHANGELOG.md, npm-shrinkwrap.json
```

## Session schema versions

```bash
# pi: version constant + migration functions
grep -n 'CURRENT_SESSION_VERSION\|function migrateV' \
  packages/coding-agent/src/core/session-manager.ts
# → :41 CURRENT_SESSION_VERSION = 3
# → :337 migrateV1ToV2, migrateV2ToV3

# pi-go: NO schema version. SetSessionVersion records the build string only.
grep -n 'func.*SetSessionVersion' /Users/dimetron/p6s/pi-dev/pi-go/internal/session/store.go
# → :637

# Confirm there is no migration path in internal/session/
grep -rn 'func migrate\|schemaVersion' /Users/dimetron/p6s/pi-dev/pi-go/internal/session/
# → (no matches)
```

## Torn-tail vulnerability and pi's fix

```bash
# pi-go: readEvents fails the whole load on any unparseable line
sed -n '1113,1140p' /Users/dimetron/p6s/pi-dev/pi-go/internal/session/store.go
# → :1136 returns err

# pi: detects a missing trailing newline and truncates to the last complete line
grep -n 'lastIndexOf(0x0a)' packages/durable/src/storage/jsonl/storage.ts
# → :799-803
```

## Provider registries that disagree in pi-go

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
echo "NewLLM arms:      $(sed -n '/^func NewLLM/,/^}/p' internal/provider/provider.go | grep -c 'case \"')"
echo "ListModels:        $(grep -c 'case \"' internal/provider/list_models.go)"
echo "setup wizard:      $(grep -c 'Provider{' internal/cli/setup.go)"
echo "model list:        $(sed -n '/allProviders/,/^}/p' internal/cli/model.go | grep -c '\"')"
# → NewLLM 10, ListModels 8, setup 10, model list 8
# azure + opencode: in NewLLM and the wizard, absent from ListModels and model list
# openai-codex:     not a NewLLM arm at all
```

## Durable is not on pi's production path

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go/tmp/harness/pi
grep -rn '@earendil-works/pi-durable' packages/coding-agent/src/ \
  | grep -v '\.test\.'
# → only src/experimental/vacation/* and src/experimental/client-tui-chat.ts
```

Production uses `core/session-manager.ts` + `core/agent-session.ts` +
`packages/agent/src/agent-loop.ts`. The durable design is a **reference**, not a
shipped default — which is why this report treats it as "better designed, not yet
shipped" rather than "pi has crash recovery".

## Hooks cannot block in pi-go

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go

# Only two hook events exist
grep -rn 'Event is "before_tool"' internal/extension/hooks.go
# → hooks.go:16-17

# BuildBeforeToolCallbacks returns nil, nil unconditionally, including on failure
sed -n '145,160p' internal/extension/hooks.go
# → :152-157 "Non-fatal: log and continue" → return nil, nil
```

Against ADK's contract, which *does* support blocking:

```go
// adk/v2.4.0 internal/llminternal/base_flow.go:1421-1434
for _, callback := range f.BeforeToolCallbacks {
    result, err := callback(toolCtx, tool, fArgs)
    if err != nil { return nil, err }
    if result != nil { return result, nil }   // tool never runs
}
```

pi's contract, which supports block + arg mutation + batch termination:

```ts
// packages/coding-agent/src/core/extensions/types.ts:1402-1411
export interface ToolCallEventResult {
  block?: boolean; reason?: string; terminate?: boolean;
}
```

## Extension event counts

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go/tmp/harness/pi

# The ExtensionEvent union
sed -n '/export type ExtensionEvent/,/;/p' packages/coding-agent/src/core/extensions/types.ts \
  | grep -cE '^\t[a-z_]+:|^\t\| "'
# → 31 named in the union; 5 more arrive via the SessionEvent union and
#   CacheWarmingDecisionEvent is re-exported at :1397 → 36 concrete events

# The UI context hooks
sed -n '/export interface ExtensionUIContext/,/^}/p' packages/coding-agent/src/core/extensions/types.ts \
  | grep -cE '^\t[a-zA-Z]+[?]?\('
# → 30
```

pi-go has 2 (`before_tool`, `after_tool`), both shell subprocesses.

## MCP: client+server vs client-only

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
grep 'modelcontextprotocol' go.mod
# → modelcontextprotocol/go-sdk v1.7.0 — official SDK, client AND server support

cd ../pi-go/tmp/harness/pi
grep -c 'Server' packages/mcp/src/client.ts
grep -rn 'McpServer' packages/mcp/src/ | head
# → client only; no server implementation in packages/mcp
```

pi's conformance suite:

```bash
ls packages/coding-agent/test/mcp-conformance/
# → run.ts, client.ts, baseline.json, README.md
grep -n '2025-03-26\|2025-06-18\|2025-11-25' packages/coding-agent/test/mcp-conformance/run.ts
# → three negotiated protocol versions
```

## Subagent agent counts

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
ls internal/subagent/bundled/*.md | wc -l
# → 18

# Worktree-isolated agents (frontmatter `worktree: true`)
grep -l 'worktree: *true' internal/subagent/bundled/*.md
# → designer.md, task.md

# External-CLI agents by transport
grep -n 'acpAgentNames\|codexAgentNames' internal/subagent/spawner_acp.go internal/subagent/spawner_codex.go
# → acp: claude, gemini, cursor, copilot, agy (spawner_acp.go:84-89)
# → codex: codex, codex-review (spawner_codex.go:19-22)

# pi's sample agents
ls packages/coding-agent/examples/extensions/subagent/agents/ | wc -l
# → 4
```

## Concurrency constants

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
grep -n 'DefaultPoolSize\|maxConcurrencyBudget\|func childConcurrency' \
  internal/subagent/orchestrator.go internal/subagent/concurrency.go
# → DefaultPoolSize = 3 (orchestrator.go:29), with the measured justification at :21-28
# → maxConcurrencyBudget = 64 (concurrency.go:19)
# → childConcurrency: parent < 2 ? 1 : parent/2 (concurrency.go:66-71)

grep -n 'MAX_PARALLEL_TASKS\|MAX_CONCURRENCY' \
  packages/coding-agent/examples/extensions/subagent/index.ts
# → both 8 and 4 (index.ts:33-36)
```

## Pi-go sandbox: what is and is not enforced

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go

# The real filesystem jail uses os.Root — kernel-enforced
grep -n 'os.OpenRoot\|os.Root' internal/tools/sandbox.go
# → :87 os.OpenRoot

# But bash only borrows sb.Dir() as a cwd
grep -n 'sb.Dir()' internal/tools/bash.go
# → :181-186 — the only use of the sandbox in the bash handler

# The real OS boundary is the Seatbelt profile, opt-in and macOS-only
grep -n 'deny default\|deny network-outbound' cmd/pi-sandbox/pi-profile.sb
```

Contrast: `bash_supervisor.go` (757 LOC) is a concurrency/reaping subsystem, not
a security one.

## Pi's security posture is documented; pi-go's is not

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go/tmp/harness/pi
sed -n '5,7p' packages/coding-agent/docs/security.md
# → "Pi can read, change, and execute files with the permissions of the account
#    that started it, and it does not ask for approval before every tool call."

sed -n '29,31p' packages/coding-agent/docs/security.md
# → the documented project-trust / sessionDir ordering gap

ls /Users/dimetron/p6s/pi-dev/pi-go/docs/security.md
# → does not exist
```

## pi-go's prompt-injection scanner has no pi equivalent

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
grep -n 'Critical\|tag\|BiDi\|Variation' internal/audit/chars.go | head
# → :38-61 Unicode tag chars, BiDi overrides, Variation Selectors
#      (named the "Glassworm vector") all Critical

# Applied on the skill load path
grep -n 'auditRejects' internal/extension/skills.go
# → :162 — every candidate scanned before load

# pi has no content scanner; its gate is load-time only
ls packages/coding-agent/src/core/ | grep -i 'audit\|scan'
# → (no equivalent)
```

## CI comparison

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
grep -n '^  [a-z-]*:' .github/workflows/ci.yml
# → vscode, lint, vulncheck, test, test-windows, coverage, build, ...

grep -n 'windows' .github/workflows/ci.yml
# → test-windows runs `go test -count=1 ./...` on windows-latest —
#   "the only job that actually executes the //go:build windows files"

cd tmp/harness/pi
ls .github/workflows/
# → 10 workflows; build-binaries.yml has a windows *binary smoke* matrix but
#   no Windows *test* job
```

## Supply chain

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
grep -n 'attest-build-provenance\|attest-sbom\|syft' .github/workflows/release.yml
# → provenance over archives AND dist/*/pi ("the archive digest is not the
#   digest anyone ends up running"); syft SBOM before GoReleaser because
#   --clean wipes dist/

cd tmp/harness/pi
grep -n 'npm audit' .github/workflows/npm-audit.yml
# → daily, --omit=dev, audit-level=moderate, plus `npm audit signatures`
# → no SBOM, no provenance attestation
```

## Custom static checkers

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go/tmp/harness/pi
grep -oE '"check:[a-z-]+"' package.json | sort -u
# → check:pinned-deps, check:runtime-deps, check:ts-imports, check:entry-graphs,
#   check:shrinkwrap, check:install-lock:coding-agent, check:browser-smoke
#   (+ check:pinned-deps, check:model-data, check:model-catalog,
#    check:package-install) = 9 project-specific checkers

# pi-go: one linter, Go only
grep -n '^lint:\|^vet:' Makefile
```

The most interesting checker:

```bash
sed -n '1,9p;36,40p' scripts/check-entry-graphs.mjs
# → "Entry points are cost contracts." Per-entry import-graph budgets.
#   Rationale: one stray `export *` made a 1-file pure function drag a ~37 MB
#   module graph.
```

## Eval coverage gate

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
# The inventory is derived from the real constructors, not hand-kept
grep -n 'CoreTools\|tools.CoreTools' internal/eval/inventory.go
# → :48, :67

# The gate itself — a plain unit test, runs in `make test`
grep -n 'TestScenarios_CoverInventory' internal/eval/scenarios/*.go
# UnmappedTools (coverage.go:166) and UnknownTargets (:181) back it
```

pi's block-on-missing rule:

```bash
cd tmp/harness/pi
grep -n 'OperationalMetricTotal\|blocked\|withheld' packages/evals/src/report.ts | head
# → :60 total: number|null (never coerced to 0)
# → :249-270 a pair contributes only when both arms produce exactly one score
# → :379 headline pass rates withheld, exit nonzero
```

## Models: what is embedded and refreshed

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
ls internal/provider/modeldata/
# → models-{anthropic,openai,gemini,mistral,openrouter,xai}.json
#   context-windows.json, llm-prices-*.json, ollama-cloud-pricing.json,
#   modelsdev-pricing.json

# Union, never replace
sed -n '44,62p' internal/provider/catalog.go
# → the comment at :44-52 records the bug: the cache used to be returned
#   ALONE, so a short refresh silently dropped the embedded snapshot

# Ollama pricing is scraped; README documents a manual step
sed -n '55,66p' internal/provider/modeldata/README.md

cd tmp/harness/pi
# Generated, gitignored, sha256-gated
grep -n 'MODEL_DATA_SCHEMA_VERSION\|structureHash' packages/ai/src/model-data.ts | head
# → :5 schema version 6; :122-130 structureHash; :240-259 comparison to manifest
grep -n 'check:model-data' packages/ai/package.json
# → wired into build:offline, so a stale shard fails the build
```

## Prompt caching

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
sed -n '4,6p' internal/provider/cache_apply.go
# → "Today only the Anthropic provider honors it; the openai, gemini, mistral,
#    xai, ollama, and azure builders ignore the option entirely."
grep -c 'cache_control' internal/provider/anthropic_caching.go
# → 3 breakpoints (Anthropic allows 4; the comment at :13-14 records the choice)

cd tmp/harness/pi
grep -n 'ttl' packages/ai/src/api/anthropic-messages.ts | head -3
# → :91 cacheControl ttl; drives Usage.cacheWrite1h billing at 2×
```

## Auth

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
grep -n 'func Providers' internal/auth/auth.go
sed -n '135,170p' internal/auth/auth.go
# → ONE provider: codex. 1463 LOC for one OAuth provider.
grep -n '0o600\|0600\|0700' internal/auth/auth.go
# → plaintext ~/.pi-go/.env, dir 0700, file 0600

cd tmp/harness/pi
ls packages/ai/src/auth/oauth/*.ts | grep -v -E 'load|index|types|errors|callback-server|pkce|device-code' | wc -l
# → 9 provider OAuth flows
sed -n '113,126p' packages/ai/src/auth/resolve.ts
# → double-checked refresh inside the store's only write path
```

## What was NOT verified

Stated explicitly, per the repo's own rule about labeling unverified figures:

- **The 43-provider figure** appears in several upstream-facing descriptions. The
  verified count from `builtinProviders()` is **42**. The report uses 42
  throughout.
- **pi's docs count** is sometimes given as 47. Verified `*.md` count is **40**;
  47 presumably includes `docs.json`, `images/`, and non-markdown assets.
- **Provider default base URLs** for pi's 42 were read from the provider modules
  and `generate-models.ts`; a few regional variants were summarized rather than
  individually verified.
- **Test-LOC totals** for pi exclude `node_modules` and `dist`; whether
  `packages/*/test/**` is the complete test surface was not independently
  confirmed.
- **Effort estimates** in [07](07-gaps-and-recommendations.md) are judgement, not
  measurement. They assume someone already knows the codebase.