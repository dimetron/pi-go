#!/usr/bin/env python3
"""Score one pi-go session log for degenerate-turn signatures.

Reports the three metrics that distinguish the failure modes seen in the corpus:
  think_run   longest run of consecutive thinking events with no tool call
  reps/period longest byte-exact periodic tail (the shape the guard aborts on)
  intent/calls "let me write/run/test" phrases vs actual tool calls
"""
import json, sys, os, re

def load(p):
    out = []
    for l in open(p, errors="replace"):
        l = l.strip()
        if l.startswith("{"):
            try: out.append(json.loads(l))
            except Exception: pass
    return out

def repeat_period(buf, probe=48):
    if len(buf) < probe * 2: return 0
    p = buf[-probe:]
    prev = buf.rfind(p, 0, len(buf) - probe)
    return 0 if prev < 0 else len(buf) - probe - prev

def periodic_reps(buf, period):
    if period <= 0: return 0
    n = 1
    while (n + 1) * period <= len(buf):
        if buf[len(buf)-(n+1)*period: len(buf)-n*period] != buf[len(buf)-n*period:][:period]:
            break
        n += 1
    return n

def score(p):
    rows = load(p)
    start = next((r for r in rows if r.get("type") == "session_start"), {})
    run = b = best = bb = 0
    for r in rows:
        if r.get("type") == "thinking":
            run += 1; b += len(str(r.get("content", "")))
        elif r.get("type") in ("tool_call", "user", "llm_text"):
            if b > bb: best, bb = run, b
            run = b = 0
    if b > bb: best, bb = run, b

    think = "".join(str(r.get("content","")) for r in rows if r.get("type")=="thinking")
    reps = per = 0
    for i in range(2000, len(think) + 2000, 2000):
        w = think[max(0, i-8192):i]
        pp = repeat_period(w)
        if pp >= 16:
            rr = periodic_reps(w, pp)
            if rr > reps: reps, per = rr, pp

    intents = len(re.findall(r"(?i)let me (write|run|create|test|do)", think))
    calls = sum(1 for r in rows if r.get("type") == "tool_call")
    aborted = any("loop aborted" in str(r.get("content","")) for r in rows if r.get("type")=="error")

    verdict = "LOOP" if (aborted or reps >= 12 or best >= 20) else ("suspect" if best >= 8 else "ok")
    print(f"{verdict:8s} think_run={best:4d} ({bb:7d}B) reps={reps:3d}/p={per:5d} "
          f"intent={intents:3d} calls={calls:3d} aborted={aborted} model={start.get('model','?')}")
    return verdict

if __name__ == "__main__":
    for p in sys.argv[1:]:
        score(p)
