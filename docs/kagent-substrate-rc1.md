# Deploying kagent v1.0.0-rc1 on Agent Substrate

Prepares a kagent + Agent Substrate deployment at **rc1**, the first 1.0
release candidate (2026-10-30). Everything below was checked against the rc1
release artifacts, not inferred from release notes.

> **Status of this document.** Written against a machine with no cluster
> running (`kind get clusters` → none). The Helm values, chart renders, and CRD
> schemas were verified offline; nothing here has been applied to a live
> cluster. The one thing not verified end to end is a real install.

## What changed at rc1, and why a version bump is not enough

Three of the four differences break existing deployments rather than extend
them.

### 1. kagent now installs Substrate itself

Previously the local setup installed Substrate by hand and pointed kagent at it.
From rc1 the release is a five-chart stack, in order:

```
kagent-crds          (api.kagent.dev CRDs)
substrate-crds       (ate.dev CRDs)
substrate-podcert    (per-namespace client certs for the bundled Postgres)
substrate            (ate-api-server, atelet, atenet-router, credential provider)
kagent               (controller, UI, WorkerPool)
```

That ordering matters twice: the podcert release must exist before the Postgres
client-certificate authentication can succeed, and the `substrate` release must
be up before the controller starts dialing `dns:///api.ate-system.svc:443`.

This also removes the old scaling trap. The WorkerPool was a hand-scaled object
outside the chart, and its `last-applied-configuration` annotation kept a
`replicas: 1` that a later `kubectl apply` would silently restore. rc1 ships
`substrateWorkerPool.create`, so the pool is chart-managed and the annotation
cannot drift:

```yaml
substrateWorkerPool:
  create: true
  name: kagent-default
  replicas: 5
  sandboxClass: gvisor
  workerImage: ghcr.io/kagent-dev/substrate/ateworker-gvisor:v1.0.0-rc1
```

`workerImage` is **required** when `create=true` — the chart refuses to render
without it:

```
Error: execution error at (kagent/templates/substrate-workerpool.yaml:3:4):
substrateWorkerPool.workerImage is required when substrateWorkerPool.create=true
```

Five replicas, not the chart default of 1, because a single worker cannot serve
several AgentTemplates plus a golden-snapshot build; the gateway surfaces the
shortfall as the documented `503 no free workers available`.

### 2. Harness lost its status subresource

rc1 removed `HarnessStatus`, `HarnessCapabilities`, `Ready`, conditions and the
`status` subresource entirely (`#3454`). Verified against the rc1 CRD: the
Harness schema has no `status` property and `subresources: {}`.

**Any readiness check reading Harness conditions now reads nothing.** The
replacement is `Agent.status`, which carries:

```
conditions, desiredRevision, latestSuccessfulRevision, observedGeneration,
warnings, workloadImage
```

```bash
kubectl get agent -n kagent
kubectl get agent <name> -n kagent \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
kubectl get agent <name> -n kagent \
  -o jsonpath='image={.status.workloadImage} revision={.status.latestSuccessfulRevision}{"\n"}'
```

### 3. Public Suspend/Resume RPCs are gone

`#3409` removed `SessionService.SuspendSession` and `ResumeSession`, their
request/response messages, and the generated client methods. Actor lifecycle is
now internal and runtime-driven.

This matters if you scripted against the old resume path — for example tooling
built around `ate-api-server` `ResumeActor`. Such calls now fail as *unknown
method*, which is a different failure from the capacity 503 and should not be
misdiagnosed as one.

### 4. Authorization is scoped, not global

rc1 enforces owned-resource RBAC and scoped Substrate inventory (`#3299`,
`#3358`, `#3359`, `#3362`). Two things follow:

- Substrate inventory is granted **per namespace** through
  `credentialProvider.namespacePolicies`, not by a global read.
- Only identities in `atenetRouter.ingressAllowedSPIFFEIDs` may route A2A
  traffic, and `ateApi.authorization.bootstrapOwners` must include the
  controller's SPIFFE id.

The SPIFFE ids hardcode the controller ServiceAccount name
(`kagent-controller`). Renaming it through `nameOverride` breaks the chain with
a permission error that does not name the cause.

