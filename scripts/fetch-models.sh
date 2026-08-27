#!/usr/bin/env bash
# fetch-models: regenerate the embedded per-provider model catalogs under
# internal/provider/modeldata/ from live provider APIs.
#
# Uses the CLI's `pi model list <provider> -o json` mode (Slice 5 of
# features/TOO/024-mistral-provider) as the fetch mechanism — no separate Go
# fetch code. Providers without an API key are skipped with a note; the target
# does not fail on a missing key.
set -euo pipefail

cd "$(dirname "$0")/.."

# Load dotenv files the same way the CLI's loadDotEnv does, so the per-provider
# skip check below sees exactly the keys the binary will see. Order matters:
# $HOME/.pi-go/.env first, then the nearest .pi-go/.env walking up from the
# working directory, which wins. That second file is the one that matters in a
# worktree - .pi-go/.env is gitignored and lives only in the primary checkout,
# so a worktree finds it by walking up rather than by having its own copy.
#
# Parsed rather than sourced: `.` would execute the file, and the CLI treats it
# as plain KEY=VALUE lines (blank lines and # comments skipped, empty values
# ignored). Matching that parser keeps the two in agreement.
load_dotenv() {
  [ -f "$1" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line#"${line%%[![:space:]]*}"}"
    case "$line" in ''|'#'*) continue ;; esac
    case "$line" in *=*) ;; *) continue ;; esac
    key="${line%%=*}"
    val="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"
    val="${val#"${val%%[![:space:]]*}"}"
    val="${val%"${val##*[![:space:]]}"}"
    [ -n "$key" ] && [ -n "$val" ] || continue
    export "$key=$val"
  done < "$1"
}

# nearest_dotenv walks up from $1 looking for .pi-go/.env, mirroring the CLI's
# findNearestDotEnv.
nearest_dotenv() {
  local dir=$1 parent
  while :; do
    if [ -f "$dir/.pi-go/.env" ]; then
      printf '%s\n' "$dir/.pi-go/.env"
      return 0
    fi
    parent=$(dirname "$dir")
    [ "$parent" = "$dir" ] && return 1
    dir=$parent
  done
}

load_dotenv "$HOME/.pi-go/.env"
if project_env=$(nearest_dotenv "$PWD"); then
  load_dotenv "$project_env"
fi

echo "Building pi..."
go build -o /tmp/pi-fetch-models ./cmd/pi

PROVIDERS="anthropic openai gemini mistral xai openrouter"
OUT_DIR="internal/provider/modeldata"
mkdir -p "$OUT_DIR"

for p in $PROVIDERS; do
  case "$p" in
    anthropic) KEY_VAR="ANTHROPIC_API_KEY" ;;
    openai)    KEY_VAR="OPENAI_API_KEY" ;;
    gemini)    KEY_VAR="GEMINI_API_KEY" ;;
    mistral)   KEY_VAR="MISTRAL_API_KEY" ;;
    xai)       KEY_VAR="XAI_API_KEY" ;;
    openrouter) KEY_VAR="OPENROUTER_API_KEY" ;;
  esac

  if [ -z "${!KEY_VAR:-}" ]; then
    echo "skip $p: $KEY_VAR not set"
    continue
  fi

  echo "fetching $p..."
  if ! /tmp/pi-fetch-models model list "$p" -o json > "$OUT_DIR/models-$p.json" 2> /tmp/pi-fetch-models-$p.err; then
    echo "  FAILED: $(cat /tmp/pi-fetch-models-$p.err)"
    rm -f "$OUT_DIR/models-$p.json"
    continue
  fi

  if command -v jq > /dev/null 2>&1; then
    # Normalize for git-friendliness: sort models by id (safety net), dedupe,
    # and pin fetched_at to the fetch date at midnight UTC so a re-fetch with
    # unchanged models produces no diff.
    jq '.fetched_at = (now | strftime("%Y-%m-%dT00:00:00Z")) |
        .models |= (sort_by(.id) | unique_by(.id))' \
      "$OUT_DIR/models-$p.json" > "$OUT_DIR/models-$p.json.tmp"
    mv "$OUT_DIR/models-$p.json.tmp" "$OUT_DIR/models-$p.json"
    if ! jq empty "$OUT_DIR/models-$p.json" 2> /dev/null; then
      echo "  WARNING: $OUT_DIR/models-$p.json is not valid JSON"
    fi
  fi
  echo "  wrote $OUT_DIR/models-$p.json"
done

rm -f /tmp/pi-fetch-models /tmp/pi-fetch-models-*.err
echo "done."
