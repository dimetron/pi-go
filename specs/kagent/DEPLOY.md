# Deploy the pi-go Harness on local Kind

## Quickest path: install a release

If you only want the latest released harness on a cluster `kubectl` already
points at, skip the rest of this document:

```bash
curl -fsSL https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install-kagent.sh | sh
```

It resolves the latest release, applies its `kagent-manifests.yaml` asset —
already pinned to the digest that release built — and prints what it changed.
`--tag`, `--namespace`, `--dry-run`, `--print`, and `--model` / `--base-url`
(which patch the Harness env after applying) are available via
`| sh -s -- --dry-run`.

The rest of this document covers building and deploying your own image.

This directory contains `manifests.yaml.tmpl`, a kagent `Harness` and
`AgentTemplate` template for running pi-go on Agent Substrate, and
`render-manifests.sh`, which renders it against a digest-pinned image. The
adapter image is built from `Dockerfile` in this directory; see
[docs/kagent-harness.md](/docs/kagent-harness.md) for an overview.

## Prerequisite: HTTP A2A adapter

The Harness workload must listen over HTTP and implement the A2A protocol,
including `/.well-known/agent-card.json`. The pi-go command below is not enough:

```text
pi acp-server
```

That command speaks ACP over stdin/stdout. The Dockerfile in this directory
builds the real adapter: `pi a2a`, which serves JSON-RPC A2A + gRPC A2A on
`$PORT` (80) plus the agent card and `/readyz` readiness probe on :8081.

The Harness must therefore select `spec.byo`, not `spec.kagent`. `spec.kagent`
selects kagent's managed ADK compiler, which drives an ADK agent and expects the
runtime to implement ADK's private task-store gRPC service
(`kagent.runtime.v1.TaskStoreService`) and to receive `KAGENT_API_URL` by
injection. pi-go implements the A2A surface, not that private contract, so a
managed harness admits the template and then fails on the first turn. `spec.byo`
is the adapter contract — with one requirement the CEL rules enforce: BYO
harnesses must set `spec.workload.command`.

### Task persistence

Where task state is written differs by controller version, and pi-go cannot
detect which one it is talking to:

- **Current kagent** writes it itself. The gateway consumes the runtime's A2A
  event stream and persists every task and event into its own PostgreSQL
  (`agent_instance_task`, `agent_instance_task_event`). The runtime only has to
  emit a well-formed task and stream its updates correctly — no RPC.
- **Older controllers** expected the runtime to persist through a private
  `TaskStoreService`. That is what `internal/kagent/taskstore.go` implements, and
  it is enabled only when `KAGENT_TASK_STORE_ADDRESS` is set. There is no
  implicit fallback: pointing it at a controller that does not serve that service
  turns a working turn into a failed RPC.

**Measured on v1alpha3 (v1.0.0-alpha14-9-g7a0a4fd0), the opposite is true:**
this controller *does* serve `kagent.runtime.v1.TaskStoreService`, and a pi-go
harness that leaves `KAGENT_TASK_STORE_ADDRESS` unset keeps its tasks in memory.
Every turn then streams a correct answer and fails immediately afterwards with
`task not found`, because the gateway looks for the task in its own database and
finds nothing. Setting it is what makes the turn succeed:

```yaml
env:
  - name: KAGENT_TASK_STORE_ADDRESS
    value: http://kagent-controller.<namespace>:8083   # gRPC, not HTTP
```

Verified by A/B on one cluster: `kagent: {}` failed, `byo: {}` alone still
failed, and `byo: {}` plus this variable produced a `TASK_STATE_COMPLETED` row
in `kagent.session_task` and a clean reply. Treat the variable as **required**
for this controller generation and re-test before removing it after a kagent
upgrade.

### MCP tools and credentials

An AgentTemplate's `spec.tools` entries are **not** delivered through the Harness
or through config files. The controller compiles them into the `KAGENT_CONFIG_JSON`
environment variable of the workload, under `agent.http_tools`:

