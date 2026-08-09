#!/usr/bin/env python3
"""
Replays on-disk session history to characterise the abort class
(last event TurnComplete=false, ErrorMessage="", Interrupted=false).

Used to produce the numbers in 09-aborted-sessions.md.

Usage:
    python3 09-replay.py                # corpus scan
    python3 09-replay.py <session-id>   # per-session detail

Dependencies: stdlib only. Reads $HOME/.pi-go/sessions/*/{events.jsonl,meta.json}.
"""

from __future__ import annotations

import json
import os
import statistics
import sys
from collections import Counter
from datetime import datetime
from pathlib import Path

SESSIONS_DIR = Path(os.environ.get("HOME", str(Path.home())) + "/.pi-go/sessions")


def is_abort(last: dict) -> bool:
    return (
        last.get("TurnComplete") is False
        and not (last.get("ErrorMessage") or "")
        and not last.get("Interrupted")
    )


def per_session_prompt(events_path: Path) -> tuple[int, int]:
    """Return (prompt_token_total, turn_count) for a session."""
    total = 0
    turns = 0
    with open(events_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            um = ev.get("UsageMetadata") or {}
            ptc = um.get("promptTokenCount") or 0
            if ptc:
                total += ptc
                turns += 1
    return total, turns


def corpus_scan() -> dict:
    dirs = [d for d in SESSIONS_DIR.iterdir() if d.is_dir() and d.name not in ("archive", ".idea")]
    out = {
        "total_dirs": len(dirs),
        "empty_events": 0,
        "complete": 0,
        "abort": 0,
        "other": 0,
        "abort_prompt_total": 0,
        "abort_turn_counts": [],
        "abort_models": Counter(),
        "abort_providers": Counter(),
        "abort_sessions": [],
    }
    for d in dirs:
        meta_p = d / "meta.json"
        events_p = d / "events.jsonl"
        if not meta_p.exists() or not events_p.exists():
            continue
        try:
            meta = json.loads(meta_p.read_text())
        except json.JSONDecodeError:
            meta = {}
        with open(events_p) as f:
            lines = f.readlines()
        if not lines:
            out["empty_events"] += 1
            continue
        try:
            last = json.loads(lines[-1])
        except json.JSONDecodeError:
            out["other"] += 1
            continue
        tc = last.get("TurnComplete")
        interr = last.get("Interrupted")
        err = last.get("ErrorMessage") or ""
        if tc is True:
            out["complete"] += 1
        elif is_abort(last):
            out["abort"] += 1
            p, t = per_session_prompt(events_p)
            out["abort_prompt_total"] += p
            out["abort_turn_counts"].append(t)
            out["abort_models"][meta.get("model") or "<missing>"] += 1
            out["abort_providers"][meta.get("provider") or "<missing>"] += 1
            out["abort_sessions"].append(d.name)
        else:
            out["other"] += 1
    out["abort_turn_counts"].sort()
    return out


def per_session_detail(sid: str) -> dict:
    d = SESSIONS_DIR / sid
    meta = json.loads((d / "meta.json").read_text())
    with open(d / "events.jsonl") as f:
        events = [json.loads(l) for l in f]
    rows = []
    prev_prompt = 0
    prev_ts = None
    for i, e in enumerate(events):
        ts = e.get("Timestamp")
        try:
            dt = datetime.fromisoformat(ts)
        except (TypeError, ValueError):
            dt = None
        gap = (dt - prev_ts).total_seconds() if prev_ts and dt else 0
        content = e.get("Content") or {}
        parts = (content or {}).get("parts", []) or []
        fcs = [p for p in parts if "functionCall" in p]
        frs = [p for p in parts if "functionResponse" in p]
        usage = e.get("UsageMetadata") or {}
        prompt = usage.get("promptTokenCount") or 0
        cand = usage.get("candidatesTokenCount") or 0
        delta = prompt - prev_prompt if prev_prompt else 0
        if prompt:
            prev_prompt = prompt
        rows.append({
            "n": i + 1,
            "ts": ts,
            "gap_s": round(gap, 3),
            "author": e.get("Author"),
            "turn_complete": e.get("TurnComplete"),
            "interrupted": e.get("Interrupted"),
            "err": e.get("ErrorMessage") or "",
            "fcs": [fc["functionCall"].get("name") for fc in fcs],
            "frs": [fr["functionResponse"].get("name") for fr in frs],
            "prompt": prompt,
            "candidates": cand,
            "delta": delta,
        })
        prev_ts = dt
    return {"meta": meta, "events": rows}


def main(argv: list[str]) -> int:
    if len(argv) > 1:
        sid = argv[1]
        detail = per_session_detail(sid)
        print(f"Session: {sid}")
        print(f"  model:    {detail['meta'].get('model')}")
        print(f"  provider: {detail['meta'].get('provider')}")
        print(f"  createdAt: {detail['meta'].get('createdAt')}")
        print(f"  updatedAt: {detail['meta'].get('updatedAt')}")
        print()
        print(f"  {'#':>2s}  {'gap_s':>6s}  {'author':>6s}  {'tc':>5s}  {'fcs':<24s}  {'frs':<24s}  {'prompt':>6s}  {'cand':>5s}  {'delta':>6s}")
        for r in detail["events"]:
            fcs = ",".join(r["fcs"]) or "-"
            frs = ",".join(r["frs"]) or "-"
            print(
                f"  {r['n']:2d}  {r['gap_s']:6.3f}  {r['author']!s:>6s}  {r['turn_complete']!s:>5s}  "
                f"{fcs:<24s}  {frs:<24s}  {r['prompt']:6d}  {r['candidates']:5d}  {r['delta']:+6d}"
            )
        last = detail["events"][-1]
        print()
        print(f"  last event: TurnComplete={last['turn_complete']} Interrupted={last['interrupted']} ErrorMessage={last['err']!r}")
        if len(detail["events"]) >= 2:
            penult = detail["events"][-2]
            print(f"  penultimate -> last gap: {last['gap_s']:.3f}s")
        return 0
    out = corpus_scan()
    print(f"Total session dirs:        {out['total_dirs']}")
    print(f"Empty events.jsonl:        {out['empty_events']}")
    print(f"Complete (last TC=true):   {out['complete']}")
    print(f"Abort class:               {out['abort']}")
    print(f"Other (err/interrupt/etc): {out['other']}")
    print()
    print(f"Abort prompt-token total:  {out['abort_prompt_total']:,}")
    tc = out["abort_turn_counts"]
    if tc:
        print(f"  turn-count median: {statistics.median(tc):.1f}")
        print(f"  turn-count p90:    {tc[int(len(tc)*0.9)]}")
        print(f"  turn-count mean:   {statistics.mean(tc):.2f}")
        print(f"  turn-count max:    {max(tc)}")
    print()
    print("Top 5 abort models:")
    for m, c in out["abort_models"].most_common(5):
        print(f"  {m}: {c}")
    print()
    print("Top 5 abort providers:")
    for p, c in out["abort_providers"].most_common(5):
        print(f"  {p}: {c}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
