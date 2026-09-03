# Custom kagent Substrate Harness for pi-go

## Goal

Run `github.com/dimetron/pi-go` as a custom agent runtime behind a kagent
`Harness` and expose it through kagent `AgentInstance`s on Agent Substrate.

The current pi-go `acp-server` is an ACP server over stdin/stdout. Substrate
needs an HTTP A2A runtime. The implementation therefore needs a small adapter
between the two protocols.

## Current compatibility facts

- pi-go repository: `https://github.com/dimetron/pi-go`
- pi-go entry point: `cmd/pi`
- pi-go ACP command: `pi acp-server`
- ACP transport: stdin/stdout
- kagent Harness API: `kagent.dev/v1alpha3`
- kagent Harness runtime adapter currently available: `spec.kagent`
- Substrate workload images must be immutable digest-pinned OCI references.
- `AgentInstance` is created through the kagent gRPC API; it is not a Kubernetes
  resource.

## Desired runtime architecture

```text
kagent AgentInstance A2A gateway
              |
              v
      Substrate actor workload
              |
              v
       pi-go A2A adapter
              |
       A2A <-> ACP bridge
              |
              v
        pi acp-server
              |
              v
          LLM provider
```

## Deliverables

### 1. HTTP A2A adapter

Add a process or package in pi-go that:

- listens on the HTTP port expected by the Substrate workload;
- serves an A2A Agent Card at `/.well-known/agent-card.json`;
- accepts JSON-RPC A2A message requests;
- supports synchronous message execution and streaming where practical;
- translates incoming A2A user messages to ACP `session/prompt` requests;
- translates ACP text and tool-update events into A2A task/artifact events;
- returns explicit A2A failed-task responses for startup, provider, and bridge
  failures;
- handles graceful shutdown and peer disconnects;
- writes diagnostics to stderr or a configured log file, never into the ACP
  stdout stream;
- does not expose arbitrary host paths or arbitrary network proxying.

The adapter must preserve the A2A context/task identity supplied by kagent.
One actor/workload must not accidentally reuse another AgentInstance's
conversation state.

### 2. Runtime image

Create a multi-stage OCI image for pi-go and the adapter.

Requirements:

- build for the target Kind/Substrate architecture (`linux/arm64` locally);
- run as a non-root user;
- include only the pi-go binary, adapter, CA certificates, and required runtime
  files;
- expose the adapter HTTP port;
- use a health/readiness endpoint;
- use an immutable Git commit or release as the source revision;
- push to the local registry for development, for example:
  `localhost:5001/dimetron/pi-go-kagent`;
- deploy the resulting `@sha256:<digest>` reference, never a mutable tag;
- include a reproducible build command and image digest in the spec/test notes.

### 3. Provider configuration

The runtime must support at least one provider without baking credentials into
an image or manifest.

For the initial local test, prefer Ollama because the cluster already has an
Ollama endpoint available through the host:

```text
OLLAMA_HOST=http://host.docker.internal:11434
```

For Anthropic, use a same-namespace Kubernetes Secret and an environment
reference. Do not put the API key in a Harness, AgentTemplate, Dockerfile, or
command line.

Example Secret shape:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: pi-go-provider
  namespace: kagent
type: Opaque
stringData:
  ANTHROPIC_API_KEY: <secret>
```

The adapter must document which provider variables it consumes and fail with a
clear health/startup error when required configuration is missing.

### 4. Durable state

Define how pi-go state maps to the Substrate durable directory.

- Store private runtime/session state below the actor's durable working
  directory.
- Do not use `/tmp` as the only persistence location.
- Keep each AgentInstance's state isolated.
- Ensure actor replacement can resume without sharing state between instances.
- Document whether pi-go's JSONL sessions and Memory Palace database are
  persisted, and which files are safe to restore.
- Add a startup migration/version check for persisted pi-go state if needed.

### 5. kagent Harness and AgentTemplate

After the image exists, apply resources like these:

```yaml
apiVersion: kagent.dev/v1alpha3
kind: Harness
metadata:
  name: pi-go
  namespace: kagent
