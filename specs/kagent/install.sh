#!/bin/sh
# Install the pi-go Harness and AgentTemplate into a kagent cluster.
#
#   curl -fsSL https://raw.githubusercontent.com/dimetron/pi-go/main/specs/kagent/install.sh | sh
#
# Applies the kagent-manifests.yaml asset from a pi-go release — already
# pinned to the digest of the image that release built, which is what
# Substrate requires. Needs curl and kubectl, and a kubectl context already
# pointing at the target cluster.
#
# Options (after `| sh -s --` when piping):
#   --tag vX.Y.Z      release to install (default: the latest release)
#   --namespace NS    namespace to install into (default: kagent)
#   --model NAME      set PI_MODEL on the Harness after applying
#   --base-url URL    set PI_BASE_URL on the Harness after applying
#   --dry-run         server-side dry run; changes nothing
#   --print           write the manifest to stdout and exit
#
#   curl -fsSL .../install.sh | sh -s -- --tag v0.0.87 --dry-run
set -eu

REPO="${PI_GO_REPO:-dimetron/pi-go}"
ASSET="kagent-manifests.yaml"

tag=""
namespace="kagent"
model=""
base_url=""
dry_run=0
print_only=0

die() { echo "install.sh: $*" >&2; exit 1; }
note() { echo "install.sh: $*" >&2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --tag)       tag="${2:?--tag needs a value}"; shift 2 ;;
    --namespace) namespace="${2:?--namespace needs a value}"; shift 2 ;;
    --model)     model="${2:?--model needs a value}"; shift 2 ;;
    --base-url)  base_url="${2:?--base-url needs a value}"; shift 2 ;;
    --dry-run)   dry_run=1; shift ;;
    --print)     print_only=1; shift ;;
    -h|--help)   sed -n '2,20p' "$0"; exit 0 ;;
    *)           die "unknown argument: $1" ;;
  esac
done

command -v curl >/dev/null 2>&1 || die "curl is required"
if [ "$print_only" -eq 0 ]; then
  command -v kubectl >/dev/null 2>&1 || die "kubectl is required"
  kubectl version >/dev/null 2>&1 ||
    die "kubectl cannot reach a cluster; check your context with 'kubectl config current-context'"
fi

# Resolve the tag. The API is unauthenticated here, so a rate-limited or
# private repo fails with a message rather than an empty tag downstream.
if [ -z "$tag" ]; then
  note "resolving the latest release of ${REPO}"
  tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$tag" ] || die "could not resolve the latest release of ${REPO}; pass --tag explicitly"
fi
note "installing ${tag}"

url="https://github.com/${REPO}/releases/download/${tag}/${ASSET}"
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT

if ! curl -fsSL "$url" -o "$manifest" 2>/dev/null; then
  die "release ${tag} has no ${ASSET}.
  Releases cut before the harness manifest was published do not carry it.
  Pick a newer --tag, or render one from a checkout:
    specs/kagent/render-manifests.sh --image <ref>@sha256:<digest>"
fi
[ -s "$manifest" ] || die "downloaded ${ASSET} is empty"

# The manifest is namespaced for kagent; retarget it when asked. Matches the
# two `namespace:` keys the template emits, nothing else.
if [ "$namespace" != "kagent" ]; then
  sed "s|^\\( *namespace: \\)kagent$|\\1${namespace}|" "$manifest" > "${manifest}.ns"
  mv "${manifest}.ns" "$manifest"
fi

if [ "$print_only" -eq 1 ]; then
  cat "$manifest"
  exit 0
fi

image="$(sed -n 's/^ *image: *//p' "$manifest" | head -n 1)"
note "image: ${image:-<none found>}"

if [ "$dry_run" -eq 1 ]; then
  kubectl apply --dry-run=server -f "$manifest"
  note "dry run only; nothing was changed"
  exit 0
fi

kubectl apply -f "$manifest"

# The published manifest carries no model endpoint on purpose — it should not
# ship one cluster's Ollama address. Set it here when asked.
if [ -n "$model" ] || [ -n "$base_url" ]; then
  env_json=""
  [ -n "$model" ] && env_json="{\"name\":\"PI_MODEL\",\"value\":\"${model}\"}"
  if [ -n "$base_url" ]; then
    [ -n "$env_json" ] && env_json="${env_json},"
    env_json="${env_json}{\"name\":\"PI_BASE_URL\",\"value\":\"${base_url}\"}"
  fi
  note "setting the Harness env"
  kubectl patch harness pi-go -n "$namespace" --type=merge \
    -p "{\"spec\":{\"env\":[${env_json}]}}"
fi

note "installed. Inspect with:"
note "  kubectl get harness,agenttemplate -n ${namespace}"
