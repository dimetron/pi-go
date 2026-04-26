"""
Pi-go agent wrapper for running pi-go on Terminal-Bench with analytics collection.

Requires: uv tool install harbor

Setup:
    harbor dataset download terminal-bench/terminal-bench-2

Run a single task:
    MOUNTS='["/usr/local/bin/pi:/mnt/pi:ro", "~/.pi-go:/mnt/pi-go:ro"]'

    harbor run \
      -t terminal-bench/fix-git \
      -m anthropic/claude-sonnet-4-6 \
      --agent-import-path tbench_pi_agent:PiAgent \
      --mounts-json "$MOUNTS" \
      -n 1 -y

Run the full suite:
    harbor run \
      -d terminal-bench/terminal-bench-2 \
      -m anthropic/claude-sonnet-4-6 \
      --agent-import-path tbench_pi_agent:PiAgent \
      --mounts-json "$MOUNTS" \
      -n 4 -y

Expand ~ in MOUNTS to your actual home directory if your shell does not
expand inside single quotes.

Analytics:
    After each run, analytics are appended to TBENCH_CSV (default: tbench_runs.csv)
    in the same format as collect.py, so analyze.py can read them directly:

        python scripts/analyze.py tbench_runs.csv

    Set TBENCH_CSV env var to override the output path.
"""

import json
import os
import shlex
import subprocess
import sys
from datetime import datetime, timezone
from harbor.agents.installed.base import BaseInstalledAgent, with_prompt_template  # ty: ignore[unresolved-import]
from harbor.environments.base import BaseEnvironment  # ty: ignore[unresolved-import]
from harbor.models.agent.context import AgentContext  # ty: ignore[unresolved-import]
from pathlib import Path

from collect import append_csv, compute_cost, lookup_pricing

AGENT_LOG_FILE = "pi.txt"
AGENT_LOG_PATH = f"/logs/agent/{AGENT_LOG_FILE}"


def parse_pi_stream_json(proc, meta) -> tuple[dict, dict[int, dict], list[dict], str]:
    """Parse pi-go --mode json output from a subprocess.

    pi-go outputs JSONL with events:
      - message_start: session info
      - text_delta: text chunks (continues same turn)
      - tool_call: tool invocation
      - tool_result: tool response
      - thinking_delta: thinking chunks (hidden, used for turn tracking)
      - message_end: final message with session_id

    Returns (summary, per_turn_usage, tool_calls, result_text).
    """
    turn_usage: dict[int, dict] = {}
    tool_calls: list[dict] = []
    turn_index = 0
    model = ""
    session_id = ""
    result_text = ""
    started = False

    for raw_line in proc.stdout:
        line = raw_line.decode("utf-8", errors="replace").strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue

        msg_type = msg.get("type")

        if msg_type == "message_start":
            session_id = msg.get("session_id", session_id)
            model = msg.get("agent", "") or meta.get("model", "")
            started = True

        elif msg_type == "text_delta":
            result_text += msg.get("delta", "")

        elif msg_type == "tool_call":
            tool_calls.append({
                "turn": turn_index,
                "name": msg.get("tool_name"),
                "input": msg.get("tool_input", {}),
            })

        elif msg_type == "tool_result":
            # Tool result continues same turn
            pass

        elif msg_type == "thinking_delta":
            # New thinking_delta after tool_result marks new turn
            # (but we track turns by tool calls, not thinking)
            pass

        elif msg_type == "message_end":
            session_id = msg.get("session_id", session_id) or session_id

            # Estimate token usage from tool calls
            total_input_tokens = 0
            total_output_tokens = 0
            for tc in tool_calls:
                input_tokens = len(json.dumps(tc.get("input", {})).split()) * 2
                total_input_tokens += input_tokens
                total_output_tokens += 50  # rough estimate per tool

            # Add estimated output for text
            total_output_tokens += len(result_text.split()) * 2

            turn_usage[0] = {
                "input_tokens": total_input_tokens,
                "output_tokens": total_output_tokens,
                "cache_read_input_tokens": 0,
                "cache_creation_input_tokens": 0,
            }

    if not session_id:
        session_id = meta.get("session_id", "")

    summary = {
        "session_id": session_id,
        "model": model or meta.get("model", ""),
        "total_cost_usd": 0,
        "duration_ms": 0,
        "num_turns": max(turn_index + 1, 1),
        "usage": turn_usage.get(0, {
            "input_tokens": 0,
            "output_tokens": 0,
            "cache_read_input_tokens": 0,
            "cache_creation_input_tokens": 0,
        }),
    }

    return summary, turn_usage, tool_calls, result_text


def build_cmd_pi(args):
    """Build command to run pi-go in JSON mode."""
    cmd = [
        "pi", "--mode", "json",
    ]
    if args.model:
        cmd += ["--model", args.model]
    return cmd


def run_pi(args, meta):
    """Run pi-go as a subprocess and parse the JSONL stream."""
    cmd = build_cmd_pi(args)

    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    assert proc.stdin is not None
    assert proc.stdout is not None

    # Write prompt to stdin
    prompt = meta.get("prompt", "")
    proc.stdin.write(prompt.encode("utf-8"))
    proc.stdin.close()

    # Capture stderr for error logging
    stderr_output = b""
    import select
    import threading

    def capture_stderr():
        nonlocal stderr_output
        _, written, _ = select.select([], proc.stderr, [], 0.1)
        if proc.stderr:
            try:
                stderr_output = proc.stderr.read()
            except Exception:
                pass

    stderr_thread = threading.Thread(target=capture_stderr)
    stderr_thread.start()

    try:
        summary, turn_usage, tool_calls, result_text = parse_pi_stream_json(proc, meta)
    finally:
        stderr_thread.join(timeout=5)
        proc.wait()

    # Check for errors in stderr
    if proc.returncode != 0 and stderr_output:
        print(f"pi-go stderr: {stderr_output.decode('utf-8', errors='replace')}", file=sys.stderr)

    return summary, turn_usage, tool_calls, result_text


