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
| `specs/kagent/DEPLOY.md` | Full end-to-end deploy steps on local Kind |
| `specs/kagent/harness.yaml` | `Harness` + `AgentTemplate` manifests |

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
| `go` | build/run Go projects (installed via `arkade system install go`) |
| `arkade` | on-demand CLI installs (`arkade get kubectl helm gh rg ...`) |
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
`ghcr.io/<owner>/<repo>-kagent:<tag>` on every `v*` tag push.

## Deploy on kagent

See [`specs/kagent/DEPLOY.md`](../specs/kagent/DEPLOY.md) for the full walkthrough.
The short version:

1. Build/push the image and copy its immutable digest
   (`docker buildx imagetools inspect ...`).
2. Substitute the digest into `specs/kagent/harness.yaml`.
3. `kubectl apply -f specs/kagent/harness.yaml`.
4. Verify the Harness and AgentTemplate conditions are all `True`.
5. Create an `AgentInstance` through the kagent gRPC API and confirm it reaches
   `AGENT_INSTANCE_STATE_READY`, then chat with it from the kagent UI.

## Model configuration

Set `PI_MODEL` and `PI_BASE_URL` in the Harness `spec.env` to point at the
provider. The template's `modelConfig` names a kagent `ModelConfig`; on the
local cluster that is `default-model-config` (Ollama at
`http://host.docker.internal:11434` from the worker nodes). Keep credentials in
kagent Secrets, never in the image or manifest.
