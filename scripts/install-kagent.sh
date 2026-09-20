#!/bin/sh
# Install the pi-go Harness and AgentTemplate into a kagent cluster.
#
#   curl -fsSL https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install-kagent.sh | sh
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
#   curl -fsSL .../install.sh | sh -s -- --tag v0.1.6 --dry-run
set -eu

REPO="${PI_GO_REPO:-dimetron/pi-go}"
ASSET="kagent-manifests.yaml"
HARNESS="pi-go"

tag=""
namespace="kagent"
model=""
base_url=""
dry_run=0
print_only=0

die() { echo "install-kagent.sh: $*" >&2; exit 1; }
note() { echo "install-kagent.sh: $*" >&2; }

# The usage text lives here rather than being read back out of the file. The
# documented invocation pipes the script into `sh`, where $0 is `sh` and the
# script never reaches disk, so `sed -n '2,20p' "$0"` reads the wrong file and
# fails with "sed: sh: No such file or directory".
usage() {
  cat <<'USAGE'
Install the pi-go Harness and AgentTemplate into a kagent cluster.

  curl -fsSL https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install-kagent.sh | sh

Applies the kagent-manifests.yaml asset from a pi-go release, already pinned to
the digest of the image that release built (Substrate requires one). Needs curl
and kubectl, and a kubectl context pointing at the target cluster.

Options (after `| sh -s --` when piping):

  --tag vX.Y.Z      release to install (default: the latest release)
  --namespace NS    namespace to install into (default: kagent)
  --model NAME      set PI_MODEL on the Harness after applying
  --base-url URL    set PI_BASE_URL on the Harness after applying
  --dry-run         server-side dry run; changes nothing
  --print           write the manifest to stdout and exit
  -h, --help        show this help

  curl -fsSL .../install-kagent.sh | sh -s -- --tag v0.1.6 --dry-run
USAGE
}

# Escape a value for a JSON string. The model name and base URL are
# interpolated into the patch below, so an unescaped quote or backslash makes
# it invalid JSON — rejected by the API after the manifest has already applied.
json_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

# Print the index of an env var in the saved spec.env, or nothing when absent.
env_index() {
  printf '%s\n' "$saved_names" | awk -v want="$1" '$0 == want { print n; exit } { n++ }'
}

# Append to $ops an op that upserts one variable into spec.env, preserving
# every other entry.
env_upsert() { # name value
  idx="$(env_index "$1")"
  if [ -n "$idx" ]; then
    ops="${ops}${ops:+,}{\"op\":\"replace\",\"path\":\"/spec/env/${idx}/value\",\"value\":\"$(json_escape "$2")\"}"
  elif [ -n "$env_present" ]; then
    ops="${ops}${ops:+,}{\"op\":\"add\",\"path\":\"/spec/env/-\",\"value\":{\"name\":\"$(json_escape "$1")\",\"value\":\"$(json_escape "$2")\"}}"
  else
    # Appending to an array that does not exist is rejected, so the first
    # variable has to bring spec.env into existence.
    ops="${ops}${ops:+,}{\"op\":\"add\",\"path\":\"/spec/env\",\"value\":[{\"name\":\"$(json_escape "$1")\",\"value\":\"$(json_escape "$2")\"}]}"
    env_present=1
  fi
}