```json
{"agent":{"http_tools":[{"params":{"url":"https://api.githubcopilot.com/mcp/x/pull_requests/readonly",
  "headers":{"Authorization":"KAGENT_ENV[KAGENT_CREDENTIAL_...]"}}}]}}
```

pi-go reads that variable at session build time and mounts each entry as an MCP
server (`internal/acp/server/kagent_mcp.go`). Two consequences worth knowing:

- **The credential is a placeholder, not a secret.** `KAGENT_ENV[...]` is
  substituted by the Substrate egress gateway on the wire, so the real token
  never enters the sandbox. pi-go passes the header through verbatim; rewriting
  or dropping it breaks authentication.
- **The egress destination must still be granted.** A credential placeholder
  authorises nothing on its own: an `EgressPolicy` must list the origin (see the
  `EgressPolicy` in the rendered manifest), and an Agent must reference it via
  `spec.egressPolicyRefs`. Without that reference the policy is inert.

## Option A: local registry (recommended)

The local kagent Kind setup uses a registry reachable as `localhost:5001` from
the host and `localhost:5001` in the image references used by the existing
installation. Build and push the adapter from the pi-go repository:

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go

docker buildx build \
  --platform linux/arm64 \
  --push \
  -t localhost:5001/dimetron/pi-go-kagent:dev \
  -f specs/kagent/Dockerfile .

# Read the immutable digest. Do not deploy :dev in the Harness.
docker buildx imagetools inspect \
  localhost:5001/dimetron/pi-go-kagent:dev
```

Copy the manifest-list digest and render the manifest against it:

```bash
./render-manifests.sh \
  --image localhost:5001/dimetron/pi-go-kagent@sha256:<64-hex-digit-digest>
```

The script refuses an image that is not digest-pinned, because Substrate
requires one and a tag would deploy whatever it points at today.

A public release gets the same image with both architectures from the release
workflow: `ghcr.io/<owner>/pi-go-kagent:<tag>` (see
[release.yml](../../.github/workflows/release.yml)). Identify the immutable
digests for a release — the manifest-list digest plus the per-architecture
`linux/amd64` and `linux/arm64` digests — with:

```bash
./image-digests.sh <tag>   # e.g. ./image-digests.sh v0.0.87
```

Pass the digest to `render-manifests.sh --image`. A release also ships the
rendered manifest as the `kagent-manifests.yaml` asset, already pinned to the
image that release built.

Check that the Kind cluster can resolve the registry before applying:

```bash
kubectl config current-context   # expected: kind-kagent
kubectl get nodes
kubectl get workerpool kagent-default -n kagent
```

If the cluster was not created with the local registry mirror, configure the
Kind registry connection first or use Option B. A host-only `localhost` image
reference is not automatically available inside a Kind node.

## Option B: load a locally built image into Kind

This avoids a registry, but the Harness still requires a digest-pinned image
reference. Build the image, load it into the node, inspect its local digest,
and use a node-resolvable reference in the manifest:

```bash
cd /Users/dimetron/p6s/pi-dev/pi-go
docker buildx build --platform linux/arm64 --load \
  -t pi-go-kagent:dev -f specs/kagent/Dockerfile .
kind load docker-image pi-go-kagent:dev --name kagent

docker image inspect pi-go-kagent:dev \
  --format '{{index .RepoDigests 0}}'
