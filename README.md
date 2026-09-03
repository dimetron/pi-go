# pi-go

[![CI](https://github.com/dimetron/pi-go/actions/workflows/ci.yml/badge.svg)](https://github.com/dimetron/pi-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dimetron/pi-go.svg)](https://pkg.go.dev/github.com/dimetron/pi-go)
[![Go Version](https://img.shields.io/github/go-mod/go-version/dimetron/pi-go)](go.mod)
[![License](https://img.shields.io/github/license/dimetron/pi-go)](LICENSE)
[![Release](https://img.shields.io/github/v/release/dimetron/pi-go?logo=github&label=latest)](https://github.com/dimetron/pi-go/releases)
[![codecov](https://codecov.io/gh/dimetron/pi-go/graph/badge.svg)](https://codecov.io/gh/dimetron/pi-go)
[![GitHub stars](https://img.shields.io/github/stars/dimetron/pi-go?style=social)](https://github.com/dimetron/pi-go)
[![GitHub issues](https://img.shields.io/github/issues/dimetron/pi-go)](https://github.com/dimetron/pi-go/issues)

A terminal-based coding agent built on [Google ADK Go](https://adk.dev/). It connects to multiple LLM providers, runs
sandboxed tools, integrates LSP, and ships with a process-based subagent system.

![pi-go TUI](docs/screen/pi-go.gif)

## Features

- **Multi-provider LLM** — Claude (Anthropic), GPT/O-series (OpenAI), Gemini (Google), Mistral, Grok (xAI), Azure OpenAI, OpenRouter, OpenCode, and Ollama (local or cloud) for models
- **Sandboxed tools** — read, write, edit, shell, grep, find, tree, and git operations. All tools are restricted to the project directory via `os.Root`.
- **Interactive TUI** — Bubble Tea v2 with Markdown rendering (Glamour), slash commands, and theming
- **Session persistence** — JSONL append-only event logs with branching, compaction, and resume
- **Model roles** — Named configurations (default, smol, slow, plan, commit) selectable via CLI flags
- **Subagents** — Process-based multi-agent system with types: explore, plan, designer, reviewer, task, quick_task
- **LSP** — JSON-RPC client for Go, TypeScript/JS, Python, and Rust, with auto-format and diagnostics hooks
- **AI Git tools** — Repository overview, file diffs, hunk parsing, and LLM-generated conventional commits (`/commit`)
- **RPC server** — Unix socket JSON-RPC 2.0 for IDE/editor integration
- **Memory Palace** — 4-layer contextual memory with SQLite storage, semantic embeddings (all-MiniLM-L6-v2), temporal knowledge graph, and project/conversation miners
- **Extensions** — Hooks (shell callbacks), skills (`.SKILL.md` instructions), and Model Context Protocol (MCP) servers
- **Skills audit** — Security scanning for hidden Unicode characters, BiDi attacks, and supply-chain threats in skill files (`pi audit`)

## Architecture

```
cmd/pi/             Entry point — CLI parsing, output mode selection
internal/
├── agent/          ADK agent setup, retry logic, runner
├── cli/            Cobra CLI flags, output modes (interactive, print, json, rpc)
├── config/         Global and project config (roles, hooks, MCP, themes)
├── audit/          Security scanner for skills (hidden Unicode, supply-chain threats)
├── extension/      Hooks, skills, MCP server integration
├── lsp/            LSP JSON-RPC client, language registry, manager, hooks
├── palace/         Memory Palace — drawers, layers, KG, miners, embedder, search
├── provider/       LLM providers implementing genai model interface
├── rpc/            Unix socket JSON-RPC 2.0 server
├── session/        JSONL persistence, branching, compaction
├── subagent/       Process spawner, orchestrator, concurrency pool
├── tools/          Sandboxed tools (read, write, edit, bash, grep, find, git, lsp)
└── tui/            Bubble Tea v2 UI, slash commands, commit workflow
```

### Request flow

```
User input → CLI → Agent → LLM provider → Tool calls → Sandbox → Response → TUI
                     ↕           ↕            ↕
              Session store   Palace       LSP servers
              (JSONL events)  (memory,   (format, diagnostics)
                              KG, search)
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed documentation.

## Installation

### Quick install (recommended)

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.sh | bash
```

This script detects your OS/arch, downloads the latest release binary, and installs it to `/usr/local/bin` (or `~/.local/bin` if needed).

**Windows**

```powershell
powershell -NoProfile -Command "iwr https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.ps1 -UseBasicParsing | iex"
```

Or, from a checkout:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install.ps1
```

Installs `pi.exe` to `%LOCALAPPDATA%\Programs` and adds that directory to your
user `PATH`. Restart the terminal afterwards so the new `PATH` is picked up.
Runs on Windows PowerShell 5.1 (built into Windows 10/11) and PowerShell 7+;
`windows/amd64` only. Set `GITHUB_TOKEN` if you hit the GitHub API rate limit
while it resolves the latest release.

The installer checks the download against the release's `checksums.txt` and
refuses to install on a mismatch. That catches a corrupted or swapped archive,
but not a substituted release — `checksums.txt` travels the same path as the
archive. Run `pi verify` afterwards for the provenance check that does answer
that question — see [Verifying a release](#verifying-a-release).

Windows machines without `bash.exe` on `PATH` — a stock Windows install has
none — run agent commands through `powershell.exe` instead, so write PowerShell
syntax in prompts: `;` or a newline rather than `&&`. Installing
[Git for Windows](https://git-scm.com/download/win) puts a `bash` on `PATH` and
restores the bash behaviour.

### NixOS / Nix

The repository includes a flake that builds `pi-go` reproducibly and exposes a
NixOS module. To install it in a NixOS configuration, add the repository as an
input:

```nix
# flake.nix
{
  inputs.pi-go.url = "github:dimetron/pi-go";

  outputs = { self, nixpkgs, pi-go, ... }: {
    nixosConfigurations.my-host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        pi-go.nixosModules.default
      ];
    };
  };
}
```

Enable it in `configuration.nix`:

```nix
{ programs.pi-go.enable = true; }
```

Then rebuild with `sudo nixos-rebuild switch --flake .`. For a one-off use,
run `nix run github:dimetron/pi-go` or install it into a profile with
`nix profile install github:dimetron/pi-go`.

### go install

```bash
go install github.com/dimetron/pi-go/cmd/pi@latest
```

Make sure your `GOPATH/bin` is in your `PATH`. The binary will be installed as `pi`.

### Build from source

```bash
git clone https://github.com/dimetron/pi-go.git
cd pi-go
go install ./cmd/pi
```

### Pre-built binaries

Download the latest release for your platform from the [Releases page](https://github.com/dimetron/pi-go/releases).

### Verifying a release

`pi verify` checks the running binary against the attestations published for
it, with no other tooling required:

```bash
pi verify                                     # the running binary
pi verify ./pi                                # a specific file
pi verify pi-go_1.2.3_linux_amd64.tar.gz      # a downloaded archive, before extracting
pi verify --json                              # machine-readable
pi verify --sbom > sbom.spdx.json             # print the attested SBOM document
```

```
/usr/local/bin/pi
  sha256:abd70659b49183320320426af4abf34555b031e432aff27afbdbf1be39e1ecff

  ✓ build provenance
      repository  github.com/dimetron/pi-go
      workflow    .github/workflows/release.yml@refs/tags/v1.2.3
      commit      4086645aa1f2c3d4e5f60718293a4b5c6d7e8f90
      run         https://github.com/dimetron/pi-go/actions/runs/1234/attempts/1
      signer      https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v1.2.3
      signed      2026-08-21T12:00:00Z

  ✓ SBOM
      format      SPDX 2.3
      packages    192
      ecosystems  golang 180, github 12
      signer      https://github.com/dimetron/pi-go/.github/workflows/release.yml@refs/tags/v1.2.3
      signed      2026-08-21T12:00:00Z
```

The check starts from the file's SHA-256 and nothing else — the version
compiled into the binary, the name it was installed under and the URL it came
from are all attacker-controlled. That digest is looked up in GitHub's
attestations API and the returned [Sigstore](https://www.sigstore.dev/) bundles
are verified against the public-good Sigstore trust root: certificate chain,
Rekor transparency-log inclusion, signed certificate timestamp, and a
certificate identity that must name **this repository's release workflow,
running on a tag**. A signature from any other workflow, branch or repository
is rejected.

A binary you built yourself has no attestation and reports as unverified. That
is the expected answer, not a failure.

Verification needs network access. The Sigstore trust root is cached in
`~/.pi-go/sigstore` after the first run.

#### Verifying with the GitHub CLI

The same attestations are readable by `gh`, if you would rather not trust the
binary to vouch for itself:

```bash
gh attestation verify ./pi --repo dimetron/pi-go

# SBOM attestation. The predicate type carries the SPDX version syft emitted.
gh attestation verify ./pi --repo dimetron/pi-go \
  --predicate-type https://spdx.dev/Document/v2.3
```

#### What is attested, and what is published

Both the release archives **and the raw binaries inside them** are attestation
subjects. `scripts/install.sh` extracts the binary and puts it on your PATH, so
the archive digest is not the digest you end up running; attesting only the
archive would leave the installed binary unverifiable. The binaries themselves
are not published as release assets — the digest is all verification needs.

Each release publishes SBOMs as assets, in SPDX JSON:

- `pi-go_<version>_<os>_<arch>.tar.gz.sbom.json` — cataloged by syft from the
  contents of that specific archive.
- `pi-go_<version>_sbom.spdx.json` — the aggregate SBOM cataloged from the
  source tree, and the one the SBOM attestation binds to. It covers the Go
  module graph, which is the same across every platform in the build matrix,
  plus the pinned GitHub Actions the release itself was built with.

Regenerate the aggregate SBOM locally with `make sbom` (requires
[syft](https://github.com/anchore/syft)).

## Requirements

- Go 1.27+
- At least one LLM provider API key or a running Ollama instance

## API keys

Set the API key for your provider as an environment variable. The provider is inferred from the model name, so `--model` is usually the only routing you need.

| Provider | Model prefix | API key env var | Base URL env var |
|---|---|---|---|
| Anthropic | `claude-*` | `ANTHROPIC_API_KEY` (or `ANTHROPIC_AUTH_TOKEN`) | `ANTHROPIC_BASE_URL` |
| OpenAI | `gpt-*` | `OPENAI_API_KEY` | `OPENAI_BASE_URL` |
| Google Gemini | `gemini-*` | `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) | `GEMINI_BASE_URL` |
| Mistral | `mistral-*`, `magistral-*` | `MISTRAL_API_KEY` | `MISTRAL_BASE_URL` |
| xAI (Grok) | `grok-*` | `XAI_API_KEY` | `XAI_BASE_URL` |
| OpenRouter | `openrouter/<model>` | `OPENROUTER_API_KEY` | `OPENROUTER_BASE_URL` |
| agentgateway | `agentgateway/<model>` | none (optional `AGENTGATEWAY_API_KEY`) | `AGENTGATEWAY_BASE_URL` (default `http://localhost:4000`) |
| Azure OpenAI | `azure/<deployment>` | `AZURE_OPENAI_API_KEY` | — |
| OpenCode | `opencode/<model>` | `OPENCODE_API_KEY` | `OPENCODE_BASE_URL` |
| Ollama (local) | `ollama/<model>` | none | `OLLAMA_HOST` (default `http://localhost:11434`) |
| Ollama Cloud | `<model>:cloud` | `OLLAMA_API_KEY` | `https://api.ollama.com`, or the local daemon when no key is set |

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export GEMINI_API_KEY="..."
export MISTRAL_API_KEY="..."     # optional — only if you use Mistral models
export XAI_API_KEY="..."         # optional — only if you use Grok models
export OPENROUTER_API_KEY="..."  # optional — only if you use OpenRouter models
export OPENCODE_API_KEY="..."
export OLLAMA_API_KEY="..."   # optional — only to reach Ollama Cloud directly
```

A name with no recognized prefix is rejected rather than guessed at — reach for the `ollama/` prefix or the `:cloud` suffix to name an Ollama model explicitly.

A `:cloud` tag names a model, not a destination. With `OLLAMA_API_KEY` set the
request goes straight to `api.ollama.com`; without one it goes to the local
daemon, which has served cloud models on your `ollama signin` identity since
Ollama 0.12 — so `pi --model deepseek-v3.1:671b-cloud` works with no key at all.
The `ollama/` prefix always means the local daemon, tag notwithstanding, and an
explicit `OLLAMA_HOST` overrides both.

## Build

```bash
make build      # build the pi binary
make test       # run unit tests
make lint       # golangci-lint (vet, staticcheck, errcheck, …)
make e2e        # run E2E integration tests
make clean      # remove binary
```

## Usage

```bash
# Default interactive mode
pi

# Select a model by prefix
pi --model claude:sonnet
pi --model openai:gpt-4o
pi --model gemini:gemini-2.5-pro
pi --model mistral-large-latest
pi --model grok-4.6
pi --model azure/my-gpt5-deployment
pi --model openrouter/google/gemini-3.7-flash
pi --model ollama/gemma4:12b-mlx
pi --model opencode/kimi-k3
pi --model agentgateway/deepseek-v4-flash:0731-cloud
pi --model minimax-m3:cloud # automatically detect ollama if :cloud

# Use model roles
pi --smol          # fast, cheap model
pi --slow          # most capable model
pi --plan          # planning-oriented model

# Additional options
pi --continue      # continue last session
pi --session <id>  # resume specific session
pi --system "..." # custom system instructions
pi --url "..."    # custom API endpoint URL

# Non-interactive modes
pi --mode print "explain this codebase"
pi --mode json "list all TODO comments"
pi --mode socket --socket /tmp/pi-go.sock  # JSON-RPC 2.0 over a Unix socket
pi --mode rpc                              # pi-compatible NDJSON over stdio (for pi-acp)
```

### Slash commands

| Command         | Description                                              |
|-----------------|----------------------------------------------------------|
| `/help`         | Show available commands                                  |
| `/model`        | Switch model mid-conversation                            |
| `/session`      | List and switch sessions                                 |
| `/branch`       | Create a conversation branch                             |
| `/commit`       | Generate and apply a git commit                          |
| `/compact`      | Compact session history                                  |
| `/agents`       | Show running subagents                                   |
| `/history`      | Show command history                                     |
| `/plan`         | Start a Plan-Driven Development (PDD) session (auto-resumes if a spec exists) |
| `/run`          | Execute a spec with task agent                           |
| `/skill-create` | Create a new skill                                       |
| `/skill-list`   | List available skills                                    |
| `/skill-load`   | Reload skills from disk                                  |
| `/memory`       | Memory Palace commands (see below)                       |
| `/audit`        | Scan skills for hidden Unicode threats                   |
| `/clear`        | Clear conversation                                       |
| `/exit`         | Exit the agent                                           |

### Memory Palace

A 4-layer contextual memory system that gives the agent persistent awareness across sessions.

**Layers:**

| Layer | Name | Description |
|-------|------|-------------|
| L0 | Identity | Static identity file |
| L1 | Essential Story | Top-15 drawers by importance, injected into system prompt |
| L2 | On-Demand Recall | Context-filtered drawer chunks |
| L3 | Search | Semantic (embedding) or keyword (FTS5) search |

**CLI commands:**

```bash
# Setup
pi memory model download         # download all-MiniLM-L6-v2 embedding model
pi memory model status           # check model path and status
pi memory init [dir]             # create palace.db + generate mempalace.yaml

# Ingest
pi memory mine <dir>             # mine source files into drawers
pi memory mine --convos <dir>    # mine conversation files (JSONL/text)

# Query
pi memory status                 # palace overview (drawers, wings, rooms, KG)
pi memory search <query>         # semantic or keyword search
pi memory wake-up                # print L0+L1 context for system prompt
pi memory recent [project]       # recent memory observations

# Knowledge Graph
pi memory kg query <entity>      # query triples involving an entity
pi memory kg add <s> <p> <o>     # add a fact triple
pi memory kg timeline <entity>   # chronological timeline of facts
```

**Configuration** via `mempalace.yaml` in the project root:

```yaml
wing: my-project
rooms:
  - name: auth
    patterns: ["internal/auth/**"]
    keywords: [jwt, token, session]
  - name: api
    patterns: ["internal/api/**"]
    keywords: [handler, endpoint, route]
```

When the Palace is enabled, the agent also gains tool access: `palace-search`, `palace-add-drawer`, `palace-kg-query`, `palace-kg-add`, `palace-diary-write`, `palace-traverse`, and more.

### Security audit

```bash
# Scan all skill files for hidden Unicode characters
pi audit

# Scan with verbose output (include info-level findings)
pi audit -v

# Output as JSON for CI pipelines
pi audit --format json --output report.json

# Auto-remove dangerous characters (creates .bak backups)
pi audit --strip

# Preview what would be removed
pi audit --strip --dry-run

# Scan a specific file
pi audit --file path/to/SKILL.md
```

Skills are automatically scanned on load — skills with critical findings (Unicode tags, BiDi overrides, variation selector attacks) are blocked from loading.

## Configuration

Pi reads configuration from `~/.pi-go/config.json` (global) and `.pi-go/config.json` (project-local):

- **Model roles** — Map role names to specific model strings
- **Hooks** — Shell commands triggered on tool events (e.g., post-write formatting)
- **MCP servers** — External tool servers via Model Context Protocol
- **Themes** — Terminal color schemes via `theme` config field
- **Base URLs** — Per-provider endpoints via the `baseURLs` field

### Provider base URLs

Self-hosted or LAN endpoints can be declared in config instead of exported in every shell:

```json
{
  "roles": {
    "default": { "model": "ollama/gemma-4-e4b:latest", "provider": "ollama" }
  },
  "baseURLs": {
    "ollama": "http://192.168.1.10:11434"
  }
}
```

Precedence is `--url` flag, then environment variable, then `baseURLs` config. The matching env vars are
`ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GEMINI_BASE_URL`, `MISTRAL_BASE_URL`, `XAI_BASE_URL`, `OPENROUTER_BASE_URL`, `OPENCODE_BASE_URL`, and `OLLAMA_HOST`. A per-shell or
CI override still takes effect. An empty env var does not mask a configured value.

### Ollama generation tuning

Ollama's per-request options are left at the server's own defaults, except for an
output cap. Each knob below is opt-in: unset means the option is not sent at all,
so Ollama's default stays in force. An unparseable value is ignored rather than
fatal — a typo in an env var should not take down an otherwise healthy session.

| Env var | Ollama option | Ollama default | Purpose |
|---|---|---|---|
| `PI_OLLAMA_NUM_PREDICT` | `num_predict` | unlimited | Max tokens generated per turn. Pi defaults this to `16384`; `0` or less removes the cap. |
| `PI_OLLAMA_REPEAT_PENALTY` | `repeat_penalty` | `1.1` | How strongly repeated tokens are penalised. `1.0` disables. |
| `PI_OLLAMA_REPEAT_LAST_N` | `repeat_last_n` | `64` | How many recent tokens the penalty looks back over. `0` disables, `-1` uses the full context. |
| `PI_OLLAMA_PRESENCE_PENALTY` | `presence_penalty` | `0.0` | Flat penalty for tokens already used. |
| `PI_OLLAMA_FREQUENCY_PENALTY` | `frequency_penalty` | `0.0` | Penalty scaled by how often a token was used. |

These matter for models prone to repetition collapse, where a turn stops making
progress and restates the same phrase until it hits a limit. `num_predict` only
bounds how far such a turn runs; it does not stop it degenerating. The penalty
window is the knob that targets the cause, and the default window is narrow:
Ollama penalises repeats across the last 64 tokens only, while observed
degenerate turns cycle on phrases of roughly 25–55 tokens, so a full cycle can
fall outside the window the penalty can see.

```bash
# Widen the repetition window and penalise repeats harder.
export PI_OLLAMA_REPEAT_LAST_N=512
export PI_OLLAMA_REPEAT_PENALTY=1.2
```

Both apply to local Ollama and Ollama Cloud — they share one request path. Raising
these trades diversity for repetition control, and a value that helps one model can
degrade another, so tune per model rather than setting them globally.

### Custom OpenAI-compatible provider

For OpenAI-compatible APIs with model names that Pi cannot infer from a prefix, explicitly set the role provider to
`openai` and point `OPENAI_BASE_URL` at the custom endpoint:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.example.com/v1"
```

```json
{
  "roles": {
    "default": {
      "model": "Qwen3.5-397B-A17B-FP8",
      "provider": "openai"
    }
  }
}
```

Then run Pi normally:

```bash
pi
```

You can also pass the endpoint per invocation:

```bash
OPENAI_API_KEY="your-api-key" pi --model Qwen3.5-397B-A17B-FP8 --url https://api.example.com/v1
```

When `--url` or `OPENAI_BASE_URL` is set, unknown model names are treated as custom OpenAI-compatible models. Setting
`provider: "openai"` in config avoids relying on model-prefix detection.

### MCP server integration

Pi supports the [Model Context Protocol](https://modelcontextprotocol.io/). Use it to extend the agent with external tools. Configure servers in
`~/.pi-go/config.json`:

```json
{
  "mcp": {
    "servers": [
      {
        "name": "tavily-search",
        "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"
      },
      {
        "name": "filesystem",
        "command": "npx",
        "args": [
          "-y",
          "@modelcontextprotocol/server-filesystem",
          "/tmp"
        ]
      }
    ]
  }
}
```

Or in standalone `~/.pi-go/mcp.json` (Claude Desktop compatible format):

```json
{
  "mcpServers": {
    "tavily-search": {
      "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"
    },
    "filesystem": {
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-filesystem",
        "/tmp"
      ]
    }
  }
}
```

**Supported transports:**

- **HTTP/Streamable** — `url` field for cloud-based MCP servers
- **Stdio** — `command` + `args` for local subprocess servers

**Environment variable substitution:** Pi automatically expands `${ENV_VAR}` patterns in server URLs using `.pi-go/.env`

## Editor integration

Pi can run as an Agent Client Protocol (ACP) server. Use it from any IDE that supports ACP.

### Zed

Add pi to Zed's `agent_servers` in your settings:

```json
{
  "agent_servers": {
    "pi": {
      "type": "custom",
      "command": "pi",
      "args": ["acp-server", "--model", "glm-5.2:cloud"],
      "env": {}
    }
  }
}
```

Then invoke via Zed's agent panel (`⌘⇧A` / `Ctrl+Shift+A`) and select "pi". The agent runs in the current Zed project
directory with full access to pi's tools and memory.

### Sessions survive the server

Every ACP session's transcript is written to the same store the terminal uses
(`~/.pi-go/sessions/<session-id>/`, or `$PI_SESSIONS_DIR`), keyed by the ACP
session id. The server implements the protocol's session lifecycle on top of it:

| Method | What pi does |
|---|---|
| `session/load` | Replays the stored transcript to the client, then continues it |
| `session/resume` | Continues the transcript without replaying it |
| `session/list` | Lists stored sessions, newest first, optionally filtered by `cwd` |

So an editor can restart pi — or the machine — and pick a thread up where it
left off, and `pi --session <id>` reopens the same conversation from the terminal.

## kagent

Run pi-go as a custom agent inside [kagent](https://kagent.dev) on Agent Substrate via the A2A
adapter image. See [docs/kagent-harness.md](docs/kagent-harness.md) for the deployment guide and
`specs/kagent/` for the Dockerfile, manifests, and step-by-step deploy notes.

## License

See [LICENSE](LICENSE) for details.
