#!/usr/bin/env python3
"""Analyze tool token usage from ~/.pi-go/sessions to identify optimization targets."""

import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

SESSION_DIR = Path.home() / ".pi-go" / "sessions"
CHARS_PER_TOKEN = 4  # rough estimate for token counting from char length


def estimate_tokens(text):
    if isinstance(text, str):
        return len(text) // CHARS_PER_TOKEN
    if isinstance(text, list):
        return sum(len(json.dumps(item)) for item in text) // CHARS_PER_TOKEN
    return 0


def load_sessions():
    """Load sessions from ~/.pi-go/sessions as JSONL files.

    pi-go session files contain JSONL entries with fields:
      type: "session_start", "user", "llm_text", "tool_call", "tool_result", "error", "info"
      content: JSON string for tool_call/tool_result
    """
    sessions = []
    for f in SESSION_DIR.glob("*.json"):
        try:
            with open(f) as fh:
                messages = []
                for line in fh:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        entry = json.loads(line)
                        messages.append(entry)
                    except json.JSONDecodeError:
                        continue
                if messages:
                    sessions.append({"messages": messages})
        except (json.JSONDecodeError, OSError):
            continue
    return sessions


def extract_tool_calls(session):
    """Extract tool calls with their input/output sizes from a pi-go JSONL session.

    pi-go session entries have type: tool_call/tool_result with JSON content.
    """
    messages = session.get("messages", [])
    calls = []

    pending_tools = {}

    for msg in messages:
        msg_type = msg.get("type", "")
        content_raw = msg.get("content", "")

        if msg_type == "tool_call":
            try:
                inp = json.loads(content_raw) if isinstance(content_raw, str) else (content_raw or {})
            except (json.JSONDecodeError, TypeError):
                inp = {}

            tool_id = inp.get("id", "") or content_raw[:20] if isinstance(content_raw, str) else ""
            name = inp.get("name", inp.get("tool_name", "unknown"))
            input_str = json.dumps(inp)
            pending_tools[tool_id] = {
                "name": name,
                "input": inp,
                "input_chars": len(input_str),
                "input_tokens_est": len(input_str) // CHARS_PER_TOKEN,
            }

        elif msg_type == "tool_result":
            try:
                result = json.loads(content_raw) if isinstance(content_raw, str) else (content_raw or {})
            except (json.JSONDecodeError, TypeError):
                result = content_raw if isinstance(content_raw, str) else ""

            if isinstance(result, dict):
                result_content = json.dumps(result)
            else:
                result_content = str(result)

            output_chars = len(result_content)
            # Find matching tool call by scanning pending_tools
            tool_id = ""
            tool_info = {"name": "unknown", "input": {}, "input_chars": 0, "input_tokens_est": 0}
            for tid, info in pending_tools.items():
                tool_info = info
                tool_id = tid
                break
            if tool_id:
                del pending_tools[tool_id]
            calls.append({
                "name": tool_info["name"],
                "input": tool_info["input"],
                "input_chars": tool_info["input_chars"],
                "input_tokens_est": tool_info["input_tokens_est"],
                "output_chars": output_chars,
                "output_tokens_est": output_chars // CHARS_PER_TOKEN,
            })

    return calls


def fmt_num(n):
    return f"{int(n):,}"


def fmt_pct(n, total):
    return f"{n / total * 100:.1f}%" if total > 0 else "0.0%"


def bar(value, max_val, width=40):
    if max_val == 0:
        return ""
    filled = int(value / max_val * width)
    return "█" * filled + "░" * (width - filled)


