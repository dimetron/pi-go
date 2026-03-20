# pi-terminal-bench Research

Source: https://github.com/badlogic/pi-terminal-bench
Author: badlogic (Mario Zechner)
License: MIT
Language split: JavaScript 65.2%, Python 28.2%, Shell 6.6%

## 1. Project Structure

```
pi-terminal-bench/
  .gitignore
  ERROR.md                              # Detailed upload_dir bug investigation
  README.md                             # Setup, usage, Harbor fix instructions
  pyproject.toml                        # Python project config (hatchling build)
  run.sh                                # Convenience script for running evals
  show-results.js                       # Results parser + leaderboard comparison
  src/
    pi_terminal_bench/
      __init__.py                       # Exports PiAgent
      pi_agent.py                       # Harbor agent adapter for pi
      install-pi.sh.j2                  # Jinja2 template for Docker pi installation
```

Output directories (gitignored):
- `pi-tbench-results/` -- evaluation results
- `jobs/`, `logs/`, `test-fix/`

## 2. PiAgent Class -- Full Analysis

### Location
`src/pi_terminal_bench/pi_agent.py`

### Inheritance
```python
from harbor.agents.installed.base import BaseInstalledAgent, ExecInput
from harbor.models.agent.context import AgentContext
from harbor.models.trial.paths import EnvironmentPaths

class PiAgent(BaseInstalledAgent):
```

`PiAgent` extends `BaseInstalledAgent` which is Harbor's interface for agents that get installed into Docker containers and then invoked with task instructions.

### Required Methods Implemented

#### `name() -> str`
Static method returning the agent identifier:
```python
@staticmethod
def name() -> str:
    return "pi"
```

#### `_install_agent_template_path -> Path`
Property pointing to the Jinja2 install script template:
```python
@property
def _install_agent_template_path(self) -> Path:
    return Path(__file__).parent / "install-pi.sh.j2"
```

#### `create_run_agent_commands(instruction: str) -> list[ExecInput]`
Core method that produces the commands Harbor will execute inside the Docker container:

```python
def create_run_agent_commands(self, instruction: str) -> list[ExecInput]:
    escaped_instruction = shlex.quote(instruction)

    env: dict[str, str] = {}

    # Forward all provider API keys from host environment
    for key in [
        "ANTHROPIC_OAUTH_TOKEN",
        "ANTHROPIC_API_KEY",
        "OPENAI_API_KEY",
        "GEMINI_API_KEY",
        "GROQ_API_KEY",
        "XAI_API_KEY",
        "OPENROUTER_API_KEY",
    ]:
        if key in os.environ:
            env[key] = os.environ[key]

    # Parse provider/model from Harbor's model format
    model_args = ""
    if self.model_name:
        provider, model = self._parse_model_name(self.model_name)
        model_args = f"--provider {provider} --model {model}"

    output_dir = EnvironmentPaths.agent_dir
    session_file = output_dir / "session.jsonl"
    json_output_file = output_dir / "pi-output.jsonl"

    return [
        ExecInput(command=f"mkdir -p {output_dir}", env=env),
        ExecInput(
            command=(
                f"pi --print --mode json --session {session_file} "
                f"{model_args} "
                f"{escaped_instruction} "
                f"2>&1 | tee {json_output_file}"
            ),
            env=env,
        ),
    ]
```

Key pi CLI flags used:
- `--print` -- print output to stdout
- `--mode json` -- structured JSONL output (for trajectory parsing)
- `--session <path>` -- persist session to file
- `--provider <provider>` -- LLM provider name
- `--model <model>` -- model identifier
- Instruction is passed as a positional argument (shell-escaped)
- Output piped through `tee` to capture to `pi-output.jsonl`

#### `populate_context_post_run(context: AgentContext) -> None`
Parses pi's JSONL output after execution to extract token usage and cost:

