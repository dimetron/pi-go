#!/usr/bin/env bash
# A/B replay of a degenerate-turn seed session across provider paths, traces on.
#
# The seed session must end at the tool result immediately BEFORE the spiral —
# the degenerate thinking is usually never committed to events.jsonl, so
# resuming restores the exact pre-failure state and the next turn is the one
# that broke.
#
# Each trial copies the seed to a throwaway session ID (the original is never
# mutated), rewrites meta.json (id/model/provider/workDir), resumes with
# --trace-http, then scores the resulting log.
#
# Keys come from ~/.pi-go/.env and <repo>/.pi-go/.env, which pi-go merges itself.
set -uo pipefail

SEED=${SEED:-260809-1229-ecff9-4280b}
SESS=~/.pi-go/sessions
TRIALS=${TRIALS:-3}
PROMPT=${PROMPT:-continue}
PI=${PI:-pi}
OUT=${OUT:-/tmp/ab-replay}
WORKDIR=${WORKDIR:-$PWD}
ARMS=${ARMS:-"ollama-cloud ollama-local opencode"}
mkdir -p "$OUT"

[[ -d "$SESS/$SEED" ]] || { echo "seed session missing: $SESS/$SEED"; exit 1; }

model_for() { case "$1" in
  ollama-cloud) echo "deepseek-v4-flash:0731:cloud";;
  ollama-local) echo "ollama/deepseek-v4-flash:0731-cloud";;
  opencode)     echo "opencode/deepseek-v4-flash";;
  *) echo "";; esac; }
provider_for() { case "$1" in opencode) echo opencode;; *) echo ollama;; esac; }

for arm in $ARMS; do
  model=$(model_for "$arm"); [[ -n "$model" ]] || { echo "unknown arm: $arm"; continue; }
  # Preflight: a dead credential would waste every trial in the arm.
  if ! timeout 120 "$PI" --model "$model" --mode print "reply with exactly: OK" >/dev/null 2>"$OUT/$arm.preflight"; then
    echo "=== arm $arm : SKIPPED - preflight failed"
    sed -n 's/.*error: /    /p' "$OUT/$arm.preflight" | head -2
    continue
  fi
  echo "=== arm $arm : $model ==="
  for i in $(seq 1 "$TRIALS"); do
    id="replay-${arm}-${i}-$$"
    cp -R "$SESS/$SEED" "$SESS/$id"
    python3 - "$SESS/$id/meta.json" "$id" "$model" "$(provider_for "$arm")" "$WORKDIR" <<'PY'
import json,sys
p,i,m,pr,wd=sys.argv[1:6]
d=json.load(open(p)); d.update(id=i,model=m,provider=pr,workDir=wd)
json.dump(d,open(p,"w"),indent=2)
PY
    timeout 900 "$PI" --session "$id" --model "$model" --trace-http \
        --mode print "$PROMPT" >"$OUT/${arm}-${i}.stdout" 2>&1
    rc=$?
    # Find the log by its session field, NOT by mtime: other pi sessions may run
    # concurrently and would otherwise be picked up as this trial's log.
    found=$(grep -l "\"session\":\"$id\"" ~/.pi-go/log/*/*.log 2>/dev/null | head -1)
    if [[ -n "$found" ]]; then
      cp "$found" "$OUT/${arm}-${i}.log"
      printf "  trial %s rc=%-3s " "$i" "$rc"
      python3 "$(dirname "$0")/score_run.py" "$OUT/${arm}-${i}.log"
    else
      echo "  trial $i rc=$rc (no log for session $id - see ${arm}-${i}.stdout)"
    fi
    rm -rf "$SESS/$id"
  done
done
echo; echo "logs + traces in $OUT"