class PiAgent(BaseInstalledAgent):
    _last_instruction: str = ""

    @staticmethod
    def name() -> str:
        return "pi"

    def get_version_command(self) -> str | None:
        return "pi --version"

    async def install(self, environment: BaseEnvironment) -> None:
        await self.exec_as_root(
            environment,
            command="cp /mnt/pi /usr/local/bin/pi && chmod +x /usr/local/bin/pi && pi --version",
        )
        await self.exec_as_root(
            environment,
            command="if [ -d /mnt/pi-go ]; then mkdir -p /root/.pi-go && cp -r /mnt/pi-go/* /root/.pi-go/; fi",
        )

    @with_prompt_template
    async def run(
            self,
            instruction: str,
            environment: BaseEnvironment,
            context: AgentContext,
    ) -> None:
        if not self.model_name:
            raise ValueError("Model is required. Pass -m to harbor run.")

        self._last_instruction = instruction
        escaped = shlex.quote(instruction)
        await self.exec_as_agent(
            environment,
            command=(
                f"pi --mode json --model {self.model_name} "
                f"'{instruction}' 2>&1 | tee {AGENT_LOG_PATH}"
            ),
        )

    def populate_context_post_run(self, context: AgentContext) -> None:
        log_path = self.logs_dir / AGENT_LOG_FILE
        if not log_path.exists():
            print(f"No pi log found at {log_path}")
            return

        log_text = log_path.read_text(encoding="utf-8", errors="replace")
        if not log_text.strip():
            print("Pi log is empty")
            return

        result, turn_usage, tool_calls = parse_stream_json(log_text)
        usage = result.get("usage", {})

        context.n_input_tokens = (
                usage.get("input_tokens", 0)
                + usage.get("cache_read_input_tokens", 0)
                + usage.get("cache_creation_input_tokens", 0)
        )
        context.n_cache_tokens = usage.get("cache_read_input_tokens", 0)
        context.n_output_tokens = usage.get("output_tokens", 0)

        cost = result.get("total_cost_usd") or 0
        if cost == 0:
            pricing = lookup_pricing(result.get("model", ""))
            cost = compute_cost(usage, pricing)
        context.cost_usd = cost

        context.metadata = {
            "session_id": result.get("session_id"),
            "model": result.get("model"),
            "duration_ms": result.get("duration_ms"),
            "num_turns": result.get("num_turns"),
            "is_error": result.get("is_error", False),
            "n_tool_calls": len(tool_calls),
        }

        csv_path = Path(os.environ.get("TBENCH_CSV", "tbench_runs.csv"))
        meta = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "agent": "pi",
            "session_id": result.get("session_id", ""),
            "tag": "tbench",
            "model": result.get("model", ""),
            "prompt": self._last_instruction[:200],
        }
        summary = {
            "total_cost_usd": cost,
            "duration_ms": result.get("duration_ms", 0),
            "num_turns": result.get("num_turns", 0),
            "usage": usage,
        }
        append_csv(csv_path, meta, summary, turn_usage, tool_calls)
        print(f"Analytics appended to {csv_path}")


def parse_stream_json(log_text: str) -> tuple[dict, dict[int, dict], list[dict]]:
    """Parse pi-go --mode json log output.

    Returns (result_summary, per_turn_usage, tool_calls) matching collect.py's format.
    """
    result = {}
    turn_usage: dict[int, dict] = {}
    tool_calls: list[dict] = []
    turn_index = 0
    model = ""
    session_id = ""

    for line in log_text.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue

        msg_type = msg.get("type")

        if msg_type == "message_start":
            session_id = msg.get("session_id", session_id)
            model = msg.get("agent", "")

        elif msg_type == "text_delta":
            pass

        elif msg_type == "tool_call":
            tool_calls.append({
                "turn": turn_index,
                "name": msg.get("tool_name"),
                "input": msg.get("tool_input", {}),
            })

        elif msg_type == "tool_result":
            pass

        elif msg_type == "message_end":
            session_id = msg.get("session_id", session_id) or session_id
            total_input_tokens = 0
            total_output_tokens = 0
            for tc in tool_calls:
                input_tokens = len(json.dumps(tc.get("input", {})).split()) * 2
                total_input_tokens += input_tokens
                total_output_tokens += 50

            result = {
                "session_id": session_id,
                "model": model,
                "total_cost_usd": 0,
                "duration_ms": 0,
                "num_turns": max(turn_index + 1, 1),
                "usage": {
                    "input_tokens": total_input_tokens,
                    "output_tokens": total_output_tokens,
                    "cache_read_input_tokens": 0,
                    "cache_creation_input_tokens": 0,
                },
                "is_error": False,
            }
            turn_usage[turn_index] = result["usage"]

    if not result.get("session_id"):
        result["session_id"] = session_id
    if not result.get("model"):
        result["model"] = model

    return result, turn_usage, tool_calls