```

Render against the printed digest, for example:

```bash
./render-manifests.sh --image pi-go-kagent@sha256:<digest> | kubectl apply -f -
```

Do not use `imagePullPolicy: Always` with an image that exists only in the Kind
node cache. The Harness API does not expose that field directly; configure the
runtime/image policy in the adapter image deployment path if needed.

## Provider configuration

The checked-in cluster currently has an Ollama ModelConfig named
`default-model-config`. Confirm it before deployment:

```bash
kubectl get modelconfig default-model-config -n kagent -o yaml
```

For pi-go's adapter, configure the provider without putting credentials in the
image or this manifest. For example, Ollama can use:

```text
OLLAMA_HOST=http://host.docker.internal:11434
```

For Anthropic, create a Secret and wire it into the Harness `spec.env` using a
`credentialRef` after confirming the adapter's expected variable name:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: pi-go-provider
  namespace: kagent
type: Opaque
stringData:
  ANTHROPIC_API_KEY: <not-committed>
```

Never place a real API key in a rendered manifest, shell history, or a Dockerfile.

## Apply the Harness and template

From this directory:

```bash
./render-manifests.sh --image <ref>@sha256:<digest> | kubectl apply -f -
```

Add `--model` / `--base-url` to point pi at a different LLM endpoint, or
`--no-env` to leave the endpoint to the cluster operator (what a release
renders).

Verify the Harness and template:

```bash
kubectl get harness pi-go -n kagent
kubectl get agenttemplate pi-go -n kagent

kubectl get agenttemplate pi-go -n kagent -o jsonpath='{range .status.harnesses[0].conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

Expected conditions:

```text
AgentTemplate: Accepted=True
               ResolvedRefs=True
               Compatible=True
               Ready=True
```

The conditions live on the AgentTemplate, not the Harness. The Harness has a
`status.conditions` field and a `READY` printer column, but the kagent
controller never writes them — the only time it touches `HarnessStatus` is to
clear it on create (`go/core/internal/grpcserver/harness.go`), and every
Harness reconciler writes `AgentTemplate` status instead
(`go/core/internal/controller/status.go`). So `kubectl get harness` always shows
a blank `READY`, for the stock `kagent` Harness as much as for `pi-go`, and a
`Harness: Ready=True` check would fail on a perfectly healthy deployment. A
Harness that is admitted but broken still shows up as `Ready=False` on its
AgentTemplate.

> **Changed at kagent v1.0.0-rc1.** The controller no longer writes *either*
> status: rc1 removed `HarnessStatus` and the AgentTemplate status subresource
> entirely (`kagent#3454`). On rc1 the `status.harnesses[0].conditions` query
> above returns nothing, and the readiness surface is `Agent.status`
> (`conditions`, `desiredRevision`, `latestSuccessfulRevision`,
> `observedGeneration`, `warnings`, `workloadImage`):
>
> ```bash
> kubectl get agent <name> -n kagent \
>   -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
> ```
>
> The AgentTemplate conditions described above are correct for v1alpha3
> controllers before rc1. See [kagent-substrate-rc1.md](../../docs/kagent-substrate-rc1.md).


If preparation fails, inspect the controller:

```bash
kubectl logs -n kagent deploy/kagent-controller --since=10m \
  | grep -i -E 'pi-go|harness|template|error|failed'
```

## Create an AgentInstance

`AgentInstance` is a gRPC resource, not a Kubernetes resource. After the
AgentTemplate is ready, create an instance with the kagent native gRPC API:

```text
namespace:      kagent
harness:        pi-go
agent_template: pi-go
request_id:     pi-go-local-<unique-value>
name:           pi-go example
```

The response must contain an instance in:

```text
AGENT_INSTANCE_STATE_READY
```

Use the returned AgentInstance ID for A2A routing. Do not route by actor address.

## Verification checklist

```bash
kubectl get workerpool kagent-default -n kagent
kubectl get harness,agenttemplate -n kagent
kubectl get pods -n kagent
```

Then verify all of the following:

- the workload exposes an A2A Agent Card;
- the AgentTemplate has all four successful preparation conditions;
- the AgentInstance reaches `READY`;
- a prompt returns a completed A2A task;
- provider failures appear as failed tasks rather than hangs;
- actor replacement preserves only the same instance's durable state;
- no credentials appear in logs or image layers.
