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
    continue
  fi

  if command -v jq > /dev/null 2>&1; then
    if ! jq empty "$OUT_DIR/models-$p.json" 2> /dev/null; then
      echo "  WARNING: $OUT_DIR/models-$p.json is not valid JSON"
    fi
  fi
  echo "  wrote $OUT_DIR/models-$p.json"
done

rm -f /tmp/pi-fetch-models /tmp/pi-fetch-models-*.err
echo "done."