```python
def populate_context_post_run(self, context: AgentContext) -> None:
    json_output_file = self.logs_dir / "pi-output.jsonl"

    if not json_output_file.exists():
        return

    total_input_tokens = 0
    total_output_tokens = 0
    total_cache_read_tokens = 0
    total_cache_write_tokens = 0
    total_cost = 0.0

    with open(json_output_file) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
                if event.get("type") == "message_end":
                    message = event.get("message", {})
                    if message.get("role") == "assistant":
                        usage = message.get("usage", {})
                        total_input_tokens += usage.get("input", 0)
                        total_output_tokens += usage.get("output", 0)
                        total_cache_read_tokens += usage.get("cacheRead", 0)
                        total_cache_write_tokens += usage.get("cacheWrite", 0)
                        cost = usage.get("cost", {})
                        total_cost += cost.get("total", 0.0)
            except json.JSONDecodeError:
                continue

    context.n_input_tokens = total_input_tokens
    context.n_output_tokens = total_output_tokens
    context.n_cache_tokens = total_cache_read_tokens + total_cache_write_tokens
    context.cost_usd = total_cost if total_cost > 0 else None
```

Expected JSONL event structure from pi:
```json
{
  "type": "message_end",
  "message": {
    "role": "assistant",
    "usage": {
      "input": 1234,
      "output": 567,
      "cacheRead": 890,
      "cacheWrite": 100,
      "cost": { "total": 0.05 }
    }
  }
}
```

#### `_parse_model_name(model_name: str) -> tuple[str, str]`
Splits Harbor's `provider/model` format into separate args:
```python
def _parse_model_name(self, model_name: str) -> tuple[str, str]:
    if "/" in model_name:
        parts = model_name.split("/", 1)
        return parts[0], parts[1]
    return "anthropic", model_name  # default provider
```

## 3. How Pi is Invoked

### Command Line Construction
Inside the Docker container, pi is invoked as:
```bash
pi --print --mode json --session /agent/session.jsonl \
    --provider anthropic --model claude-sonnet-4-5 \
    'Task instruction text here...' \
    2>&1 | tee /agent/pi-output.jsonl
```

### Environment Variables Forwarded
The adapter forwards these from the host into the container:
- `ANTHROPIC_OAUTH_TOKEN` (preferred for Anthropic)
- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `GEMINI_API_KEY`
- `GROQ_API_KEY`
- `XAI_API_KEY`
- `OPENROUTER_API_KEY`

### Pi Installation in Docker (install-pi.sh.j2)
```bash
#!/bin/bash
set -e

apt-get update
apt-get install -y curl

# Install Node.js via nvm
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.2/install.sh | bash
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"
nvm install 22

# Install pi coding agent from npm
{% if version %}
npm install -g @mariozechner/pi-coding-agent@{{ version }}
{% else %}
npm install -g @mariozechner/pi-coding-agent@latest
{% endif %}

pi --help
```

The Jinja2 template supports an optional `version` variable. If not set, installs `@latest`.
npm package: `@mariozechner/pi-coding-agent`

## 4. Harbor Agent Adapter Interface

The `BaseInstalledAgent` interface requires implementing:

| Method | Purpose |
|--------|---------|
| `name() -> str` | Static. Agent identifier string used in eval keys. |
| `_install_agent_template_path -> Path` | Property. Path to Jinja2 shell script template for installing the agent in Docker. |
| `create_run_agent_commands(instruction: str) -> list[ExecInput]` | Returns list of `ExecInput(command=str, env=dict)` that Harbor executes sequentially in the container. |
| `populate_context_post_run(context: AgentContext) -> None` | Optional. Called after agent run to extract token usage, cost, etc. from agent output files. |

### ExecInput
```python
ExecInput(
    command: str,    # Shell command to run
    env: dict[str, str],  # Environment variables
)
```

### AgentContext Fields Set
```python
context.n_input_tokens   # Total input tokens
context.n_output_tokens  # Total output tokens
context.n_cache_tokens   # Cache read + write tokens
context.cost_usd         # Total cost in USD (or None)
```

### Harbor Integration Flow
1. Harbor creates a Docker container for the task
2. Harbor runs the install template (installs Node.js + pi)
3. Harbor calls `create_run_agent_commands(instruction)` with the task prompt
4. Harbor executes the returned commands sequentially in the container
5. Harbor calls `populate_context_post_run(context)` to gather metrics
6. Harbor runs the verifier (uploads test files, executes test.sh)
7. Harbor collects reward from verifier output

## 5. Docker Setup and Isolation Model

