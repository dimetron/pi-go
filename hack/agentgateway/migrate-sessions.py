#!/usr/bin/env python3
"""Import pi-go session history into agentgateway's request_logs table.

pi-go writes one directory per session under ~/.pi-go/sessions/. Sessions that
ran before pi-go pointed at the gateway never reached agentgateway, so its
analytics start the day the gateway did. This replays those sessions into the
request log so the dashboard covers the whole history.

Each assistant turn in events.jsonl carries a usageMetadata block:

    {"usageMetadata": {"promptTokenCount": 17252, "candidatesTokenCount": 39},
     "timestamp": "...", "id": "4002169e-...", "content": {...}}

which maps onto one request_logs row. Cost is *reconstructed* from today's
catalog rates, not recovered — rates change, so treat it as an estimate. Every
imported row carries attributes_json.agw.backfill = "pi-go-sessions" so it can
be told apart from live traffic, and deleted again:

    DELETE FROM request_logs WHERE attributes_json->>'agw.backfill' = 'pi-go-sessions';

Usage:
    ./migrate-sessions.py                      # dry run: report, write nothing
    ./migrate-sessions.py --since 2026-08-01   # limit by session start date
    ./migrate-sessions.py --out load.sql       # emit SQL
    ./migrate-sessions.py --out load.sql --apply   # emit and load via docker compose
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from collections import Counter
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
SESSIONS = Path(os.environ.get("PI_GO_SESSIONS", Path.home() / ".pi-go" / "sessions"))
CATALOGS = [HERE / "base-costs.json", HERE / "pi-aliases.json"]
MARKER = "pi-go-sessions"

# agentgateway's cost catalog keys Google and AWS under their cloud names, while
# pi-go calls them "gemini" and "bedrock". Look costs up under the catalog's
# spelling or every Gemini row silently prices at zero.
CATALOG_PROVIDER = {
    "gemini": "gcp.gemini",
    "vertex": "gcp.vertex_ai",
    "bedrock": "aws.bedrock",
}

# Sessions whose provider is already the gateway are in request_logs already;
# importing them would double-count.
SKIP_PROVIDERS = {"agentgateway"}

# A turn that takes longer than this is a stalled session, not a slow model.
MAX_DURATION_MS = 10 * 60 * 1000


# --- model / provider normalisation ---------------------------------------

# pi-go has spelled the same Ollama Cloud model several ways over time. The
# catalog matches ids exactly, so collapse them or the cost lookup misses and
# the dashboard splits one model into several rows.
MODEL_ALIASES = {
    "deepseek-v4-flash:0731:cloud": "deepseek-v4-flash:0731-cloud",
    "deepseek-v4-pro:0813:cloud": "deepseek-v4-pro:0813-cloud",
}

_PREFIX_PROVIDER = (
    ("claude", "anthropic"),
    ("gpt-oss", "ollama"),  # before the gpt- rule: gpt-oss is an Ollama model
    ("gpt", "openai"),
    ("o1", "openai"),
    ("o3", "openai"),
    ("o4", "openai"),
    ("gemini", "gemini"),
    ("gemma", "ollama"),
    ("mistral", "mistral"),
    ("magistral", "mistral"),
    ("ministral", "mistral"),
    ("codestral", "mistral"),
    ("grok", "xai"),
    ("glm", "ollama"),
    ("deepseek", "ollama"),
    ("minimax", "ollama"),
    ("qwen", "ollama"),
    ("kimi", "ollama"),
    ("nemotron", "ollama"),
    ("zai-", "mistral"),
)


def normalise_model(model: str) -> str:
    model = (model or "").strip()
    return MODEL_ALIASES.get(model, model)


def infer_provider(model: str, declared: str | None) -> str:
    """Trust meta.json when it names a provider; otherwise read it off the model.

    4,285 of ~4,980 sessions predate the provider field, so inference is the
    common path, not the fallback.
    """
    if declared and declared not in ("", "?"):
        return declared
    m = (model or "").lower()
    if not m:
        return "unknown"
    # An OpenRouter id is the only one with a vendor prefix: "vendor/model".
    if "/" in m:
        return "openrouter"
    if m.endswith(":cloud") or m.endswith("-cloud") or ":cloud" in m or "-cloud" in m:
        return "ollama"
    for prefix, provider in _PREFIX_PROVIDER:
        if m.startswith(prefix):
            return provider
    return "unknown"


# --- cost ------------------------------------------------------------------


def load_catalog() -> dict[tuple[str, str], dict]:
    """Merge the catalog files the gateway itself reads, later file winning."""
    rates: dict[tuple[str, str], dict] = {}
    for path in CATALOGS:
        if not path.exists():
            continue
        doc = json.loads(path.read_text())
        for provider, pdata in doc.get("providers", {}).items():
            for model, mdata in pdata.get("models", {}).items():
                r = mdata.get("rates") or {}
                if r:
                    rates[(provider, model)] = r
    return rates


def cost_for(rates, provider: str, model: str, tin: int, tout: int):
    """Return (cost, matched) — matched is False when the model has no rates."""
    provider = CATALOG_PROVIDER.get(provider, provider)
    r = rates.get((provider, model))
    if r is None and model.endswith("-cloud"):
        r = rates.get((provider, model[: -len("-cloud")]))
    if r is None and model.endswith(":cloud"):
        r = rates.get((provider, model[: -len(":cloud")]))
    if r is None:
        return 0.0, False
    c = tin * float(r.get("input", 0)) / 1e6 + tout * float(r.get("output", 0)) / 1e6
    return c, True


# --- extraction ------------------------------------------------------------


def parse_ts(value):
    if not value:
        return None
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None


def iter_turns(session_dir: Path):
    """Yield (event_id, completed_at, prev_ts, input_tokens, output_tokens).

    Only events carrying token usage become rows; user messages and tool events
    are not billable and have no place in a request log.
    """
    events = session_dir / "events.jsonl"
    if not events.exists():
        return
    prev_ts = None
    with events.open(errors="replace") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            ts = parse_ts(e.get("timestamp"))
            usage = e.get("usageMetadata")
            if isinstance(usage, dict):
                tin = usage.get("promptTokenCount") or 0
                tout = usage.get("candidatesTokenCount") or 0
                if (tin or tout) and ts is not None:
                    yield (e.get("id"), ts, prev_ts, int(tin), int(tout))
            if ts is not None:
                prev_ts = ts


def sql_str(value) -> str:
    if value is None:
        return "NULL"
    return "'" + str(value).replace("'", "''") + "'"


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--since", help="only sessions created on/after this date (YYYY-MM-DD)")
    ap.add_argument("--out", help="write SQL here (default: dry run, no output)")
    ap.add_argument("--apply", action="store_true", help="load --out via docker compose exec postgres")
    ap.add_argument("--env-file", default="../../.pi-go/.env", help="compose --env-file used by --apply")
    ap.add_argument("--batch", type=int, default=500, help="rows per INSERT statement")
    args = ap.parse_args()

    if args.apply and not args.out:
        ap.error("--apply requires --out")

    since = None
    if args.since:
        since = datetime.strptime(args.since, "%Y-%m-%d").replace(tzinfo=timezone.utc)

    if not SESSIONS.is_dir():
        print(f"no session directory at {SESSIONS}", file=sys.stderr)
        return 1

    rates = load_catalog()
    print(f"catalog: {len(rates)} priced models from {', '.join(p.name for p in CATALOGS if p.exists())}")

    rows = []
    stats = Counter()
    per_model = Counter()
    tokens_in = Counter()
    tokens_out = Counter()
    cost_by_model = Counter()
    unpriced = Counter()
    seen_ids: set[str] = set()

    for entry in sorted(SESSIONS.iterdir()):
        if not entry.is_dir():
            continue
        meta_path = entry / "meta.json"
        if not meta_path.exists():
            stats["sessions_no_meta"] += 1
            continue
        try:
            meta = json.loads(meta_path.read_text())
        except (json.JSONDecodeError, OSError):
            stats["sessions_unreadable"] += 1
            continue

        created = parse_ts(meta.get("createdAt"))
        if since and created and created < since:
            stats["sessions_before_since"] += 1
            continue

        declared = meta.get("provider")
        if declared in SKIP_PROVIDERS:
            stats["sessions_already_via_gateway"] += 1
            continue

        model = normalise_model(meta.get("model") or "")
        provider = infer_provider(model, declared)
        if provider == "unknown":
            stats["sessions_unknown_provider"] += 1
        stats["sessions_scanned"] += 1

        session_rows = 0
        for event_id, ts, prev_ts, tin, tout in iter_turns(entry):
            # The event id is the dedupe key; a re-run must not double-insert.
            rid = f"pigo-{event_id}" if event_id else f"pigo-{entry.name}-{session_rows}"
            if rid in seen_ids:
                stats["turns_duplicate_id"] += 1
                continue
            seen_ids.add(rid)

            duration_ms = 0
            if prev_ts is not None:
                duration_ms = int((ts - prev_ts).total_seconds() * 1000)
                duration_ms = max(0, min(duration_ms, MAX_DURATION_MS))
            started = ts - timedelta(milliseconds=duration_ms)

            cost, matched = cost_for(rates, provider, model, tin, tout)
            if not matched:
                unpriced[f"{provider}/{model}"] += 1

            attrs = {
                "agw.backfill": MARKER,
                "pi.session_id": meta.get("id") or entry.name,
                "pi.title": meta.get("title"),
                "pi.work_dir": meta.get("workDir"),
                "gen_ai.provider.name": provider,
                "gen_ai.request.model": model,
                "gen_ai.usage.input_tokens": tin,
                "gen_ai.usage.output_tokens": tout,
                "agw.ai.usage.cost.total": cost,
                "agw.cost_basis": "reconstructed-from-current-catalog" if matched else "no-rates",
            }
            rows.append(
                "("
                + ", ".join(
                    [
                        sql_str(rid),
                        sql_str(started.isoformat()),
                        sql_str(ts.isoformat()),
                        str(duration_ms),
                        "200",
                        sql_str("chat"),
                        sql_str(provider),
                        sql_str(model),
                        sql_str(model),
                        str(tin),
                        str(tout),
                        str(tin + tout),
                        repr(cost),
                        sql_str("pi-go"),
                        "false",
                        sql_str(json.dumps(attrs)) + "::jsonb",
                    ]
                )
                + ")"
            )
            session_rows += 1
            stats["turns"] += 1
            per_model[f"{provider}/{model}"] += 1
            tokens_in[f"{provider}/{model}"] += tin
            tokens_out[f"{provider}/{model}"] += tout
            cost_by_model[f"{provider}/{model}"] += cost

        if session_rows:
            stats["sessions_with_turns"] += 1

    # --- report ---
    print()
    for key in (
        "sessions_scanned",
        "sessions_with_turns",
        "sessions_already_via_gateway",
        "sessions_before_since",
        "sessions_unknown_provider",
        "sessions_no_meta",
        "sessions_unreadable",
        "turns",
        "turns_duplicate_id",
    ):
        if stats[key]:
            print(f"  {key:32} {stats[key]:,}")

    print(f"\n  {'model':40} {'rows':>7} {'in':>15} {'out':>10} {'cost':>10}")
    for name, n in per_model.most_common(20):
        print(f"  {name:40} {n:>7,} {tokens_in[name]:>15,} {tokens_out[name]:>10,} {cost_by_model[name]:>10.4f}")

    print(f"\n  TOTAL rows={stats['turns']:,} in={sum(tokens_in.values()):,} "
          f"out={sum(tokens_out.values()):,} cost=${sum(cost_by_model.values()):.4f}")
    if unpriced:
        print(f"\n  unpriced (cost recorded as 0) — add rates to pi-aliases.json to fix:")
        for name, n in unpriced.most_common(10):
            print(f"    {name:44} {n:,} turns")

    if not args.out:
        print("\ndry run — nothing written. Re-run with --out FILE [--apply] to load.")
        return 0

    cols = (
        "id, started_at, completed_at, duration_ms, http_status, gen_ai_operation_name, "
        "gen_ai_provider_name, gen_ai_request_model, gen_ai_response_model, "
        "input_tokens, output_tokens, total_tokens, cost, user_agent_name, has_payload, attributes_json"
    )
    out = Path(args.out)
    with out.open("w") as fh:
        fh.write("BEGIN;\n")
        for i in range(0, len(rows), args.batch):
            chunk = rows[i : i + args.batch]
            fh.write(f"INSERT INTO request_logs ({cols}) VALUES\n")
            fh.write(",\n".join(chunk))
            fh.write("\nON CONFLICT (id) DO NOTHING;\n")
        fh.write("COMMIT;\n")
    print(f"\nwrote {len(rows):,} rows to {out} ({out.stat().st_size/1e6:.1f} MB)")

    if not args.apply:
        print("re-run with --apply to load it, or pipe it into psql yourself.")
        return 0

    cmd = [
        "docker", "compose", "--env-file", args.env_file,
        "exec", "-T", "postgres",
        "psql", "-U", "agentgateway", "-d", "agentgateway", "-v", "ON_ERROR_STOP=1", "-q",
    ]
    print(f"loading via: {' '.join(cmd)}")
    with out.open("rb") as fh:
        proc = subprocess.run(cmd, stdin=fh, cwd=HERE)
    if proc.returncode != 0:
        print("load failed", file=sys.stderr)
        return proc.returncode
    print("loaded.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
