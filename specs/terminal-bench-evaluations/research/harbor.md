# Harbor Framework Research

> Research date: 2026-03-19
> Sources: GitHub, harborframework.com, pi-terminal-bench repo, web search

## 1. What is Harbor?

Harbor is a framework for evaluating and optimizing AI agents and language models in containerized environments. Created by the Terminal-Bench team (Laude Institute), it was released alongside Terminal-Bench 2.0.

- **GitHub**: https://github.com/harbor-framework/harbor (also mirrored at github.com/laude-institute/harbor)
- **Website**: https://harborframework.com/
- **Registry**: https://harborframework.com/registry
- **PyPI**: https://pypi.org/project/harbor/
- **License**: Apache-2.0
- **Language**: Python (87.2%)
- **Stats**: ~1k stars, 774 forks, 149 contributors, 632 commits

### Installation

```bash
uv tool install harbor
# or
pip install harbor
```

### Origin

Emerged from Terminal-Bench (May 2025). The team observed users doing custom evals, prompt optimization, RL, SFT trace generation, and CI/CD agent testing -- but managing containerized tasks at scale was difficult. Harbor was built to solve this.

---

## 2. Agent Adapter Interface

Harbor supports two agent types:

### External Agents (BaseAgent)

Interface with environments through `BaseEnvironment.exec()` (runs bash commands in the container).

```python
from harbor.agents.base import BaseAgent

class MyExternalAgent(BaseAgent):
    @staticmethod
    def name() -> str:
        """The name of the agent."""
        pass

    def version(self) -> str | None:
        """The version of the agent."""
        pass

    async def setup(self, environment: BaseEnvironment) -> None:
        """Run commands to setup the agent & its tools."""
        pass

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        """Runs the agent in the environment. Populate context with results."""
        pass
```

### Installed Agents (BaseInstalledAgent)

Deployed directly into container environments and run in headless mode.

```python
from harbor.agents.installed.base import BaseInstalledAgent

class ExecInput(BaseModel):
    command: str
    cwd: str | None = None
    env: dict[str, str] | None = None
    timeout_sec: int | None = None

class MyInstalledAgent(BaseInstalledAgent):
    @property
    def _install_agent_template_path(self) -> Path:
        """Path to jinja template script for container installation."""
        pass

    def create_run_agent_commands(self, instruction: str) -> list[ExecInput]:
        """Create commands executing agent in headless mode."""
        pass

    def populate_context_post_run(self, context: AgentContext) -> None:
        """Populate context with execution results."""
        pass
```

### Running a Custom Agent

```bash
harbor run -d "<dataset@version>" --agent-import-path path.to.agent:SomeAgent
```

### Pre-integrated Agents

Harbor ships with: Terminus-2, Claude Code, Codex CLI, Gemini CLI, OpenHands, Mini-SWE-Agent.

### pi-terminal-bench Example