### Container Lifecycle
- Each task gets its own Docker container
- Harbor manages docker-compose for container orchestration
- The agent install template runs first (apt-get, nvm, npm install)
- The agent then runs inside `/app` (standard Harbor working directory)
- Agent output goes to `EnvironmentPaths.agent_dir` (typically `/agent/`)

### Isolation
- Each task is fully isolated in its own container
- No shared state between tasks
- API keys are injected via environment variables per command
- Container is destroyed after verification completes

### Parallelism
- `-n <N>` controls concurrent task execution
- Local Docker: typically `-n 4`
- Daytona cloud: up to `-n 32`

## 6. Verifier and Test Validation

### How Verification Works
1. After the agent completes, Harbor's verifier uploads test files from `task.paths.tests_dir` to `/tests` in the container
2. The verifier executes `/tests/test.sh` inside the container
3. The test script validates the agent's work (checks files created, commands run, outputs produced)
4. The verifier reads `reward.txt` or `reward.json` for the final score
5. Reward is binary: `1.0` (pass) or `0.0` (fail)

### Reward Collection
Results are aggregated in `result.json` under:
```json
{
  "stats": {
    "evals": {
      "<agent>__<model>__terminal-bench": {
        "reward_stats": {
          "reward": {
            "1.0": ["task_id_1", "task_id_2"],
            "0.0": ["task_id_3"]
          }
        },
        "n_errors": 0
      }
    }
  }
}
```

## 7. show-results.js -- Results Display

### Location
`show-results.js` (root, executable Node.js script)

### What It Does
1. Finds the latest results directory in `pi-tbench-results/`
2. Parses `result.json` to extract pass/fail counts
3. Calculates accuracy and standard error
4. Inserts pi's result into the Terminal-Bench 2.0 leaderboard at the correct rank
5. Displays a formatted ANSI-colored table

### Results Parsing Logic
```javascript
function parseResults(resultsPath) {
  const data = JSON.parse(fs.readFileSync(resultsPath, 'utf8'));
  const evalKey = Object.keys(data.stats.evals)[0];
  const stats = data.stats.evals[evalKey];
  const rewards = stats.reward_stats.reward;

  const passed = (rewards["1.0"] || []).length;
  const failed = (rewards["0.0"] || []).length;
  const total = passed + failed;
  const errors = stats.n_errors;

  const accuracy = (passed / total) * 100;
  const stderr = Math.sqrt((accuracy / 100) * (1 - accuracy / 100) / total) * 100;

  // Extract agent and model from eval key: "pi__claude-opus-4-5__terminal-bench"
  const parts = evalKey.split('__');
  const agent = parts[0];
  const model = parts[1].replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());

  return { agent, model, accuracy, stderr, passed, failed, total, errors };
}
```

### Eval Key Format
Pattern: `<agent>__<model-with-dashes>__terminal-bench`
Example: `pi__claude-opus-4-5__terminal-bench`

### Standard Error Calculation
Uses binomial standard error: `stderr = sqrt(p * (1-p) / n) * 100`

### Leaderboard
The script embeds a hardcoded leaderboard array (60 entries as of Dec 1, 2025) and inserts pi at the correct rank position. Top entries include:
- #1: Codex CLI / GPT-5.1-Codex-Max (60.4%)
- #2: Warp / Multiple (59.1%)
- #3: II-Agent / Gemini 3 Pro (58.9%)
- #17: Claude Code / Claude Sonnet 4.5 (40.1%)

### Usage
```bash
./show-results.js              # auto-finds latest in pi-tbench-results/
./show-results.js /path/to/dir # explicit results directory
```

## 8. Upload_dir Bug and Patch

### The Problem
Harbor's `upload_dir` uses `docker cp source container:/target`. When the agent creates a `/tests` directory during task execution (e.g., `mkdir -p /tests`), the subsequent verifier upload places test files at `/tests/tests/test.sh` instead of `/tests/test.sh`.

Docker cp behavior:
- Target does NOT exist: copies contents into target path (correct)
- Target ALREADY exists: copies source as subdirectory of target (broken)

