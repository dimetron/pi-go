#!/usr/bin/env bash
# fetch-ollama-pricing: regenerate the embedded Ollama Cloud pricing snapshot
# under internal/provider/modeldata/ollama-cloud-pricing.json from
# https://ollama.com/pricing.
#
# Ollama does not publish per-token rates through an API: models.dev carries
# the ollama-cloud catalog with IDs and release dates but no cost fields, and
# api.ollama.com/v1/models returns IDs only. The per-million-token USD rates
# live in the two tables on the pricing page, so this script scrapes them.
#
# The snapshot has two sections:
#   - "models": the standard rates (the table headlined "Model pricing").
#   - "peak": rates that apply 12:00-18:00 UTC Monday to Friday.
#
# The API is OpenAI-compatible at https://api.ollama.com, so pi-go stores the
# prices under provider name "ollama-cloud" and serves them from CostFor when
# a cloud-tagged model runs there. Local models are free and stay unpriced.
set -euo pipefail

cd "$(dirname "$0")/.."

URL="https://ollama.com/pricing"
OUT="internal/provider/modeldata/ollama-cloud-pricing.json"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

echo "fetching $URL..."
if ! curl -fsSL --max-time 60 -H "User-Agent: Mozilla/5.0" "$URL" -o "$WORK/pricing.html"; then
  echo "FAILED: could not fetch $URL (keeping existing $OUT)" >&2
  exit 1
fi

if ! command -v python3 > /dev/null 2>&1; then
  echo "FAILED: python3 is required to parse the pricing page (keeping existing $OUT)" >&2
  exit 1
fi

# Parse the HTML tables into the snapshot shape, and pin fetched_at to the
# fetch date at midnight UTC so a re-fetch with unchanged data produces no
# diff. The first table is the normal rates, the second the peak rates.
python3 - "$WORK/pricing.html" "$WORK/snapshot.json" <<'PY'
import html, json, re, sys, datetime

src, dst = sys.argv[1], sys.argv[2]
raw = open(src).read()

def rate(cell):
    cell = cell.strip()
    if cell in ("", "-"):
        return None
    if not cell.startswith("$"):
        raise ValueError(f"unexpected rate cell {cell!r}")
    return float(cell[1:])

def parse_table(tbl):
    rows = re.findall(r"<tr>(.*?)</tr>", tbl, re.S)
    out = {}
    for r in rows:
        cells = re.findall(r"<td[^>]*>(.*?)</td>", r, re.S)
        if len(cells) != 4:
            continue
        name = html.unescape(re.sub(r"<[^>]+>", "", cells[0])).strip()
        if not name:
            continue
        out[name] = {
            "input": rate(html.unescape(re.sub(r"<[^>]+>", "", cells[1])).strip()),
            "cache_read": rate(html.unescape(re.sub(r"<[^>]+>", "", cells[2])).strip()),
            "output": rate(html.unescape(re.sub(r"<[^>]+>", "", cells[3])).strip()),
        }
    return out

tables = re.findall(r"<table.*?</table>", raw, re.S)
if len(tables) < 2:
    sys.exit("FAILED: expected the pricing page to carry a normal and a peak table")

models = parse_table(tables[0])
peak = parse_table(tables[1])
if not models:
    sys.exit("FAILED: no priced models found on the pricing page")
# The peak table only lists a subset; an empty one means the page changed.
if not peak:
    sys.exit("FAILED: peak pricing table parsed to nothing — page layout changed")

out = {
    "source": "ollama.com/pricing",
    "fetched_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT00:00:00Z"),
    "models": models,
    "peak": peak,
}

with open(dst, "w") as f:
    json.dump(out, f, indent=1, sort_keys=True)
    f.write("\n")
PY

# Validate the output parses and has the expected shape before replacing.
if ! python3 -c "
import json, sys
d = json.load(open('$WORK/snapshot.json'))
assert d['source'] == 'ollama.com/pricing'
assert d['models'] and d['peak'], 'both sections must be non-empty'
for m, r in d['models'].items():
    assert r['input'] is not None and r['output'] is not None, m
" 2>/dev/null; then
  echo "FAILED: snapshot is not valid (keeping existing $OUT)" >&2
  exit 1
fi

mv "$WORK/snapshot.json" "$OUT"
echo "wrote $OUT ($(wc -c < "$OUT") bytes, $(python3 -c "import json; print(len(json.load(open('$OUT'))['models']))") models)"