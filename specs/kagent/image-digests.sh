#!/usr/bin/env bash
# Identify the digest-pinned pi-go-kagent image references for a release.
#
# The release workflow (.github/workflows/release.yml) builds and pushes a
# multi-arch image (linux/amd64 + linux/arm64) to GHCR on every v* tag:
#
#   ghcr.io/<owner>/pi-go-kagent:<tag>
#
# Substrate requires digest-pinned image references, so before deploying a
# release you need the immutable digests. This script prints the manifest-list
# digest plus the per-architecture digests, and a ready-to-paste YAML snippet
# to pass to render-manifests.sh --image.
#
# Usage:
#   ./image-digests.sh <tag>            # e.g. v0.0.87
#   ./image-digests.sh v0.0.87
#
# Requires: docker with buildx (docker buildx imagetools), jq.
set -euo pipefail

TAG="${1:?usage: image-digests.sh <tag> (e.g. v0.0.87)}"
IMAGE="ghcr.io/dimetron/pi-go-kagent"
REF="${IMAGE}:${TAG}"

if ! docker buildx imagetools inspect "${REF}" >/dev/null 2>&1; then
  echo "error: ${REF} is not published (or not pullable)." >&2
  echo "The release workflow pushes it on every v* tag; check the tag name." >&2
  exit 1
fi

echo "== ${REF} =="
echo

# Manifest-list digest (the multi-arch image as a whole).
LIST_DIGEST="$(docker buildx imagetools inspect "${REF}" | awk '/^Digest:/ {print $2}')"
echo "manifest list: ${IMAGE}@${LIST_DIGEST}"
echo

# Per-architecture digests from the manifest list.
RAW="$(docker buildx imagetools inspect --raw "${REF}")"
AMD64_DIGEST="$(printf '%s' "${RAW}" | jq -r '.manifests[] | select(.platform.os=="linux" and .platform.architecture=="amd64") | .digest' | head -1)"
ARM64_DIGEST="$(printf '%s' "${RAW}" | jq -r '.manifests[] | select(.platform.os=="linux" and .platform.architecture=="arm64") | .digest' | head -1)"

if [ -z "${AMD64_DIGEST}" ] || [ -z "${ARM64_DIGEST}" ]; then
  echo "error: could not find linux/amd64 and linux/arm64 in the manifest list." >&2
  exit 1
fi

echo "linux/amd64: ${IMAGE}@${AMD64_DIGEST}"
echo "linux/arm64: ${IMAGE}@${ARM64_DIGEST}"
echo

cat <<EOF
Render with (pick the arch matching the worker nodes):

  # amd64
  image: ${IMAGE}@${AMD64_DIGEST}
  # arm64
  image: ${IMAGE}@${ARM64_DIGEST}
  # multi-arch (kagent pulls the right arch automatically)
  image: ${IMAGE}@${LIST_DIGEST}
EOF
