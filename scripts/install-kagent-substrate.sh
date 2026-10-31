#!/bin/sh
# Install kagent v1.0.0-rc1 on Agent Substrate v1.0.0-rc1 into a Kind cluster.
#
#   ./scripts/install-kagent-substrate.sh --dry-run     # print, change nothing
#   ./scripts/install-kagent-substrate.sh               # apply
#
# Replaces the old bring-your-own-Substrate flow. From rc1 the kagent install
# owns the Substrate control plane itself: kagent-crds, substrate-crds,
# substrate-podcert, substrate, then kagent. The previous setup installed
# Substrate by hand and pointed kagent at it, which is why the WorkerPool was a
# hand-scaled object outside the chart and kept reverting through
# last-applied-configuration.
#
# Prerequisites (not automated here):
#   - kubectl context on a Kind cluster
#   - the substrate-podcert prerequisite: cert-manager CRDs and a running
#     cert-manager, since the bundled database validates client certificates
#   - a provider credential; pass --provider and set the matching env var
#
# Values are pinned rather than defaulted where a default would silently change
# production behaviour (worker replicas, WorkerPool ownership).
set -eu

KAGENT_VERSION="${KAGENT_VERSION:-1.0.0-rc1}"
NAMESPACE="${NAMESPACE:-kagent}"
ATE_NAMESPACE="${ATE_NAMESPACE:-ate-system}"
PODCERT_NAMESPACE="${PODCERT_NAMESPACE:-podcertificate-controller-system}"

# 5, not the chart default of 1. Measured on the alpha generations: one worker
# cannot serve the AgentTemplates plus a golden-snapshot build, and the A2A
# gateway surfaces the shortfall as a 503 "no free workers available".
WORKER_REPLICAS="${WORKER_REPLICAS:-5}"
SANDBOX_CLASS="${SANDBOX_CLASS:-gvisor}"

PROVIDER="${PROVIDER:-openAI}"
DRY_RUN=0
SKIP_CRDS=0

die() { echo "install-kagent-substrate.sh: $*" >&2; exit 1; }
note() { echo "install-kagent-substrate.sh: $*" >&2; }

usage() {
  cat <<'USAGE'
Install kagent v1.0.0-rc1 + Agent Substrate v1.0.0-rc1.

  ./scripts/install-kagent-substrate.sh [options]

Options:
  --dry-run             print the helm commands, change nothing
  --namespace NS        kagent namespace (default: kagent)
  --worker-replicas N   Substrate WorkerPool replicas (default: 5)
  --sandbox-class C     gvisor or microvm (default: gvisor)
  --provider NAME       model provider for the default ModelConfig (default: openAI)
  --skip-crds           skip the two CRD releases (when CRDs are already installed)
  -h, --help            this help

Environment:
  KAGENT_VERSION        release to install (default: 1.0.0-rc1)
  WORKER_REPLICAS       same as --worker-replicas
  OPENAI_API_KEY        credential when --provider is openAI
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --namespace) shift; NAMESPACE="${1:?--namespace needs a value}" ;;
    --worker-replicas) shift; WORKER_REPLICAS="${1:?--worker-replicas needs a value}" ;;
    --sandbox-class) shift; SANDBOX_CLASS="${1:?--sandbox-class needs a value}" ;;
    --provider) shift; PROVIDER="${1:?--provider needs a value}" ;;
    --skip-crds) SKIP_CRDS=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

case "$SANDBOX_CLASS" in
  gvisor)  WORKER_IMAGE="ghcr.io/kagent-dev/substrate/ateworker-gvisor:v${KAGENT_VERSION}" ;;
  microvm) WORKER_IMAGE="ghcr.io/kagent-dev/substrate/ateworker-microvm:v${KAGENT_VERSION}" ;;
  *) die "--sandbox-class must be gvisor or microvm, got: $SANDBOX_CLASS" ;;
esac

case "$WORKER_REPLICAS" in
  ''|*[!0-9]*) die "--worker-replicas must be a number, got: $WORKER_REPLICAS" ;;
esac

# The controller talks to Substrate over mTLS on the ate-system API service, and
# the bundled PostgreSQL validates client certs, so both switches are required
# rather than cosmetic. The SPIFFE ids below are what substrate-podcert issues
# against; renaming the controller ServiceAccount breaks the chain silently.
KAGENT_VALUES=$(cat <<YAML
config:
  substrate:
    apiEndpoint: dns:///api.${ATE_NAMESPACE}.svc:443
    routerUrl: https://atenet-router.${ATE_NAMESPACE}.svc:443
controller:
  substrateClientCertificate:
    enabled: true
database:
  postgres:
    clientCertificate:
      enabled: true
providers:
  default: ${PROVIDER}
substrateWorkerPool:
  create: true
  name: kagent-default
  replicas: ${WORKER_REPLICAS}
  sandboxClass: ${SANDBOX_CLASS}
  workerImage: ${WORKER_IMAGE}
YAML
)

# Scoped authorization (rc1): Substrate inventory is granted per namespace rather
# than read globally, and the controller is the only identity allowed to ingress
# through the router. Without these the controller reconciles but cannot list
# workers or route A2A traffic.
SUBSTRATE_VALUES=$(cat <<YAML
ateApi:
  authorization:
    bootstrapOwners:
    - spiffe://cluster.local/ns/${NAMESPACE}/sa/kagent-controller
    - system:serviceaccount:${ATE_NAMESPACE}:ate-client
atenetRouter:
  ingressAllowedSPIFFEIDs:
  - spiffe://cluster.local/ns/${NAMESPACE}/sa/kagent-controller
credentialProvider:
  namespacePolicies:
  - allowedNamespaces:
    - ${NAMESPACE}
    atespace: ${NAMESPACE}
postgres:
  clientCertificates:
    enabled: true
preview:
- PerTemplateSnapshotLocation
YAML
)

