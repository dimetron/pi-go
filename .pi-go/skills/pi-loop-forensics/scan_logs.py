#!/usr/bin/env python3
"""Scan all pi-go session logs for loop / degenerate-turn signatures."""
import json, os, glob, re
from collections import Counter, defaultdict

ROOT = os.path.expanduser("~/.pi-go/log")
files = sorted(glob.glob(os.path.join(ROOT, "*", "*.log")))

def load(p):
    rows = []
    with open(p, errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except Exception:
                pass
    return rows

def repeat_period(buf, probe=48):
    if len(buf) < probe * 2:
        return 0
    p = buf[-probe:]
    prev = buf.rfind(p, 0, len(buf) - probe)
    if prev < 0:
        return 0
    return len(buf) - probe - prev

def max_periodic_repeats(buf, period):
    """How many back-to-back copies of the tail period exist."""
    if period <= 0:
        return 0
    n = 1
    while (n + 1) * period <= len(buf):
        a = buf[len(buf) - (n + 1) * period: len(buf) - n * period]
        b = buf[len(buf) - n * period:]
        if a != b[:period]:
            break
        n += 1
    return n

sessions = []
for p in files:
    rows = load(p)
    if not rows:
        continue
    start = next((r for r in rows if r.get("type") == "session_start"), {})
    model = start.get("model", "?")
    types = Counter(r.get("type") for r in rows)
    aborted = [r for r in rows if r.get("type") == "error"
               and "loop aborted" in str(r.get("content", ""))]

    # longest run of consecutive thinking events with no tool_call between
    run = 0
    best_run = 0
    best_bytes = 0
    cur_bytes = 0
    runs = []
    for r in rows:
        t = r.get("type")
        if t == "thinking":
            run += 1
            cur_bytes += len(str(r.get("content", "")))
        elif t in ("tool_call", "user", "llm_text"):
            if run:
                runs.append((run, cur_bytes))
            run = 0
            cur_bytes = 0
    if run:
        runs.append((run, cur_bytes))
    if runs:
        best_run, best_bytes = max(runs, key=lambda x: x[1])

    # near-miss repetition: scan concatenated thinking for periodic tails
    think = "".join(str(r.get("content", "")) for r in rows if r.get("type") == "thinking")
    near = 0
    near_period = 0
    # sample the stream in windows to find the worst periodicity
    step = 2000
    for i in range(step, len(think) + step, step):
        w = think[max(0, i - 8192): i]
        per = repeat_period(w)
        if per >= 16:
            reps = max_periodic_repeats(w, per)
            if reps > near:
                near, near_period = reps, per

    # identical consecutive tool calls
    sig = None
    streak = 0
    max_streak = 0
    max_streak_name = ""
    for r in rows:
        if r.get("type") != "tool_call":
            continue
        s = json.dumps(r.get("content"), sort_keys=True) if not isinstance(r.get("content"), str) else r.get("content")
        s = f"{r.get('tool','')}|{s}"
        if s == sig:
            streak += 1
        else:
            sig, streak = s, 1
        if streak > max_streak:
            max_streak, max_streak_name = streak, r.get("tool", "?")

    sessions.append(dict(
        path=p, model=model, rows=len(rows), types=types,
        aborted=[str(a.get("content")) for a in aborted],
        max_think_run=best_run, max_think_bytes=best_bytes,
        near_repeats=near, near_period=near_period,
        max_tool_streak=max_streak, max_tool_name=max_streak_name,
    ))

print(f"scanned {len(sessions)} sessions across {len(set(os.path.dirname(s['path']) for s in sessions))} days\n")

by_model = defaultdict(lambda: dict(n=0, aborts=0, think_runs=[], near=[]))
for s in sessions:
    m = by_model[s["model"]]
    m["n"] += 1
    m["aborts"] += len(s["aborted"])
    m["think_runs"].append(s["max_think_run"])
    m["near"].append(s["near_repeats"])

print("=== per model ===")
for model, m in sorted(by_model.items(), key=lambda x: -x[1]["n"]):
    tr = sorted(m["think_runs"])
    p95 = tr[int(len(tr) * .95)] if tr else 0
    print(f"{model:38s} sessions={m['n']:4d} aborts={m['aborts']:3d} "
          f"max_think_run(max/p95)={max(tr or [0]):4d}/{p95:4d} "
          f"max_near_repeats={max(m['near'] or [0]):3d}")

print("\n=== sessions that aborted ===")
for s in sessions:
    if s["aborted"]:
        print(f"{os.path.basename(os.path.dirname(s['path']))}/{os.path.basename(s['path']):24s} "
              f"{s['model']:34s} {s['aborted']}")

print("\n=== top 20 by longest tool-free thinking run ===")
for s in sorted(sessions, key=lambda x: -x["max_think_run"])[:20]:
    print(f"{s['max_think_run']:4d} events {s['max_think_bytes']:7d}B  near_reps={s['near_repeats']:3d}(p={s['near_period']:4d}) "
          f"toolstreak={s['max_tool_streak']:2d}  {s['model']:34s} "
          f"{os.path.basename(os.path.dirname(s['path']))}/{os.path.basename(s['path'])}")

print("\n=== top 15 by near-miss repetition (never aborted) ===")
for s in sorted([x for x in sessions if not x["aborted"]], key=lambda x: -x["near_repeats"])[:15]:
    print(f"reps={s['near_repeats']:3d} period={s['near_period']:5d}  think_run={s['max_think_run']:4d}  "
          f"{s['model']:34s} {os.path.basename(os.path.dirname(s['path']))}/{os.path.basename(s['path'])}")