def print_table(title, headers, rows, aligns=None):
    if not rows:
        return
    aligns = aligns or ["<"] * len(headers)
    cols = [[h] + [str(r[i]) for r in rows] for i, h in enumerate(headers)]
    widths = [max(len(str(c)) for c in col) for col in cols]

    print(f"\n┌{'─' * (sum(widths) + 2 * len(widths) + len(widths) - 1)}┐")
    print(f"│ {title:^{sum(widths) + 3 * (len(widths) - 1)}} │")
    print(f"├{'─' * (sum(widths) + 2 * len(widths) + len(widths) - 1)}┤")

    header_str = " │ ".join(f"{h:{a}{w}}" for h, a, w in zip(headers, aligns, widths))
    print(f"│ {header_str} │")
    print(f"├{'─' * (sum(widths) + 2 * len(widths) + len(widths) - 1)}┤")

    for r in rows:
        row_str = " │ ".join(f"{str(r[i]):{a}{w}}" for i, (a, w) in enumerate(zip(aligns, widths)))
        print(f"│ {row_str} │")

    print(f"└{'─' * (sum(widths) + 2 * len(widths) + len(widths) - 1)}┘")


def analyze_tool_distribution(all_calls):
    """Aggregate stats per tool."""
    stats: dict[str, dict[str, Any]] = defaultdict(lambda: {
        "count": 0,
        "input_chars": 0,
        "output_chars": 0,
        "input_tokens": 0,
        "output_tokens": 0,
        "total_tokens": 0,
        "output_sizes": [],
        "input_sizes": [],
    })

    for call in all_calls:
        name = call["name"]
        s = stats[name]
        s["count"] += 1
        s["input_chars"] += call.get("input_chars", 0)
        s["output_chars"] += call.get("output_chars", 0)
        inp_tok = call.get("input_tokens_est", 0)
        out_tok = call.get("output_tokens_est", 0)
        s["input_tokens"] += inp_tok
        s["output_tokens"] += out_tok
        s["total_tokens"] += inp_tok + out_tok
        s["output_sizes"].append(call.get("output_chars", 0))
        s["input_sizes"].append(call.get("input_chars", 0))

    return dict(stats)


def print_distribution_table(stats):
    total_all_tokens = sum(s["total_tokens"] for s in stats.values())
    sorted_tools = sorted(stats.items(), key=lambda x: -x[1]["total_tokens"])

    rows = []
    for name, s in sorted_tools:
        avg_out = s["output_tokens"] // s["count"] if s["count"] else 0
        avg_in = s["input_tokens"] // s["count"] if s["count"] else 0
        median_out = sorted(s["output_sizes"])[len(s["output_sizes"]) // 2] // CHARS_PER_TOKEN if s[
            "output_sizes"] else 0
        p95_out = sorted(s["output_sizes"])[int(len(s["output_sizes"]) * 0.95)] // CHARS_PER_TOKEN if s[
            "output_sizes"] else 0
        max_out = max(s["output_sizes"]) // CHARS_PER_TOKEN if s["output_sizes"] else 0
        rows.append((
            name,
            fmt_num(s["count"]),
            fmt_num(s["total_tokens"]),
            fmt_pct(s["total_tokens"], total_all_tokens),
            fmt_num(avg_in),
            fmt_num(avg_out),
            fmt_num(median_out),
            fmt_num(p95_out),
            fmt_num(max_out),
            bar(s["total_tokens"], sorted_tools[0][1]["total_tokens"], 30),
        ))

    print_table(
        "Tool Token Distribution (estimated tokens, sorted by total impact)",
        ["Tool", "Count", "Total Tok", "% Total", "Avg In", "Avg Out", "Med Out", "P95 Out", "Max Out", "Impact"],
        rows,
        ["<", ">", ">", ">", ">", ">", ">", ">", ">", "<"],
    )


def print_output_cost_table(stats):
    """Focus on output tokens since those are the expensive part (tool results going INTO context)."""
    total_output = sum(s["output_tokens"] for s in stats.values())
    sorted_tools = sorted(stats.items(), key=lambda x: -x[1]["output_tokens"])

    rows = []
    cumulative = 0
    for name, s in sorted_tools:
        cumulative += s["output_tokens"]
        avg_out = s["output_tokens"] // s["count"] if s["count"] else 0
        rows.append((
            name,
            fmt_num(s["count"]),
            fmt_num(s["output_tokens"]),
            fmt_pct(s["output_tokens"], total_output),
            fmt_pct(cumulative, total_output),
            fmt_num(avg_out),
            bar(s["output_tokens"], sorted_tools[0][1]["output_tokens"], 30),
        ))

    print_table(
        "Tool OUTPUT Tokens (what goes into context window - key cost driver)",
        ["Tool", "Count", "Out Tokens", "% of Out", "Cumul %", "Avg Out", "Cost Bar"],
        rows,
        ["<", ">", ">", ">", ">", ">", "<"],
    )


