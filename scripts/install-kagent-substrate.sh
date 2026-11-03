#!/bin/sh
# Install kagent v1.0.0-rc1 on Agent Substrate v1.0.0-rc1.
#
#   ./scripts/install-kagent-substrate.sh --dry-run     # print, change nothing
#   ./scripts/install-kagent-substrate.sh               # apply
#
# The order below is the order that actually succeeds, which differs from the
# chart's own dependency order in four places. Each was hit while bringing rc1 up
# on aarch64 k3s (v1.36.5+k3s1):
#
#   1. The podcertificate controller mounts three CA-pool Secrets that no chart
#      creates. Installing it first leaves the pod wedged in FailedMount
#      ("secret service-dns-ca-pool not found") until something creates them.
#   2. ate-api-server and ate-controller mount actor-id-ca-pool,
#      actor-id-jwt-pool and the ate-api-authentication ConfigMap. Those are not
#      chart output either; they come from `ate-setup create`.
#   3. kagent cannot use Substrate's bundled PostgreSQL. The substrate chart
#      ships a NetworkPolicy admitting only `app=ate-api-server` into
#      `app=postgres`; kagent's controller runs in another namespace, so the
#      connection can never be established. kagent gets its own PostgreSQL.
#   4. The CA bundle the controller mounts is at /run/substrate-servicedns, not
#      the /run/servicedns path the upstream DSN examples use. pgx parses TLS
#      material at startup, so a wrong path fails migrations with "invalid
#      PostgreSQL connection string" rather than a dial error.
#
# Prerequisites (not automated here):
#   - kubectl context on the target cluster
#   - cert-manager including its CRDs: the podcertificate controller requests its
#     CA bundles through it
#   - the feature gates PodCertificateRequest, ClusterTrustBundle and
#     ClusterTrustBundleProjection, plus the certificates.k8s.io/v1beta1 API.
#     The substrate chart refuses to render without them. For k3s see
#     scripts/k3s-substrate-feature-gates.yaml.
#   - kubectl-ate from the substrate release, and the ate-setup binary built from
#     the substrate source tree (it resolves manifests relative to its repo).
set -eu

KAGENT_VERSION="${KAGENT_VERSION:-1.0.0-rc1}"
SUBSTRATE_VERSION="${SUBSTRATE_VERSION:-1.0.0-rc1}"
NAMESPACE="${NAMESPACE:-kagent}"
ATE_NAMESPACE="${ATE_NAMESPACE:-ate-system}"
PODCERT_NAMESPACE="${PODCERT_NAMESPACE:-podcertificate-controller-system}"

# 5, not the chart default of 1. One worker cannot serve several AgentTemplates
# plus a golden-snapshot build, and the A2A gateway surfaces the shortfall as a
# 503 "no free workers available".
WORKER_REPLICAS="${WORKER_REPLICAS:-5}"
SANDBOX_CLASS="${SANDBOX_CLASS:-gvisor}"

# kagent's own database. Separate from Substrate's bundled instance by
# necessity, not preference — see note 3 in the header.
KAGENT_DB_IMAGE="${KAGENT_DB_IMAGE:-postgres:18-alpine}"

PROVIDER="${PROVIDER:-openAI}"
DRY_RUN=0
SKIP_CRDS=0
ATE_SETUP="${ATE_SETUP:-ate-setup}"

die() { echo "install-kagent-substrate.sh: $*" >&2; exit 1; }
note() { echo "install-kagent-substrate.sh: $*" >&2; }

