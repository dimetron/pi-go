#!/usr/bin/env bash
# Render specs/kagent/manifests.yaml.tmpl into a deployable manifest.
#
# Substrate requires a digest-pinned workload image, so --image is mandatory
# and is rejected unless it carries an @sha256: digest. A tag would deploy
# whatever that tag points at today, which is exactly what pinning prevents.
#
# Usage:
#   ./render-manifests.sh --image ghcr.io/dimetron/pi-go-kagent@sha256:<digest>
#   ./render-manifests.sh --image ... --no-env > manifests.yaml
#   ./render-manifests.sh --image ... | kubectl apply -f -
#
# Writes to stdout unless --output is given. Substitution is plain sed, so no
# gettext/envsubst dependency — envsubst is absent from a default macOS.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
template="${here}/manifests.yaml.tmpl"

image=""
name="pi-go"
namespace="kagent"
worker_pool="kagent-default"
snapshot_location="gs://ate-snapshots/pi-go/"
model_config="default-model-config"
# Defaults suit the local Kind Substrate; a release renders with --no-env so
# the published manifest carries no cluster-specific endpoint.
model="ollama/deepseek-v4-flash:0731-cloud"
base_url="http://host.docker.internal:11434"
want_env=1
output=""

die() { echo "render-manifests.sh: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --image)             image="${2:?--image needs a value}"; shift 2 ;;
    --name)              name="${2:?--name needs a value}"; shift 2 ;;
    --namespace)         namespace="${2:?--namespace needs a value}"; shift 2 ;;
    --worker-pool)       worker_pool="${2:?--worker-pool needs a value}"; shift 2 ;;
    --snapshot-location) snapshot_location="${2:?--snapshot-location needs a value}"; shift 2 ;;
    --model-config)      model_config="${2:?--model-config needs a value}"; shift 2 ;;
    --model)             model="${2:?--model needs a value}"; shift 2 ;;
    --base-url)          base_url="${2:?--base-url needs a value}"; shift 2 ;;
    --no-env)            want_env=0; shift ;;
    --output)            output="${2:?--output needs a value}"; shift 2 ;;
    -h|--help)           sed -n '2,14p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *)                   die "unknown argument: $1" ;;
  esac
done

[ -n "$image" ] || die "--image is required (see --help)"
case "$image" in
  *@sha256:*) ;;
  *) die "--image must be digest-pinned (…@sha256:…); Substrate rejects a tag: ${image}" ;;
esac
[ -f "$template" ] || die "template not found: ${template}"

# The Harness env block, or nothing. Built here rather than in the template so
# --no-env leaves no empty `env:` key behind, which the API would reject.
env_block=""
if [ "$want_env" -eq 1 ]; then
  env_block="  env:
    - name: PI_MODEL
      value: ${model}
    - name: PI_BASE_URL
      value: ${base_url}
"
fi

# '|' as the sed delimiter: image references, URLs and digests all contain '/'
# and ':' but never '|'. The env block travels through the environment rather
# than `awk -v`, which cannot carry a value containing newlines.
render() {
  sed \
    -e "s|\${PI_GO_IMAGE}|${image}|g" \
    -e "s|\${PI_GO_NAME}|${name}|g" \
    -e "s|\${PI_GO_NAMESPACE}|${namespace}|g" \
    -e "s|\${PI_GO_WORKER_POOL}|${worker_pool}|g" \
    -e "s|\${PI_GO_SNAPSHOT_LOCATION}|${snapshot_location}|g" \
    -e "s|\${PI_GO_MODEL_CONFIG}|${model_config}|g" \
    "$template" \
  | PI_GO_ENV_BLOCK="$env_block" awk '
      $0 == "${PI_GO_ENV_BLOCK}---" {
        printf "%s---\n", ENVIRON["PI_GO_ENV_BLOCK"]
        next
      }
      { print }
    '
}

if [ -n "$output" ]; then
  render > "$output"
  echo "render-manifests.sh: wrote ${output}" >&2
else
  render
fi