def print_input_cost_table(stats):
    """Tool input tokens = what the model generates to call tools (output tokens cost)."""
    total_input = sum(s["input_tokens"] for s in stats.values())
    sorted_tools = sorted(stats.items(), key=lambda x: -x[1]["input_tokens"])

    rows = []
    for name, s in sorted_tools:
        avg_in = s["input_tokens"] // s["count"] if s["count"] else 0
        rows.append((
            name,
            fmt_num(s["count"]),
            fmt_num(s["input_tokens"]),
            fmt_pct(s["input_tokens"], total_input),
            fmt_num(avg_in),
            bar(s["input_tokens"], sorted_tools[0][1]["input_tokens"], 30),
        ))

    print_table(
        "Tool INPUT Tokens (model-generated call args - billed as output tokens)",
        ["Tool", "Count", "In Tokens", "% of In", "Avg In", "Cost Bar"],
        rows,
        ["<", ">", ">", ">", ">", "<"],
    )


def print_histogram(title, tool_name, sizes, bins=10):
    """Print a text histogram of output sizes for a tool."""
    if not sizes:
        return
    token_sizes = [s // CHARS_PER_TOKEN for s in sizes]
    min_val = min(token_sizes)
    max_val = max(token_sizes)
    if max_val == min_val:
        print(f"\n{title}: all values = {min_val}")
        return

    bin_width = (max_val - min_val) / bins
    buckets = [0] * bins
    for v in token_sizes:
        idx = min(int((v - min_val) / bin_width), bins - 1)
        buckets[idx] += 1

    max_count = max(buckets)
    print(f"\n  {title} (n={len(sizes)})")
    print(f"  {'─' * 60}")
    for i, count in enumerate(buckets):
        lo = int(min_val + i * bin_width)
        hi = int(min_val + (i + 1) * bin_width)
        bar_len = int(count / max_count * 40) if max_count else 0
        print(f"  {lo:>8}-{hi:<8} │{'█' * bar_len} {count}")
    print()


def print_top_expensive_calls(all_calls, n=15):
    """Show the individual most expensive tool calls."""
    sorted_calls = sorted(all_calls, key=lambda c: -c.get("output_chars", 0))

    rows = []
    for call in sorted_calls[:n]:
        name = call["name"]
        out_tok = call.get("output_tokens_est", 0)
        in_tok = call.get("input_tokens_est", 0)
        inp = call.get("input", {})

        if name == "bash":
            detail = str(inp.get("command", ""))[:60]
        elif name == "read":
            detail = str(inp.get("path", ""))[:60]
        elif name in ("edit", "multiedit"):
            detail = str(inp.get("path", ""))[:60]
        elif name == "write":
            detail = str(inp.get("path", ""))[:60]
        elif name == "grep":
            detail = str(inp.get("pattern", ""))[:60]
        elif name == "task":
            detail = str(inp.get("description", ""))[:60]
        elif name == "batch":
            tools = inp.get("tool_calls", [])
            detail = f"{len(tools)} calls: " + ",".join(t.get("tool", "?") for t in tools[:5])
            detail = detail[:60]
        elif name == "code_execution":
            detail = str(inp.get("code", ""))[:60].replace("\n", "\\n")
        else:
            detail = str(inp)[:60]

        rows.append((
            name,
            fmt_num(out_tok),
            fmt_num(in_tok),
            detail,
        ))

    print_table(
        f"Top {n} Most Expensive Individual Tool Calls (by output tokens)",
        ["Tool", "Out Tok", "In Tok", "Detail"],
        rows,
        ["<", ">", ">", "<"],
    )


def print_batch_subtool_analysis(batch_sub_calls):
    """Analyze what tools are called inside batch and their output sizes."""
    stats: dict[str, dict[str, Any]] = defaultdict(lambda: {"count": 0, "output_tokens": 0, "sizes": []})
    for call in batch_sub_calls:
        name = call["name"]
        s = stats[name]
        s["count"] += 1
        s["output_tokens"] += call["output_tokens_est"]
        s["sizes"].append(call["output_chars"])

    total = sum(s["output_tokens"] for s in stats.values())
    sorted_tools = sorted(stats.items(), key=lambda x: -x[1]["output_tokens"])

    rows = []
    for name, s in sorted_tools:
        avg = s["output_tokens"] // s["count"] if s["count"] else 0
        rows.append((
            name,
            fmt_num(s["count"]),
            fmt_num(s["output_tokens"]),
            fmt_pct(s["output_tokens"], total),
            fmt_num(avg),
        ))

    print_table(
        "Inside Batch: Sub-tool Output Token Distribution",
        ["Tool", "Count", "Out Tokens", "% of Batch Out", "Avg Out"],
        rows,
        ["<", ">", ">", ">", ">"],
    )


def print_session_summary(sessions, all_calls):
    total_sessions = len(sessions)
    sessions_with_tools = sum(1 for s in sessions if s.get("messages"))
    total_calls = len(all_calls)
    total_input_tok = sum(c.get("input_tokens_est", 0) for c in all_calls)
    total_output_tok = sum(c.get("output_tokens_est", 0) for c in all_calls)

    agg_usage = defaultdict(int)
    for s in sessions:
        u = s.get("token_usage", {})
        for k, v in u.items():
            agg_usage[k] += v
    # pi-go JSONL sessions don't have token_usage field; compute from tool calls
    if agg_usage.get("input_tokens", 0) == 0 and total_input_tok > 0:
        agg_usage["input_tokens"] = total_input_tok
        agg_usage["output_tokens"] = total_output_tok

    print("\n" + "═" * 70)
    print("  PI-GO SESSION TOOL TOKEN ANALYSIS")
    print("═" * 70)
    print(f"  Sessions analyzed:     {total_sessions}")
    print(f"  Sessions with tools:   {sessions_with_tools}")
    print(f"  Total tool calls:      {fmt_num(total_calls)}")
    print(f"  Tool input tokens:     {fmt_num(total_input_tok)} (model generates these)")
    print(f"  Tool output tokens:    {fmt_num(total_output_tok)} (go into context)")
    print("  ─────────────────────────────────")
    print("  Aggregate session token usage:")
    print(f"    input_tokens:                {fmt_num(agg_usage.get('input_tokens', 0))}")
    print(f"    output_tokens:               {fmt_num(agg_usage.get('output_tokens', 0))}")
    print(f"    cache_creation_input_tokens: {fmt_num(agg_usage.get('cache_creation_input_tokens', 0))}")
    print(f"    cache_read_input_tokens:     {fmt_num(agg_usage.get('cache_read_input_tokens', 0))}")
    print()


def main():
    sessions = load_sessions()
    if not sessions:
        print("No sessions found in", SESSION_DIR, file=sys.stderr)
        sys.exit(1)

    all_calls = []
    for session in sessions:
        calls = extract_tool_calls(session)
        all_calls.extend(calls)

    print_session_summary(sessions, all_calls)

    stats = analyze_tool_distribution(all_calls)
    print_distribution_table(stats)
    print_output_cost_table(stats)
    print_input_cost_table(stats)

    print_top_expensive_calls(all_calls)

    top_tools = sorted(stats.items(), key=lambda x: -x[1]["output_tokens"])[:3]
    for name, s in top_tools:
        if s["output_sizes"]:
            print_histogram(f"{name} output token distribution", name, s["output_sizes"])


if __name__ == "__main__":
    main()
