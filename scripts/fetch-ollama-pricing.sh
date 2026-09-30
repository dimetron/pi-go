#!/usr/bin/env bash
# fetch-ollama-pricing: regenerate the embedded Ollama Cloud pricing snapshot
# under internal/provider/modeldata/ollama-cloud-pricing.json from
# https://ollama.com/pricing.
#
# Ollama does not publish per-token rates through an API: models.dev carries
# the ollama-cloud catalog with IDs and release dates but no cost fields, and
# api.ollama.com/v1/models returns IDs only. The per-million-token USD rates
# live in the single "Model pricing" table on the pricing page, so this script
# scrapes it.
#
# The page marks the discount, not the surcharge: the plain rows are the rates
# that apply 12:00-18:00 UTC on weekdays, and a row suffixed "(Off-Peak)" is
# the discounted rate for outside that window and all weekend — "Off-peak
# pricing apply outside 12:00 and 18:00 UTC on weekdays and all day on
# weekends." The snapshot keeps the same two sections as before:
#   - "models": the off-peak rates. A model the page gives no separate
#     off-peak row bills at one rate all day, so it uses its plain rate.
#   - "peak": the 12:00-18:00 UTC weekday rates (the plain rows).
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

# Parse the single "Model pricing" table into the snapshot shape, and pin
# fetched_at to the fetch date at midnight UTC so a re-fetch with unchanged
# data produces no diff. The page labels the discounted row, so the plain rows
# become "peak" and the "(Off-Peak)" rows become "models".
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

def cell_text(cell):
    return html.unescape(re.sub(r"<[^>]+>", "", cell)).strip()

def parse_table(tbl):
    out = {}
    for r in re.findall(r"<tr>(.*?)</tr>", tbl, re.S):
        cells = re.findall(r"<td[^>]*>(.*?)</td>", r, re.S)
        if len(cells) != 4:
            continue
        name = cell_text(cells[0])
        if not name:
            continue
        out[name] = {
            "input": rate(cell_text(cells[1])),
            "cache_read": rate(cell_text(cells[2])),
            "output": rate(cell_text(cells[3])),
        }
    return out

tables = re.findall(r"<table.*?</table>", raw, re.S)
if not tables:
    sys.exit("FAILED: no table found on the pricing page — layout changed")

rows = parse_table(tables[0])
if not rows:
    sys.exit("FAILED: no priced models found on the pricing page")

# The page marks the discount, not the surcharge: plain rows are the weekday
# peak (12:00-18:00 UTC) rates, "(Off-Peak)" rows the discounted ones.
off_peak_re = re.compile(r"\s*\(Off-Peak\)\s*$", re.I)
plain = {}
off_peak = {}
for name, rates in rows.items():
    if off_peak_re.search(name):
        off_peak[off_peak_re.sub("", name)] = rates
    else:
        plain[name] = rates

if not plain:
    sys.exit("FAILED: no plain-priced rows on the pricing page — layout changed")

# An off-peak row only discounts a model that also has a plain row; one without
# means the labels were restructured and the mapping below is no longer sound.
orphans = sorted(set(off_peak) - set(plain))
if orphans:
    sys.exit("FAILED: off-peak rows with no plain rate: " + ", ".join(orphans))

# A model has a distinct peak rate exactly when the page discounts it. "models"
# is the off-peak rate where there is one, otherwise the plain rate, so a model
# billed the same rate all day still prices correctly.
models = {name: off_peak.get(name, rates) for name, rates in plain.items()}
peak = {name: plain[name] for name in off_peak}

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