usage() {
  cat <<'USAGE'
Install kagent v1.0.0-rc1 + Agent Substrate v1.0.0-rc1.

  ./scripts/install-kagent-substrate.sh [options]

Options:
  --dry-run             print the commands and values, change nothing
  --namespace NS        kagent namespace (default: kagent)
  --worker-replicas N   Substrate WorkerPool replicas (default: 5)
  --sandbox-class C     gvisor or microvm (default: gvisor)
  --provider NAME       default model provider (default: openAI)
  --ate-setup PATH      ate-setup binary (default: ate-setup on PATH)
  --skip-crds           skip the two CRD releases
  -h, --help            this help

Environment:
  KAGENT_VERSION        kagent release (default: 1.0.0-rc1)
  SUBSTRATE_VERSION     substrate release (default: 1.0.0-rc1)
  WORKER_REPLICAS       same as --worker-replicas
  KAGENT_DB_IMAGE       image for kagent's PostgreSQL (default: postgres:18-alpine)
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --namespace) shift; NAMESPACE="${1:?--namespace needs a value}" ;;
    --worker-replicas) shift; WORKER_REPLICAS="${1:?--worker-replicas needs a value}" ;;
    --sandbox-class) shift; SANDBOX_CLASS="${1:?--sandbox-class needs a value}" ;;
    --provider) shift; PROVIDER="${1:?--provider needs a value}" ;;
    --ate-setup) shift; ATE_SETUP="${1:?--ate-setup needs a value}" ;;
    --skip-crds) SKIP_CRDS=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

case "$SANDBOX_CLASS" in
  gvisor)  WORKER_IMAGE="ghcr.io/kagent-dev/substrate/ateworker-gvisor:v${SUBSTRATE_VERSION}" ;;
  microvm) WORKER_IMAGE="ghcr.io/kagent-dev/substrate/ateworker-microvm:v${SUBSTRATE_VERSION}" ;;
  *) die "--sandbox-class must be gvisor or microvm, got: $SANDBOX_CLASS" ;;
esac

case "$WORKER_REPLICAS" in
  ''|*[!0-9]*) die "--worker-replicas must be a number, got: $WORKER_REPLICAS" ;;
esac

run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '  %s\n' "$*"
  else
    "$@"
  fi
}

# Values go through a file rather than `-f /dev/stdin`. helm opens the values
# path more than once, and the second open of /dev/stdin reads empty — which
# silently drops every value in the block instead of failing.
VALUES_DIR=$(mktemp -d)
trap 'rm -rf "$VALUES_DIR"' EXIT

# helm_install NAME NS CHART VERSION TIMEOUT VALUES
helm_install() {
  name=$1 rel_ns=$2 chart=$3 version=$4 timeout=$5 values=$6
  printf '%s' "$values" > "$VALUES_DIR/$name.yaml"
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '  helm upgrade --install %s %s --version %s -n %s -f <%s values above>\n' \
      "$name" "$chart" "$version" "$rel_ns" "$name"
    return 0
  fi
  helm upgrade --install "$name" "$chart" \
    --version "$version" --namespace "$rel_ns" --create-namespace \
    --wait --wait-for-jobs --timeout "$timeout" -f "$VALUES_DIR/$name.yaml"
}

# --- values -----------------------------------------------------------------

PODCERT_VALUES=$(cat <<YAML
postgresClients:
- ${NAMESPACE}/kagent-controller=kagent_owner_user
- ${NAMESPACE}/kagent-controller=kagent_readwrite_user
YAML
)

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
    ownerConnectionStringSecretRef:
      name: kagent-postgres-owner
      key: ownerConnectionString
    readWriteConnectionStringSecretRef:
      name: kagent-postgres-readwrite
      key: readWriteConnectionString
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

if [ "$DRY_RUN" -eq 1 ]; then
  echo "# kagent ${KAGENT_VERSION} + substrate ${SUBSTRATE_VERSION}"
  echo "# namespace=${NAMESPACE} workers=${WORKER_REPLICAS} sandbox=${SANDBOX_CLASS}"
  echo "#"
  echo "# Review the values below before applying. A wrong SPIFFE id here fails at"
  echo "# runtime as a permission error, not at install time."
  echo
  for pair in "substrate-podcert:$PODCERT_VALUES" "substrate:$SUBSTRATE_VALUES" "kagent:$KAGENT_VALUES"; do
    echo "## ${pair%%:*} values"
    printf '%s\n' "${pair#*:}" | sed 's/^/    /'
    echo
  done
  echo "# commands:"
