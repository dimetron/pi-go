# Deploy the pi-go Harness on local Kind

This directory contains `harness.yaml`, a deployable kagent `Harness` and
`AgentTemplate` pair for running pi-go on Agent Substrate. The adapter image is
built from `Dockerfile` in this directory; see
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

Copy the `linux/arm64` manifest digest and substitute it in `harness.yaml`:

```yaml
image: localhost:5001/dimetron/pi-go-kagent@sha256:<64-hex-digit-digest>
```

A public release gets the same image with both architectures from the release
workflow: `ghcr.io/<owner>/pi-go-kagent:<tag>` (see
[release.yml](../../.github/workflows/release.yml)).

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

Use the printed digest in `harness.yaml`, for example:

```yaml
image: pi-go-kagent@sha256:<digest>
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

Never place a real API key in `harness.yaml`, shell history, or a Dockerfile.

## Apply the Harness and template

From this directory:

```bash
kubectl apply -f harness.yaml
```

Verify the Harness and template:

```bash
kubectl get harness pi-go -n kagent
kubectl get agenttemplate pi-go -n kagent

kubectl get harness pi-go -n kagent -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\\n"}{end}'
kubectl get agenttemplate pi-go -n kagent -o jsonpath='{range .status.harnesses[0].conditions[*]}{.type}={.status} {.reason}: {.message}{"\\n"}{end}'
```

Expected conditions:

```text
Harness:       Ready=True
AgentTemplate: Accepted=True
               ResolvedRefs=True
               Compatible=True
               Ready=True
```

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