PODCERT_VALUES=$(cat <<YAML
postgresClients:
- ${NAMESPACE}/kagent-controller=kagent_owner_user
- ${NAMESPACE}/kagent-controller=kagent_readwrite_user
YAML
)

# Three different name pairs meet here and only one of them is interchangeable.
# Both spellings are load-bearing:
#   - kagent's controller reads these *pairs* because they must match the
#     `postgres.podcert.ate.dev/username` annotation in its own deployment
#     (kagent/templates/controller-deployment.yaml), which issues the certs.
#   - The SQL roles the controller then assumes are kagent_owner /
#     kagent_readwrite (config.database.owner.role, config.database.readWrite.role).
# The substrate chart, left alone, defaults to substrate_owner /
# substrate_readwrite for its own database — unrelated to these, because kagent
# runs its own bundled Postgres. Getting a pair wrong here fails at connect time
# with a certificate error, not at install time.

run_helm() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '  %s\n' "$*"
  else
    "$@"
  fi
}

if [ "$DRY_RUN" -eq 1 ]; then
  echo "# kagent ${KAGENT_VERSION} + Substrate ${KAGENT_VERSION}"
  echo "# namespace=${NAMESPACE} workers=${WORKER_REPLICAS} sandbox=${SANDBOX_CLASS}"
  echo "#"
  echo "# Review these values before applying. Each block is passed to its release"
  echo "# with -f, and a wrong SPIFFE id there fails at runtime with a permission"
  echo "# error rather than at install time."
  echo
  for pair in "substrate-podcert:$PODCERT_VALUES" "substrate:$SUBSTRATE_VALUES" "kagent:$KAGENT_VALUES"; do
    echo "## ${pair%%:*} values"
    printf '%s\n' "${pair#*:}" | sed 's/^/    /'
    echo
  done
  echo "# helm releases that would run:"
fi

if [ "$SKIP_CRDS" -eq 0 ]; then
  echo "==> kagent-crds"
  run_helm helm upgrade --install kagent-crds \
    oci://ghcr.io/kagent-dev/kagent/helm/kagent-crds \
    --version "$KAGENT_VERSION" --namespace "$NAMESPACE" --create-namespace \
    --wait --wait-for-jobs --timeout 5m

  echo "==> substrate-crds"
  run_helm helm upgrade --install substrate-crds \
    oci://ghcr.io/kagent-dev/substrate/helm/substrate-crds \
    --version "$KAGENT_VERSION" --namespace "$ATE_NAMESPACE" --create-namespace \
    --wait --wait-for-jobs --timeout 5m
fi

echo "==> substrate-podcert"
run_helm helm upgrade --install substrate-podcert \
  oci://ghcr.io/kagent-dev/substrate/helm/substrate-podcert \
  --version "$KAGENT_VERSION" --namespace "$PODCERT_NAMESPACE" --create-namespace \
  --wait --wait-for-jobs --timeout 5m \
  -f /dev/stdin <<YAML
$PODCERT_VALUES
YAML

echo "==> substrate"
run_helm helm upgrade --install substrate \
  oci://ghcr.io/kagent-dev/substrate/helm/substrate \
  --version "$KAGENT_VERSION" --namespace "$ATE_NAMESPACE" --create-namespace \
  --wait --wait-for-jobs --timeout 10m \
  -f /dev/stdin <<YAML
$SUBSTRATE_VALUES
YAML

echo "==> kagent"
run_helm helm upgrade --install kagent \
  oci://ghcr.io/kagent-dev/kagent/helm/kagent \
  --version "$KAGENT_VERSION" --namespace "$NAMESPACE" --create-namespace \
  --wait --wait-for-jobs --timeout 10m \
  -f /dev/stdin <<YAML
$KAGENT_VALUES
YAML

if [ "$DRY_RUN" -eq 1 ]; then
  echo
  echo "# Not run in dry-run:"
  echo "#   kubectl -n $NAMESPACE create secret generic pi-go-provider --from-literal=... "
  echo "#   ./scripts/install-kagent.sh --tag <pi-go release>"
  exit 0
fi

# rc1 removed the public SuspendSession/ResumeSession RPCs. Anything scripted
# against the old resume path — including older operator tooling — will fail
# with "unknown method" rather than a capacity error, which is a different
# failure than the 503 this repository previously documented.
note "installed kagent ${KAGENT_VERSION}; verifying"
kubectl get workerpool -n "$NAMESPACE" 2>/dev/null || \
  note "WorkerPool is cluster-scoped in some builds; check: kubectl get workerpool -A"
kubectl get deploy -n "$NAMESPACE" kagent-default 2>/dev/null || true
kubectl get svc -n "$NAMESPACE" kagent-controller 2>/dev/null || true

cat <<'NEXT'

Next:
  1. Confirm the controller is serving:
       kubectl -n kagent logs deploy/kagent-controller --tail=50
  2. Confirm the worker pool is ready:
       kubectl -n kagent get workerpool,deploy -l app.kubernetes.io/name=kagent
  3. Deploy the pi-go Harness on top:
       ./scripts/install-kagent.sh --tag <pi-go release tag>
NEXT