spec:
  # Use the currently registered Harness compiler while the dedicated custom
  # runtime compiler is not yet implemented.
  kagent: {}
  workload:
    image: localhost:5001/dimetron/pi-go-kagent@sha256:<adapter-image-digest>
  substrate:
    workerPoolRef:
      name: kagent-default
    snapshotPolicy:
      location: gs://ate-snapshots/pi-go/
  allowedAgentTemplates:
    selector:
      matchLabels:
        runtime: pi-go
---
apiVersion: kagent.dev/v1alpha3
kind: AgentTemplate
metadata:
  name: pi-go
  namespace: kagent
  labels:
    runtime: pi-go
spec:
  description: pi-go coding agent running on Agent Substrate
  modelConfig:
    name: <model-config>
  systemPrompt: |
    You are pi-go, a helpful coding and Kubernetes assistant.
    Use tools carefully and explain actions briefly.
```

The `Harness` and `AgentTemplate` must both be accepted and prepared before an
AgentInstance is created.

If the runtime requires pi-go-specific environment variables, add them to the
Harness `spec.env` using literal non-secret values or `credentialRef` for
secrets. Keep provider-specific model selection in a compatible ModelConfig
where possible.

### 6. AgentInstance creation

Create an instance only after template preparation succeeds:

```text
namespace:      kagent
harness:        pi-go
agent_template: pi-go
request_id:     unique-idempotency-key
name:           pi-go example
```

Expected state:

```text
AGENT_INSTANCE_STATE_READY
```

Use the returned AgentInstance ID for A2A routing. Do not route by actor address
or by a mutable Kubernetes object name.

## Implementation plan

1. Inspect pi-go ACP server and A2A client/event types.
2. Define the adapter's internal protocol mapping and identity rules.
3. Implement a minimal non-streaming A2A endpoint with Agent Card and health.
4. Add ACP process lifecycle management and stderr diagnostics.
5. Add streaming/event translation.
6. Add durable-state path configuration and restart handling.
7. Add the multi-stage image and local arm64 build.
8. Run adapter unit tests with a fake ACP peer.
9. Build and push the image; record its digest.
10. Apply the Harness and AgentTemplate to the Kind cluster.
11. Create an AgentInstance and verify `READY`.
12. Send a real A2A prompt through kagent and verify a response.
13. Restart/recreate the actor and verify state isolation and recovery.

## Acceptance criteria

### Build and image

- `go test ./...` passes for the new pi-go adapter code.
- The image builds for the local Kind architecture.
- The image runs as non-root.
- The image is addressed by a digest.

### Kubernetes and Substrate

- `Harness/pi-go` exists in namespace `kagent`.
- `AgentTemplate/pi-go` exists in namespace `kagent`.
- Harness admission selector matches the template label.
- Harness status is `Ready=True`.
- AgentTemplate status contains `Accepted=True`, `ResolvedRefs=True`,
  `Compatible=True`, and `Ready=True`.
- `kagent-default` WorkerPool is ready.
- A created AgentInstance reaches `READY`.

### Protocol

- Agent Card is reachable from the workload.
- A2A request produces a completed response for a deterministic test prompt.
- Provider errors become visible failed tasks, not silent hangs.
- Streaming does not corrupt JSON-RPC or ACP stdout.
- A second AgentInstance cannot read or mutate the first instance's state.

### Security

- No API key is committed, logged, or embedded in an image layer.
- The adapter does not accept arbitrary command execution through A2A.
- The runtime remains non-root and confined to its intended durable directory.
- Mutable image tags are not used in the Harness.

## Known blockers and decisions

- pi-go currently documents ACP server mode, not a Substrate-compatible HTTP
  A2A server.
- kagent's current v2 controller registers the kagent compiler; a dedicated
  `HarnessTypePiGo` compiler is not required for the first adapter experiment,
  but should be considered if pi-go needs runtime-specific compilation.
- A Claude Harness is a separate concern and requires both a Claude compiler
  and an Anthropic ModelConfig/Secret. It should not be mixed into this first
  pi-go-on-Substrate slice.
- The current cluster has a working local Ollama ModelConfig, making Ollama the
  lowest-risk first end-to-end provider.