### Impact
- Verifier fails with `bash: /tests/test.sh: No such file or directory`
- Results in `RewardFileNotFoundError` -- no reward.txt generated
- Oracle agent is unaffected (doesn't create `/tests`)

### The Fix
Append `/.` to the source path so `docker cp` always copies contents:

```python
# BEFORE (broken)
async def upload_dir(self, source_dir: Path | str, target_dir: str):
    await self._run_docker_compose_command(
        ["cp", str(source_dir), f"main:{target_dir}"],
        check=True,
    )

# AFTER (fixed)
async def upload_dir(self, source_dir: Path | str, target_dir: str):
    source = str(source_dir).rstrip('/') + '/.'
    await self._run_docker_compose_command(
        ["cp", source, f"main:{target_dir}"],
        check=True,
    )
```

### Applying the Patch
```bash
# Find Harbor's docker.py
HARBOR_DOCKER=$(python -c "import harbor.environments.docker.docker as m; print(m.__file__)")
# Back up and patch
cp "$HARBOR_DOCKER" "${HARBOR_DOCKER}.bak"
# Apply fix (one-liner provided in README)

# Verify
python -c "from harbor.environments.docker.docker import DockerEnvironment; import inspect; print('PATCHED' if 'rstrip' in inspect.getsource(DockerEnvironment.upload_dir) else 'NOT PATCHED')"
```

## 9. Provider Configuration

### Supported Providers
Harbor model format: `provider/model-name`

| Provider | Env Var | Example Model |
|----------|---------|---------------|
| Anthropic | `ANTHROPIC_OAUTH_TOKEN` (preferred) or `ANTHROPIC_API_KEY` | `anthropic/claude-sonnet-4-5` |
| OpenAI | `OPENAI_API_KEY` | `openai/gpt-4o` |
| Google | `GEMINI_API_KEY` | `google/gemini-2.5-pro` |
| Groq | `GROQ_API_KEY` | `groq/...` |
| xAI | `XAI_API_KEY` | `xai/...` |
| OpenRouter | `OPENROUTER_API_KEY` | `openrouter/...` |

### Default Provider
If no `/` in model name, defaults to `anthropic`.

### run.sh Default Config
The convenience script `run.sh` uses:
- Model: `anthropic/claude-opus-4-5`
- Attempts: 5 (`--n-attempts 5`)
- Parallelism: 4 (`-n 4`)
- Output: `./pi-tbench-results`

## 10. Daytona Cloud Deployment

### What is Daytona
Daytona provides cloud-based container environments as an alternative to local Docker.

### Configuration
```bash
export DAYTONA_API_KEY="..."
```

### Usage
```bash
harbor run \
  -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5 \
  --env daytona \
  -n 32
```

Key differences from local Docker:
- `--env daytona` -- uses Daytona instead of local Docker
- `-n 32` -- much higher parallelism (vs `-n 4` locally)
- Requires `DAYTONA_API_KEY` environment variable
- No local Docker required

## 11. Build and Development

### pyproject.toml
- Build system: hatchling
- Python: >= 3.11
- Dependencies: `harbor>=0.1.0`
- Dev deps: pytest, ruff
- Package location: `src/pi_terminal_bench` (hatch wheel config)

### Development Commands
```bash
# Install for development
uv venv && source .venv/bin/activate
uv pip install -e ".[dev]"

# Tests
pytest

# Linting
ruff check src/
ruff format src/
```

## 12. Key Takeaways for pi-go Implementation

1. **Harbor adapter is minimal**: Only 4 methods needed (name, install template path, run commands, post-run context).
2. **Pi is invoked as CLI**: `pi --print --mode json --session <file> --provider <p> --model <m> '<instruction>'`
3. **JSONL output format**: Pi outputs structured events; `message_end` events with `role: assistant` contain usage data.
4. **Session file**: Pi supports `--session` for persisting conversation state to JSONL.
5. **API keys are forwarded as env vars**: No config files, just environment variables.
6. **Docker isolation**: Each task gets its own container with clean state.
7. **upload_dir bug is critical**: Must be patched before running evals with any agent that creates `/tests`.
8. **Results format**: `result.json` with `stats.evals.<key>.reward_stats.reward` containing task ID lists under "1.0" and "0.0" keys.
9. **Leaderboard submission**: Email results directory to Terminal-Bench maintainers.
10. **Terminal-Bench 2.0**: The benchmark version used; dataset loaded via `-d terminal-bench@2.0`.