The `pi-terminal-bench` repo (https://github.com/badlogic/pi-terminal-bench) implements `PiAgent` class:

```bash
harbor run -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5
```

---

## 3. Docker Isolation Model

### Environments = Containers

Every task runs in an isolated container. The `BaseEnvironment` interface provides unified interaction:
- `exec(command)` -- execute bash commands inside the container
- `upload_dir(source, target)` -- copy files into the container
- File download for artifact collection post-trial

### Task Directory Structure

```
<task-id>/
├── task.toml           # Configuration and metadata
├── instruction.md      # Agent instructions (markdown)
├── environment/
│   └── Dockerfile      # Container definition (or docker-compose.yaml)
├── solution/
│   └── solve.sh        # Oracle solution script (optional)
└── tests/
    ├── test.sh          # Test execution + reward writing
    └── test_*.py        # Optional pytest files
```

### Container Resource Configuration (task.toml)

```toml
[environment]
build_timeout_sec = 600
docker_image = null          # or pre-built image name
cpus = 1
memory_mb = 2048
storage_mb = 10240
gpus = 0
gpu_types = null             # e.g. ["H100", "A100"]
allow_internet = true
mcp_servers = []             # MCP server configurations

[agent]
timeout_sec = 120

[verifier]
timeout_sec = 120
```

### Special Filesystem Paths Inside Container

| Path | Purpose |
|------|---------|
| `/logs/verifier/` | Reward file and verifier logs (downloaded post-run) |
| `/logs/agent/` | Agent logs storage |
| `/solution/` | Copied solution folder |
| `/tests/` | Copied tests folder |

### Supported Container Runtimes

- **Docker** (local) -- uses `environment/Dockerfile` or `environment/docker-compose.yaml`
- **Daytona** -- cloud sandbox provider
- **Modal** -- serverless cloud
- **E2B** -- cloud sandbox
- **Runloop** -- cloud runtime
- **Kubernetes** -- self-managed clusters
- Custom runtimes via `BaseEnvironment` interface implementation

---

## 4. Task Distribution and Result Collection

### Core Concepts

| Concept | Description |
|---------|-------------|
| **Task** | Single instruction + container environment + test script |
| **Dataset** | Collection of tasks (= a benchmark) |
| **Trial** | An agent's attempt at completing a task; produces a reward |
| **Job** | Orchestrates evaluations across datasets, agents, tasks, models |

### Execution Flow

1. Job generates `TrialConfig` objects from dataset + agent + model combinations
2. Trials run in parallel (controlled by `--n-concurrent` / `-n`)
3. Each trial: build container -> upload task files -> run agent -> run verifier -> collect reward
4. Reward is read from `/logs/verifier/reward.txt` (single metric) or `/logs/verifier/reward.json` (multi-metric)
5. Artifacts collected from sandbox post-trial

### Reward System

Test scripts write rewards to `/logs/verifier/`:

```bash
#!/bin/bash
uvx pytest /tests/test.py
if [ $? -eq 0 ]; then
  echo 1 > /logs/verifier/reward.txt
else
  echo 0 > /logs/verifier/reward.txt
fi
```

**reward.txt**: plain text, single float (e.g., "1" or "0.95")
**reward.json**: JSON object with multiple float/integer metrics (e.g., `{"accuracy": 0.9, "runtime_sec": 12.5}`)

### Job Configuration

Run via CLI flags:
```bash
harbor run -d "terminal-bench@2.0" -a "claude-code" -m "anthropic/claude-opus-4-1" -n 4
```

Or via YAML config:
```bash
harbor run -c "<path/to/job.yaml>"
```

### Agent Trajectory Format (ATIF)

Standardized interchange format for agent execution logs -- enables training integration (RL, SFT).

---

## 5. Daytona Cloud Deployment

Daytona (https://www.daytona.io/) provides secure infrastructure for running AI-generated code. Harbor uses it for parallel cloud execution.

### Setup

```bash
export DAYTONA_API_KEY="your-key-here"
```

### Running on Daytona

```bash
harbor run -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5 \
  --env daytona \
  -n 32
```

Key differences from local Docker:
- `--env daytona` flag redirects execution to Daytona cloud
- `-n` can scale to 100+ concurrent containers
- Each trial gets its own cloud sandbox
- No local Docker daemon required for execution

### Other Cloud Providers

Same pattern applies:
- `--env modal` for Modal
- `--env e2b` for E2B
- Custom providers implement `BaseEnvironment`

---

## 6. The upload_dir Bug

### The Problem

Harbor's `upload_dir` function uses `docker cp` to transfer directories into containers. `docker cp` has context-dependent behavior:

- If `/target` does NOT exist: copies contents into `/target` (correct)
- If `/target` ALREADY exists: copies `source` as a subdirectory `/target/source/` (WRONG)

### Impact

When an agent creates a `/tests` directory during task execution, the subsequent test file upload ends up at `/tests/tests/test.sh` instead of `/tests/test.sh`, causing **verifier failures**.

### The Fix

Modify Harbor's `upload_dir` function to append `/.` to the source path:

```python
# Original (buggy):
["cp", str(source_dir), f"main:{target_dir}"]

# Fixed:
source = str(source_dir).rstrip('/') + '/.'
["cp", source, f"main:{target_dir}"]
```

The `/.` suffix forces Docker to copy directory **contents** rather than the directory itself.

### Verification

```python
python -c "from harbor.environments.docker.docker import DockerEnvironment; \
import inspect; print('PATCHED' if 'rstrip' in \
inspect.getsource(DockerEnvironment.upload_dir) else 'NOT PATCHED')"
```

### Important

This patch must be applied BEFORE running evaluations. The bug is documented in the pi-terminal-bench repo.

---

## 7. How Harbor Relates to Benchmarks

Harbor is the **official harness** for Terminal-Bench 2.0 and serves as a universal evaluation framework for many benchmarks.

### Terminal-Bench 2.0

- 89 tasks for testing agents in terminal environments
- Distributed via Harbor registry: `terminal-bench@2.0`
- Also available on HuggingFace: `harborframework/terminal-bench-2.0`
- Terminal-Bench Pro: 200 extended real-world terminal tasks

```bash
harbor run -d terminal-bench@2.0 -a claude-code -m anthropic/claude-opus-4-1 -n 4
```

### SWE-bench

- `swebench-verified@1.0` -- 500 human-validated tasks
- `swebenchpro@1.0` -- 731 multi-language instances (Python, JS/TS, Go)

### Other Integrated Benchmarks (selected)

| Benchmark | Tasks | Domain |
|-----------|-------|--------|
| terminal-bench@2.0 | 89 | Terminal agent evaluation |
| terminal-bench-pro@1.0 | 200 | Extended terminal tasks |
| swebench-verified@1.0 | 500 | Software engineering |
| swebenchpro@1.0 | 731 | Multi-language SWE |
| featurebench@1.0 | 200 | Feature implementation |
| code-contests@1.0 | 9,644 | Competitive programming |
| aider-polyglot@1.0 | 225 | Multi-language code editing |
| kumo@1.0 | 5,300 | General agent tasks |
| gaia@1.0 | 165 | Multi-step reasoning |
| simpleqa@1.0 | 4,326 | Factuality evaluation |
| medagentbench@1.0 | 300 | Healthcare clinical tasks |
| bfcl@1.0 | 3,641 | Function calling |
| arc_agi_2@1.0 | 167 | Abstract reasoning |
| binary-audit@1.0 | 46 | Security (binary backdoors) |
| crustbench@1.0 | 100 | C-to-Rust transpilation |
| vmax-tasks@1.0 | 1,043 | JavaScript bug fixing |
| usaco@2.0 | 304 | Competitive programming |

### Adapter System

Adapters translate original benchmark formats into Harbor's unified task format:

1. Parse original benchmark tasks
2. Generate Harbor task directories (instruction.md, Dockerfile, test.sh, etc.)
3. Validate with oracle agent (100% reward expected)
4. Run parity experiments vs original benchmark
5. Register in Harbor registry

Required adapter files:
- `adapter.py` -- core translation logic
- `run_adapter.py` -- entry point
- `adapter_metadata.json` -- metadata about conversion
- `parity_experiment.json` -- parity validation results

---

## 8. Registry System

### Architecture

The registry is a JSON file mapping dataset names + versions to git repositories containing tasks.

### Dataset Entry Format

```json
{
    "name": "my-dataset",
    "version": "1.0",
    "description": "A description of the dataset",
    "tasks": [
        {
            "name": "task-1",
            "git_url": "https://github.com/my-org/my-dataset.git",
            "git_commit_id": "1234567890",
            "path": "task-1"
        }
    ]
}
```

Key property: datasets can contain tasks from **multiple repositories**, enabling composition.

### Registry Types

| Type | Usage |
|------|-------|
| Default (built-in) | `registry.json` in Harbor repo root |
| Custom local | `--registry-path "<path/to/registry.json>"` |
| Custom remote | `--registry-url "<url/to/registry.json>"` |

### CLI Commands

```bash
harbor datasets list                          # List available datasets
harbor run -d "terminal-bench@2.0" ...        # Use registry dataset by name@version
harbor run -p "<path/to/local/dataset>" ...   # Use local dataset by path
```

### Dataset Composition

A dataset is a collection of tasks. Tasks are referenced by:
- Git URL (repository containing the task)
- Git commit ID (for reproducibility)
- Path within the repository

This allows a single dataset to aggregate tasks from multiple repos, and the same task to appear in multiple datasets.

### Adding to Registry

1. Create tasks in Harbor task format
2. Push to a git repository
3. Add entries to `registry.json` (in harbor-datasets or custom registry)
4. Each entry pins a specific commit hash for reproducibility

---

## 9. Integration Ecosystem

### Training / Optimization

- **SkyRL** -- RL optimization framework integration
- **GEPA** -- agent improvement framework
- ATIF format enables SFT trace generation from trial trajectories

### Monitoring

- **LiteLLM** integration documented
- **Opik** (Comet) integration for evaluation observability

### Leaderboard Submission (Terminal-Bench)

```bash
harbor run -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5 \
  --k 5 \
  --jobs-dir "./pi-tbench-results"
```

Results emailed to mchlmerrill@gmail.com or alex@laude.org for leaderboard integration.

---

## 10. Quick Reference: Running pi on Terminal-Bench

From the pi-terminal-bench repo:

```bash
# Install Harbor
uv tool install harbor

# Setup pi adapter
cd ~/workspaces/pi-terminal-bench
uv venv && source .venv/bin/activate
uv pip install -e ".[dev]"

# IMPORTANT: Apply upload_dir patch first!

# Local execution (4 concurrent)
harbor run -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5 \
  -n 4

# Cloud execution via Daytona (32 concurrent)
export DAYTONA_API_KEY="..."
harbor run -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5 \
  --env daytona \
  -n 32

# Single task testing
harbor run -d terminal-bench@2.0 \
  --agent-import-path pi_terminal_bench:PiAgent \
  -m anthropic/claude-sonnet-4-5 \
  --task-ids <task-id>
```

---

## Sources

- [Harbor GitHub Repository](https://github.com/harbor-framework/harbor)
- [Harbor Documentation](https://harborframework.com/docs)
- [Harbor Registry](https://harborframework.com/registry)
- [Harbor Core Concepts](https://harborframework.com/docs/core-concepts)
- [Harbor Task Structure](https://harborframework.com/docs/tasks)
- [Harbor Agent Interface](https://harborframework.com/docs/agents)
- [Harbor Adapters](https://harborframework.com/docs/adapters)
- [Harbor Datasets](https://harborframework.com/docs/datasets)
- [pi-terminal-bench Adapter](https://github.com/badlogic/pi-terminal-bench)
- [Terminal-Bench 2.0 Announcement](https://www.tbench.ai/news/announcement-2-0)
- [Terminal-Bench Paper (arXiv)](https://arxiv.org/html/2601.11868v1)
- [VentureBeat Coverage](https://venturebeat.com/ai/terminal-bench-2-0-launches-alongside-harbor-a-new-framework-for-testing)
- [CompileBench Migration to Harbor](https://quesma.com/blog/compilebench-in-harbor/)
- [LangChain Deep Agents Evaluation](https://blog.langchain.com/evaluating-deepagents-cli-on-terminal-bench-2-0/)
