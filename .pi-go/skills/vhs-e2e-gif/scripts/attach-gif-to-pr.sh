#!/usr/bin/env bash
# Publish one or more GIFs where a PR comment can render them, then post that
# comment. Nothing binary is committed to the repository.
#
# Usage:
#   attach-gif-to-pr.sh <pr-number> <file.gif> [file.gif ...]
#
# Environment:
#   CAPTIONS   '|'-separated captions, one per file, in order
#   TAG        release tag to create   (default: media-pr<PR>)
#   MODE       release | branch        (default: release)
#   MEDIA_DIR  in-repo path for MODE=branch  (default: docs/media)
#
# MODE=branch commits the GIFs to the PR branch. It exists only for repos where
# releases are unavailable — prefer release, which leaves git history alone.
set -euo pipefail

PR=${1:?usage: attach-gif-to-pr.sh <pr-number> <file.gif> [file.gif ...]}
shift
[ $# -gt 0 ] || { echo "attach-gif-to-pr.sh: no GIF given" >&2; exit 2; }
for f in "$@"; do
  [ -f "$f" ] || { echo "attach-gif-to-pr.sh: no such file: $f" >&2; exit 1; }
done
FILES=("$@")

MODE=${MODE:-release}
TAG=${TAG:-media-pr$PR}
MEDIA_DIR=${MEDIA_DIR:-docs/media}
CAPTIONS=${CAPTIONS:-}

command -v gh >/dev/null 2>&1 || { echo "attach-gif-to-pr.sh: gh not found" >&2; exit 1; }
REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
if [ "$(gh repo view --json visibility -q .visibility)" != "PUBLIC" ]; then
  echo "attach-gif-to-pr.sh: $REPO is not public — the URL will not render for" >&2
  echo "                     readers who are not signed in." >&2
  exit 1
fi

caption_for() { # 1-based index -> caption, falling back to the bare filename
  local i=$1 n=1 c
  local IFS='|'
  for c in $CAPTIONS; do
    [ "$n" = "$i" ] && { printf '%s' "$c"; return; }
    n=$((n + 1))
  done
  printf '%s' "$(basename "${FILES[$((i - 1))]}")"
}

declare -a URLS=()

publish_release() {
  # An immutable release is sealed on publish: assets attach at CREATE time
  # only, and `gh release upload` afterwards answers
  #   HTTP 422: Cannot upload assets to an immutable release.
  # A deleted immutable tag also stays burned ("tag_name was used by an
  # immutable release"), so a re-record needs a fresh tag rather than a reuse.
  local tag=$TAG n=2
  while gh release view "$tag" >/dev/null 2>&1; do
    tag="$TAG-$n"
    n=$((n + 1))
  done
  if ! gh release create "$tag" "$@" \
      --title "Media: PR #$PR recordings" \
      --notes "Screen recordings referenced from PR #$PR. Not a software release." \
      --latest=false >/dev/null; then
    # Most likely the tag is burned by a previously deleted immutable release.
    tag="$tag-$(date +%s)"
    echo "retrying with tag $tag"
    gh release create "$tag" "$@" \
      --title "Media: PR #$PR recordings" \
      --notes "Screen recordings referenced from PR #$PR. Not a software release." \
      --latest=false >/dev/null
  fi
  local f
  for f in "$@"; do
    URLS+=("https://github.com/$REPO/releases/download/$tag/$(basename "$f")")
  done
  echo "release: https://github.com/$REPO/releases/tag/$tag"
}

publish_branch() {
  local root branch f dest
  root=$(git rev-parse --show-toplevel)
  branch=$(git rev-parse --abbrev-ref HEAD)
  [ "$branch" != "HEAD" ] || { echo "attach-gif-to-pr.sh: detached HEAD" >&2; return 1; }
  mkdir -p "$root/$MEDIA_DIR"
  for f in "$@"; do
    dest="$root/$MEDIA_DIR/$(basename "$f")"
    cp "$f" "$dest"
    git -C "$root" add "$MEDIA_DIR/$(basename "$f")"
    URLS+=("https://raw.githubusercontent.com/$REPO/$branch/$MEDIA_DIR/$(basename "$f")")
  done
  if git -C "$root" diff --cached --quiet; then
    echo "recordings unchanged; nothing to commit"
  else
    # -s -S per this repo's signing rule; signing needs the 1Password agent,
    # which is unreachable from inside the agent sandbox.
    git -C "$root" commit -s -S -q -m "docs(media): recordings for #$PR"
  fi
  git -C "$root" push -q origin "$branch"
}

case "$MODE" in
  release) publish_release "$@" ;;
  branch)  publish_branch "$@" ;;
  *) echo "attach-gif-to-pr.sh: unknown MODE=$MODE (release|branch)" >&2; exit 2 ;;
esac

BODY="<!-- vhs-e2e-gif -->"
i=1
for url in "${URLS[@]}"; do
  cap=$(caption_for "$i")
  BODY="$BODY
### $cap

![$cap]($url)
"
  i=$((i + 1))
done
if [ "$MODE" = "release" ]; then
  BODY="$BODY
<sub>Recorded with VHS, hosted as release assets — nothing binary is committed to the repo.</sub>"
else
  BODY="$BODY
<sub>Recorded with VHS.</sub>"
fi

# --edit-last keeps one recording comment per PR instead of a pile of them.
if ! gh pr comment "$PR" --body "$BODY" --edit-last 2>/dev/null; then
  gh pr comment "$PR" --body "$BODY"
fi

printf 'asset:   %s\n' "${URLS[@]}"