fi

# --- phase 1: CRDs ----------------------------------------------------------

if [ "$SKIP_CRDS" -eq 0 ]; then
  echo "==> CRDs"
  run helm upgrade --install kagent-crds oci://ghcr.io/kagent-dev/kagent/helm/kagent-crds \
    --version "$KAGENT_VERSION" --namespace "$NAMESPACE" --create-namespace \
    --wait --wait-for-jobs --timeout 5m
  run helm upgrade --install substrate-crds oci://ghcr.io/kagent-dev/substrate/helm/substrate-crds \
    --version "$SUBSTRATE_VERSION" --namespace "$ATE_NAMESPACE" --create-namespace \
    --wait --wait-for-jobs --timeout 5m
fi

# --- phase 2: CA pools ------------------------------------------------------
#
# Everything in phases 3-6 mounts certificates minted from these pools, so they
# must exist first. make-ca-pool leaves an existing pool alone, which keeps a
# rerun from rotating the root out from under already-issued certificates.
echo "==> CA pools"
if [ "$DRY_RUN" -eq 1 ]; then
  echo "  kubectl create namespace $PODCERT_NAMESPACE"
  for pool in service-dns-ca-pool pod-identity-ca-pool postgres-ca-pool; do
    echo "  kubectl ate admin make-ca-pool --name $pool --secret-namespace $PODCERT_NAMESPACE --key-type ED25519 --ca-id $pool"
  done
  for step in actor-id-ca-pool actor-id-ca-certs jwt-authority-pool egress-mitm-ca-pool; do
    echo "  $ATE_SETUP create $step"
  done
else
  command -v kubectl-ate >/dev/null 2>&1 || die \
    "kubectl-ate not found; install it from the substrate ${SUBSTRATE_VERSION} release"
  kubectl create namespace "$PODCERT_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  for pool in service-dns-ca-pool pod-identity-ca-pool postgres-ca-pool; do
    kubectl ate admin make-ca-pool --name "$pool" \
      --secret-namespace "$PODCERT_NAMESPACE" \
      --key-type ED25519 --ca-id "$pool" >/dev/null
  done
  # ate-setup reads manifests relative to the repository it runs from; running it
  # from elsewhere fails with "no go.mod found above /".
  for step in actor-id-ca-pool actor-id-ca-certs jwt-authority-pool egress-mitm-ca-pool; do
    "$ATE_SETUP" create "$step" >/dev/null
  done
fi

# --- phase 3: certificate controller ---------------------------------------

echo "==> substrate-podcert"
helm_install substrate-podcert "$PODCERT_NAMESPACE" \
  oci://ghcr.io/kagent-dev/substrate/helm/substrate-podcert "$SUBSTRATE_VERSION" \
  6m "$PODCERT_VALUES"

# --- phase 4: Substrate -----------------------------------------------------

echo "==> substrate"
helm_install substrate "$ATE_NAMESPACE" \
  oci://ghcr.io/kagent-dev/substrate/helm/substrate "$SUBSTRATE_VERSION" \
  10m "$SUBSTRATE_VALUES"

# --- phase 5: kagent's own PostgreSQL --------------------------------------
#
# Not the bundled instance. The DSNs name the controller's mounted CA bundle at
# /run/substrate-servicedns and its client certificates under
# /run/postgres.podcert.ate.dev. The user names are the podcert logins
# (kagent_*_user); the SQL roles assumed after connecting are kagent_owner and
# kagent_readwrite.
echo "==> kagent PostgreSQL"

kagent_dsn() {
  printf 'postgresql://%s@kagent-postgres.%s.svc:5432/kagent?sslmode=disable' "$1" "$NAMESPACE"
}

