#!/usr/bin/env bash
# fetch-modelsdev-pricing: regenerate the embedded models.dev pricing snapshot
# under internal/provider/modeldata/modelsdev-pricing.json from
# https://models.dev/api.json.
#
# The full models.dev API is ~4.4 MiB across 200+ providers; the embedded file
# keeps only the providers pi-go supports and only the rate fields cost
# estimation needs, so it stays ~60 KiB. The snapshot carries a fetched_at
# timestamp; the runtime refreshes it from the same endpoint when it is more
# than a day old.
set -euo pipefail

cd "$(dirname "$0")/.."

URL="https://models.dev/api.json"
OUT="internal/provider/modeldata/modelsdev-pricing.json"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

echo "fetching $URL..."
if ! curl -fsSL --max-time 60 "$URL" -o "$WORK/api.json"; then
  echo "FAILED: could not fetch $URL (keeping existing $OUT)" >&2
  exit 1
fi

if ! command -v python3 > /dev/null 2>&1; then
  echo "FAILED: python3 is required to compact the snapshot (keeping existing $OUT)" >&2
  exit 1
fi

# Compact the API to the supported providers and rate fields, and pin fetched_at
# to the fetch date at midnight UTC so a re-fetch with unchanged data produces
# no diff.
python3 - "$WORK/api.json" "$WORK/compact.json" <<'PY'
import json, sys, datetime

src, dst = sys.argv[1], sys.argv[2]
api = json.load(open(src))

# pi-go provider name -> models.dev source id.
provs = {
    "openai": "openai",
    "anthropic": "anthropic",
    "gemini": "google",
    "mistral": "mistral",
    "xai": "xai",
    "azure": "azure",
    "openrouter": "openrouter",
}

out = {
    "source": "models.dev",
    "fetched_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT00:00:00Z"),
    "providers": {},
}
for pigo, srcid in provs.items():
    if srcid not in api:
        continue
    models = {}
    for mid, m in api[srcid].get("models", {}).items():
        cost = m.get("cost")
        if not cost:
            continue
        entry = {}
        for k in ("input", "output", "cache_read", "cache_write"):
            if k in cost and cost[k] is not None:
                entry[k] = cost[k]
        tiers = []
        for t in cost.get("tiers", []):
            if t.get("tier", {}).get("type") != "context":
                continue
            tr = {"context_over": t["tier"]["size"]}
            for k in ("input", "output", "cache_read", "cache_write"):
                if k in t and t[k] is not None:
                    tr[k] = t[k]
            tiers.append(tr)
        if tiers:
            entry["tiers"] = tiers
        if entry:
            models[mid] = entry
    if models:
        out["providers"][pigo] = models

if not out["providers"]:
    sys.exit("FAILED: no supported priced models in API")

with open(dst, "w") as f:
    json.dump(out, f, indent=1)
    f.write("\n")
PY

# Validate the compacted output parses and has the expected shape.
if ! python3 -c "import json,sys; d=json.load(open('$WORK/compact.json')); assert d['source']=='models.dev'; assert d['providers']" 2>/dev/null; then
  echo "FAILED: compacted snapshot is not valid (keeping existing $OUT)" >&2
  exit 1
fi

mv "$WORK/compact.json" "$OUT"
echo "wrote $OUT ($(wc -c < "$OUT") bytes)"