# Build the ops that restore spec.env after the apply and upsert PI_MODEL /
# PI_BASE_URL into it.
#
# Two separate traps make this more than a one-line patch:
#
#   * `kubectl apply` is a three-way merge, and the release manifest is rendered
#     with --no-env so it carries no `env` key at all. When a previous apply
#     recorded one in last-applied-configuration, the apply deletes the live
#     list — so an installer re-run silently drops every variable a cluster
#     operator added, including `credentialRef` entries, which have no literal
#     value to reconstruct.
#   * `env` is a list-map keyed by name, but a merge patch replaces the whole
#     list rather than merging it by key, and the CRD accepts neither a
#     strategic merge nor a server-side apply alongside the client-side apply
#     that installed the Harness. So the merge patch that names only the
#     requested pair would also drop the rest.
#
# Hence: capture the list before the apply, put it back afterwards, then update
# the two variables in place. Indices come from the pre-apply list, which is
# what the restore re-creates.
harness_env_ops() {
  ops=""
  env_present=""
  if [ -n "$saved_env" ] && [ "$saved_env" != "[]" ]; then
    ops="{\"op\":\"add\",\"path\":\"/spec/env\",\"value\":${saved_env}}"
    env_present=1
  fi
  [ -n "$model" ] && env_upsert PI_MODEL "$model"
  [ -n "$base_url" ] && env_upsert PI_BASE_URL "$base_url"
  printf '%s' "$ops"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --tag)       tag="${2:?--tag needs a value}"; shift 2 ;;
    --namespace) namespace="${2:?--namespace needs a value}"; shift 2 ;;
    --model)     model="${2:?--model needs a value}"; shift 2 ;;
    --base-url)  base_url="${2:?--base-url needs a value}"; shift 2 ;;
    --dry-run)   dry_run=1; shift ;;
    --print)     print_only=1; shift ;;
    -h|--help)   usage; exit 0 ;;
    *)           die "unknown argument: $1" ;;
  esac
done

# A namespace has to be a DNS-1123 label anyway, and validating it here is also
# what keeps it safe to interpolate into the sed pattern below: a value
# containing '|' or '&' would otherwise be read as sed syntax rather than as a
# namespace.
#
# The check is a grep rather than a `case ... *[!a-z0-9-]*`, because bracket
# negation is a bash/dash extension: macOS's /bin/sh (bash in POSIX mode) reads
# `[!...]` as a literal character class, so `case` accepts "Bad" and "bad|ns"
# while the same pattern rejects them under dash. grep agrees across all three.
validate_namespace() {
  [ -n "$1" ] || return 1
  [ "${#1}" -le 63 ] || return 1
  case "$1" in -* | *-) return 1 ;; esac
  ! printf '%s' "$1" | LC_ALL=C grep -q '[^a-z0-9-]'
}
validate_namespace "$namespace" ||
  die "--namespace must be a DNS-1123 label (lowercase alphanumerics and '-', at most 63 characters, starting and ending alphanumeric): ${namespace}"

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
  sed "s|^\( *namespace: \)kagent$|\1${namespace}|" "$manifest" > "${manifest}.ns"
  mv "${manifest}.ns" "$manifest"
fi

if [ "$print_only" -eq 1 ]; then
  cat "$manifest"
  exit 0
fi

image="$(sed -n 's/^ *image: *//p' "$manifest" | head -n 1)"
note "image: ${image:-<none found>}"

# Capture spec.env before applying, because the apply itself can delete it (see
# harness_env_ops). Only needed when this run will touch the env afterwards.
# The JSON is captured whole so `credentialRef` entries survive verbatim; the
# names come from jsonpath, which does not depend on the JSON being reformatted.
saved_env=""
saved_names=""
if [ -n "$model" ] || [ -n "$base_url" ]; then
  saved_env="$(kubectl get harness "$HARNESS" -n "$namespace" \
    -o jsonpath='{.spec.env}' 2>/dev/null || true)"
  case "$saved_env" in
    ""|"[]") saved_env=""; saved_names="" ;;
    *)
      saved_names="$(kubectl get harness "$HARNESS" -n "$namespace" \
        -o jsonpath='{range .spec.env[*]}{.name}{"\n"}{end}' 2>/dev/null || true)"
      ;;
  esac
fi

if [ "$dry_run" -eq 1 ]; then
  kubectl apply --dry-run=server -f "$manifest"
  note "dry run only; nothing was changed"
  exit 0
fi

kubectl apply -f "$manifest"

# The published manifest carries no model endpoint on purpose — it should not
# ship one cluster's Ollama address. Set it here when asked.
if [ -n "$model" ] || [ -n "$base_url" ]; then
  note "setting the Harness env"
  kubectl patch harness "$HARNESS" -n "$namespace" --type=json \
    -p "[$(harness_env_ops)]"
fi

note "installed. Inspect with:"
note "  kubectl get harness,agenttemplate -n ${namespace}"
