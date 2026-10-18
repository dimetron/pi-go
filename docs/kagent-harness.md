# Running pi-go as a kagent Harness

pi-go ships an A2A adapter that lets it run as a custom agent runtime inside
[kagent](https://kagent.dev) on Agent Substrate. Instead of speaking ACP over
stdin/stdout, the adapter exposes the A2A protocol over HTTP + gRPC, implements
the Substrate runtime contract, and is packaged as an OCI image with the tools
a coding agent needs.

Docs in this section:

| File | Purpose |
|---|---|
| `specs/kagent/Dockerfile` | Builds the pi-go-kagent adapter image |
| `specs/kagent/image-digests.sh` | Prints the digest-pinned amd64/arm64 image refs for a release |
| `specs/kagent/DEPLOY.md` | Full end-to-end deploy steps on local Kind |
| `specs/kagent/manifests.yaml.tmpl` | `Harness` + `AgentTemplate` template |
| `specs/kagent/render-manifests.sh` | Renders that template against a digest-pinned image |
| `scripts/install-kagent.sh` | One-liner installer: applies a release's manifest to a cluster |
| `docs/kagent-mcp.md` | Wiring MCP servers into the harness, with the remote GitHub MCP server as the worked example |

## What the adapter image contains

The image runs `pi a2a`, which serves:

- **JSON-RPC A2A + gRPC A2A** on `$PORT` (default `80`) — the kagent gateway
  dials the runtime over gRPC and routes HTTP A2A by content type
- **agent card** at `/.well-known/agent-card.json` (honors the
  `KAGENT_AGENT_CARD_JSON` the compiler injects)
- **readiness** at `/readyz` on `:8081` (Substrate ActorTemplate probe)
- **durable pi state** under `/data` (Substrate durable dir mount)

The Substrate sandbox blocks `apt-get` at runtime (`setgroups` is denied in the
gVisor sandbox), so the image bakes in the tools a coding agent needs:

| Tool | Why |
|---|---|
| `git` | clone repos, `git diff/log/status` tools |
| `ripgrep` (`rg`) | the grep tool's fast path |
| `curl`, `wget` | fetch tarballs and API payloads |
| `jq` | JSON manipulation |
| `python3` + `pip` | scripting/parsing helper |
| `gh` | GitHub CLI — PRs, issues, releases, `gh api` (pinned release + SHA-256) |
| `gcx` | Grafana `gcx` — Grafana Cloud/LGTM CLI (pinned release + SHA-256) |
| `go` | build/run Go projects (installed via `arkade system install go`) |
| `arkade` | on-demand CLI installs (`arkade get kubectl helm rg ...`) |
| `build-essential`, `unzip`, `xz-utils`, `zip`, `tar` | build + archive support |
| `vim-tiny` | minimal editor for the bash tool |

## Build the image

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go

# arm64 for the local Kind cluster (Substrate worker nodes are arm64)
docker buildx build --platform linux/arm64 --push \
  -t localhost:5001/dimetron/pi-go-kagent:dev \
  -f specs/kagent/Dockerfile .

# Multi-arch for a public release (both amd64 and arm64)
docker buildx build --platform linux/amd64,linux/arm64 --push \
  -t ghcr.io/dimetron/pi-go-kagent:<tag> \
  -f specs/kagent/Dockerfile .
```

The release workflow (`release.yml`) already builds and pushes the image to
`ghcr.io/<owner>/<repo>-kagent:<tag>` on every `v*` tag push. To identify the
immutable digests for a release — the manifest-list digest plus the
per-architecture `linux/amd64` and `linux/arm64` digests — run:

```bash
./specs/kagent/image-digests.sh <tag>   # e.g. v0.0.87
```

## Deploy on kagent

See [`specs/kagent/DEPLOY.md`](../specs/kagent/DEPLOY.md) for the full walkthrough.
The short version:

1. Build/push the image and copy its immutable digest
   (`docker buildx imagetools inspect ...`), or run
   `specs/kagent/image-digests.sh <tag>` for a release.
2. Render the manifest against that digest:
   `specs/kagent/render-manifests.sh --image <ref>@sha256:<digest>`.
3. Apply it: pipe the render into `kubectl apply -f -`.
4. Verify the Harness and AgentTemplate conditions are all `True`.
5. Create an `AgentInstance` through the kagent gRPC API and confirm it reaches
   `AGENT_INSTANCE_STATE_READY`, then chat with it from the kagent UI.

## Durable state: sessions survive suspend, restore and replacement

Substrate checkpoints an idle actor and may restore it elsewhere, or replace it
outright. Either way the actor comes back as a new process, and the only thing
that carries over is the durable directory mounted at `/data`. The adapter is
built so that is enough:

- The image sets `HOME=/data`, so everything pi keeps under `~/.pi-go` — session
  transcripts, the memory store, server logs — lands on the durable mount. A
  home under `/home/pi` would be lost with the pod.
- Each A2A context id is a pi session id, and its transcript is persisted under
  `/data/.pi-go/sessions/<context-id>/` as it happens (`events.jsonl` is
  append-on-write, so nothing is lost to a checkpoint taken between turns).
- On the next message for a context the restarted process opens the stored
  transcript instead of starting a fresh one, and the conversation continues.

Set `PI_SESSIONS_DIR` in the Harness `spec.env` to keep transcripts somewhere
other than the default `$HOME/.pi-go/sessions`. Each AgentInstance has its own
durable directory, so one actor cannot read another's transcripts.

The ACP server (`pi acp-server`) persists sessions the same way and exposes
them through `session/load`, `session/resume` and `session/list`.

## Calling other agents from inside a harness

A harness built by `pi a2a` also gets three tools that talk to the kagent
control plane, so it can discover and call sibling agents without each one
being written into `config.json`:

| Tool | What it does |
|---|---|
| `kagent_list_agents` | Lists agent instances in the namespace, with the `instance_id` needed to call one. Optional `agent_template` / `harness` filters. |
| `kagent_create_agent` | Creates an instance from a `(harness, agent_template)` pair and returns its `instance_id`. |
| `kagent_call_agent` | Sends a prompt to an agent over A2A and returns its reply. Accepts an `instance_id` directly, or a template name (an existing READY instance is reused, one is created if none is running). |

They are wired only into the A2A harness server (`buildToolsetsFromCfg(cfg,
true)` in `internal/acp/server/runtime.go`), not into an editor's `pi
acp-server` backend, which has no controller to talk to. Nothing is registered
unless a controller URL is known, so a `pi a2a` process outside kagent simply
gains no tools.

The endpoint comes from the environment the kagent compiler injects:

| Variable | Meaning | Default |
|---|---|---|
| `KAGENT_API_URL` | Controller base URL, e.g. `http://kagent-controller.kagent:8083` | none — no URL means no tools |
| `KAGENT_NAMESPACE` | Namespace the instance calls are scoped to | `kagent` |
| `PI_KAGENT_USER` | Identity sent as `x-user-id` | `admin@kagent.dev` |

Note that a **managed** (`spec.kagent`) harness gets `KAGENT_API_URL` injected
by the compiler, but a BYO harness does not — add it to `spec.env` explicitly
if you switch the harness to `spec.byo`.

## Model configuration

Set `PI_MODEL` and `PI_BASE_URL` in the Harness `spec.env` to point at the
provider. The template's `modelConfig` names a kagent `ModelConfig`; on the
local cluster that is `default-model-config` (Ollama at
`http://host.docker.internal:11434` from the worker nodes). Keep credentials in
kagent Secrets, never in the image or manifest.