rc1 also adds Actor JWT credentials — `ModelConfig.spec.actorJwt`,
`RemoteMCPServer.spec.actorJwt`, and
`EgressPolicy.spec.destinations[].authentication.actorJwt`, each taking required
`audiences` (with `kagent-runtime` reserved) and optional `expirationSeconds`.
These are additive; the existing `secretKeyRef` form still works.

## What did *not* change

Worth stating because the version number suggests otherwise.

- **The CRD group version is still `api.kagent.dev/v1alpha3`.** The "v1" in rc1
  is the internal protobuf package, not the Kubernetes API. Existing manifests
  keep their `apiVersion`.
- `Harness.spec.byo`, `spec.kagent`, `spec.workload.command`,
  `spec.workload.image`, `spec.substrate.workerPoolRef` and
  `spec.snapshotPolicy` all survive, so `specs/kagent/manifests.yaml.tmpl`
  applies unchanged.
- `AgentTemplate` and `EgressPolicy` keep their fields.
- `HarnessSpec` still requires exactly one of `[kagent codex claude byo]`. Note
  `codex` and `claude` are new runtime selectors at rc1; pi-go remains `byo`.
- The WorkerPool CRD is **`ate.dev/v1`** (group `ate.dev`, version `v1`) —
  verified from `substrate-crds-1.0.0-rc1`. Some third-party summaries of rc1
  say `substrate.ate.dev/v1alpha1`; that group does not exist in the shipped
  chart, and an object created under it will not be reconciled.

## Prepare the deployment

The repo script wraps the releases in the order that actually succeeds:

```bash
./scripts/install-kagent-substrate.sh --dry-run     # prints, changes nothing
./scripts/install-kagent-substrate.sh               # applies
```

Options: `--worker-replicas`, `--sandbox-class gvisor|microvm`, `--namespace`,
`--provider`, `--ate-setup PATH`, `--skip-crds`. `KAGENT_VERSION` and
`SUBSTRATE_VERSION` override the pinned versions.

### The install order is not the chart's dependency order

Five places need more than `helm install` in sequence. Each was hit bringing rc1
up on aarch64 k3s (v1.36.5+k3s1):

| # | What happens with the naive order | What the script does |
|---|---|---|
| 1 | podcertificate controller mounts three CA-pool Secrets no chart creates, and wedges in `FailedMount` | `kubectl ate admin make-ca-pool` ×3 **first** |
| 2 | ate-api-server mounts `actor-id-ca-pool`, `actor-id-jwt-pool`, `ate-api-authentication` — none are chart output | `ate-setup create …` **before** substrate |
| 3 | kagent cannot reach Substrate's bundled Postgres (see below) | deploy a **separate** Postgres for kagent |
| 4 | controller fails `invalid PostgreSQL connection string` | DSN uses the mounted CA path `/run/substrate-servicedns/…` |
| 5 | substrate chart refuses to render | feature gates on the API server and kubelet |

### kagent does not share Substrate's PostgreSQL

The substrate chart ships this NetworkPolicy, with the intent stated in a comment
above it — *"The bundled postgres is reached only by ate-api-server"*:

```yaml
spec:
  podSelector: {matchLabels: {app: postgres}}
  ingress:
  - from: [{podSelector: {matchLabels: {app: ate-api-server}}}]   # and nothing else
```

kagent's controller runs in namespace `kagent` and can never satisfy
`app=ate-api-server`, so no configuration of the connection string will work.
The script therefore deploys its own `postgres:18-alpine` in the kagent
namespace and points the chart at it through
`database.postgres.{owner,readWrite}ConnectionStringSecretRef` — the same shape
the working Kind install uses.

Two name families meet here and are not interchangeable:

- podcert **logins**: `kagent_owner_user`, `kagent_readwrite_user` — these must
  match the `postgres.podcert.ate.dev/username` annotation in kagent's own
  controller deployment
- SQL **roles** assumed after connecting: `kagent_owner`, `kagent_readwrite`

### Values that matter

`substrate` release:

