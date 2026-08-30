#!/usr/bin/env bash
# sync-sessions.sh — pull pi-go sessions from a remote host without losing
# local data.
#
# rsync stages the remote sessions tree into a temp dir, then session-merge
# folds it into ~/.pi-go/sessions. The merge never deletes local-only sessions
# and never truncates a local events file: for a session present on both sides
# it unions events by ID (the longer file wins when one side is a strict
# continuation), keeps the newer meta.json, and writes everything atomically.
#
# Usage:
#   scripts/sync-sessions.sh [user@]host [remote-sessions-dir] [--dry-run]
#
# Examples:
#   scripts/sync-sessions.sh archm2pro
#   scripts/sync-sessions.sh archm2pro .pi-go/sessions --dry-run
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REMOTE="${1:?usage: sync-sessions.sh [user@]host [remote-sessions-dir] [--dry-run]}"
REMOTE_DIR="${2:-.pi-go/sessions}"
DRY_RUN="${3:-}"

LOCAL_DIR="${HOME}/.pi-go/sessions"
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/pi-sessions-sync.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT

echo "==> Staging remote sessions from ${REMOTE}:${REMOTE_DIR}"
# --rsync-path raises the remote shell's file-descriptor limit: macOS defaults
# to 256, which makes rsync fail with "Too many open files" on large trees.
# Exclude IDE/log/temp junk that lives in the sessions dir but is not session
# data.
rsync -avz --rsync-path="ulimit -n 65536; rsync" \
  --exclude '.DS_Store' \
  --exclude '*.log' \
  --exclude '.idea/' \
  --exclude '*.tmp' \
  "${REMOTE}:${REMOTE_DIR}/" "${STAGE}/"

echo "==> Merging into ${LOCAL_DIR} (local data is never deleted)"
ARGS=(--local "$LOCAL_DIR" --remote "$STAGE")
if [[ -n "$DRY_RUN" ]]; then
  ARGS+=(--dry-run)
fi
(cd "$REPO_ROOT" && go run ./hack/session-merge "${ARGS[@]}")

echo "==> Done. Local sessions: $(find "$LOCAL_DIR" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