if [ "$DRY_RUN" -eq 1 ]; then
  echo "  deploy $KAGENT_DB_IMAGE as kagent-postgres in $NAMESPACE"
  echo "  create logins kagent_owner_user, kagent_readwrite_user; roles kagent_owner, kagent_readwrite"
  echo "  secret kagent-postgres-owner     key=ownerConnectionString"
  echo "  secret kagent-postgres-readwrite key=readWriteConnectionString"
else
  DB_PW=$(kubectl -n "$NAMESPACE" get secret kagent-postgres-superuser -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)
  if [ -z "$DB_PW" ]; then
    DB_PW=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
  fi
  kubectl create secret generic kagent-postgres-superuser -n "$NAMESPACE" \
    --from-literal=password="$DB_PW" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  cat <<YAML | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kagent-postgres
  namespace: ${NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels: {app: kagent-postgres}
  template:
    metadata:
      labels: {app: kagent-postgres}
    spec:
      containers:
      - name: postgres
        image: ${KAGENT_DB_IMAGE}
        env:
        - name: POSTGRES_DB
          value: kagent
        - name: POSTGRES_USER
          value: postgres
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef: {name: kagent-postgres-superuser, key: password}
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
        ports:
        - containerPort: 5432
        volumeMounts:
        - {name: data, mountPath: /var/lib/postgresql/data}
      volumes:
      - name: data
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: kagent-postgres
  namespace: ${NAMESPACE}
spec:
  selector: {app: kagent-postgres}
  ports:
  - {port: 5432, targetPort: 5432}
YAML

  kubectl -n "$NAMESPACE" rollout status deploy/kagent-postgres --timeout=180s

  # Idempotent: roles created only when absent, so a rerun cannot disturb them.
  kubectl -n "$NAMESPACE" exec deploy/kagent-postgres -- psql -U postgres -d kagent -v ON_ERROR_STOP=1 <<'SQL'
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'kagent_owner') THEN
    CREATE ROLE kagent_owner;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'kagent_readwrite') THEN
    CREATE ROLE kagent_readwrite;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'kagent_owner_user') THEN
    CREATE ROLE kagent_owner_user LOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'kagent_readwrite_user') THEN
    CREATE ROLE kagent_readwrite_user LOGIN;
  END IF;
END
$$;
GRANT kagent_owner TO kagent_owner_user;
GRANT kagent_readwrite TO kagent_readwrite_user;
GRANT ALL ON SCHEMA public TO kagent_owner;
SQL

  kubectl create secret generic kagent-postgres-owner -n "$NAMESPACE" \
    --from-literal=ownerConnectionString="$(kagent_dsn kagent_owner_user)" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create secret generic kagent-postgres-readwrite -n "$NAMESPACE" \
    --from-literal=readWriteConnectionString="$(kagent_dsn kagent_readwrite_user)" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
fi

# --- phase 6: kagent --------------------------------------------------------

echo "==> kagent"
helm_install kagent "$NAMESPACE" \
  oci://ghcr.io/kagent-dev/kagent/helm/kagent "$KAGENT_VERSION" \
  10m "$KAGENT_VALUES"

if [ "$DRY_RUN" -eq 1 ]; then
  echo
  echo "# Not run in dry-run: the provider credential, then the pi-go Harness:"
  echo "#   ./scripts/install-kagent.sh --tag <pi-go release tag>"
  exit 0
fi

note "installed kagent ${KAGENT_VERSION} on substrate ${SUBSTRATE_VERSION}"
kubectl get workerpool -A 2>/dev/null || true
kubectl get pods -n "$NAMESPACE" 2>/dev/null || true

cat <<'NEXT'

Verify:
  # controller reached Substrate and its database (no x509 or permission errors)
  kubectl -n kagent logs deploy/kagent-controller --tail=50 \
    | grep -Ei 'substrate|permission|denied|x509|database'

  # the worker pool is chart-managed at the expected size
  kubectl get workerpool kagent-default -n kagent \
    -o jsonpath='replicas={.spec.replicas} ready={.status.readyReplicas}{"\n"}'

Next:
  ./scripts/install-kagent.sh --tag <pi-go release tag>
NEXT