```yaml
ateApi:
  authorization:
    bootstrapOwners:
    - spiffe://cluster.local/ns/kagent/sa/kagent-controller
    - system:serviceaccount:ate-system:ate-client
atenetRouter:
  ingressAllowedSPIFFEIDs:
  - spiffe://cluster.local/ns/kagent/sa/kagent-controller
credentialProvider:
  namespacePolicies:
  - allowedNamespaces: [kagent]
    atespace: kagent
postgres:
  clientCertificates:
    enabled: true
preview:
- PerTemplateSnapshotLocation
```

`kagent` release:

```yaml
config:
  substrate:
    apiEndpoint: dns:///api.ate-system.svc:443
    routerUrl: https://atenet-router.ate-system.svc:443
controller:
  substrateClientCertificate:
    enabled: true
database:
  postgres:
    ownerConnectionStringSecretRef:
      name: kagent-postgres-owner
      key: ownerConnectionString
    readWriteConnectionStringSecretRef:
      name: kagent-postgres-readwrite
      key: readWriteConnectionString
substrateWorkerPool:
  create: true
  replicas: 5
  workerImage: ghcr.io/kagent-dev/substrate/ateworker-gvisor:v1.0.0-rc1
```

Note the two `*ConnectionStringSecretRef` entries: without them the chart falls
back to the bundled Postgres, which the NetworkPolicy above makes unreachable.

## Prerequisites not automated

- A cluster and a current `kubectl` context.
- **cert-manager**, including its CRDs, because `substrate-podcert` issues the
  Postgres client certificates through it.
- The **feature gates** `PodCertificateRequest`, `ClusterTrustBundle` and
  `ClusterTrustBundleProjection`, plus the `certificates.k8s.io/v1beta1` API.
  On k3s, `scripts/k3s-substrate-feature-gates.yaml` is a ready config; copy it
  to `/etc/rancher/k3s/config.yaml` and restart k3s.
- **`kubectl-ate`** from the substrate release, and **`ate-setup`** built from
  the substrate source tree (`go build ./cmd/ate-setup`). `ate-setup` resolves
  its manifests relative to the repository, so it must be run from inside the
  checkout — from elsewhere it fails with `no go.mod found above /`.
- A model provider credential. The rc1 CLI export assumes `OPENAI_API_KEY`; set
  `--provider` and the matching key for anything else.

## Verify

```bash
# Five releases, all deployed
helm list -A | grep -E 'kagent|substrate'

# WorkerPool is chart-managed and at the expected size
kubectl get workerpool kagent-default -n kagent \
  -o jsonpath='replicas={.spec.replicas} ready={.status.readyReplicas}{"\n"}'

# Controller is talking to Substrate (no TLS/permission errors)
kubectl -n kagent logs deploy/kagent-controller --tail=50 \
  | grep -Ei 'substrate|permission|denied|x509'

# No capacity failures
kubectl logs -n ate-system deploy/ate-api-server --since=15m 2>&1 \
  | grep -c 'no free workers'
```

Then deploy the pi-go Harness on top, which is unchanged from the previous
release flow:

```bash
./scripts/install-kagent.sh --tag <pi-go release tag>
```

## Rollback

rc1 is an RC. Substrate moved from `v0.7.0-alpha2` to `v1.0.0-rc1` in this
release, so a rollback is not Helm-only: CRDs and the Substrate API changed. Pin
the previous working version before installing:

```bash
helm list -A | grep -E 'kagent|substrate'   # record what is there now
```

Rolling back across the rc1 boundary means reinstalling the prior chart set and
its CRDs, then verifying actors still resume — the old public resume RPCs are
gone in rc1, so any tooling depending on them needs its prior version too.

## Not verified

- No cluster was available, so the five releases were never actually applied.
  Chart rendering, value acceptance, and CRD schemas were verified offline.
- The `microvm` sandbox class is untested here; it additionally needs
  KVM-capable nodes and atelet's device plugin.
- Whether `KAGENT_TASK_STORE_ADDRESS` is still required at rc1. `DEPLOY.md`
  measured it as required on `v1alpha3` (`v1.0.0-alpha14-9-g7a0a4fd0`), and rc1
  is a later controller generation, so re-test rather than assume either way.
