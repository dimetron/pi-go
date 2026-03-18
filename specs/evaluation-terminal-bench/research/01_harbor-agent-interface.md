# Research: Harbor Framework Agent Interface

## Overview

The Harbor Framework provides a Python-based agent interface for evaluating autonomous AI agents on terminal-based benchmarks.

## Agent Interface Specification

### BaseAgent (External Agents)

Custom agents must inherit from `harbor.agents.base.BaseAgent` and implement:

| Method | Purpose |
|--------|---------|
| `name() -> str` | Unique identifier |
| `version() -> str | None` | Version string |
| `async setup(environment: BaseEnvironment)` | Prepare agent and tools in container |
| `async run(instruction: str, environment: BaseEnvironment)` | Main execution loop |
| `create_run_agent_commands(instruction: str) -> list[ExecInput]` | Translate instruction to commands |
| `populate_context_post_run(context: AgentContext)` | Fill ATIF trajectory data |

### ExecInput Structure

```python
class ExecInput(BaseModel):
    command: str              # bash command to execute
    cwd: str | None = None    # working directory
    env: dict | None = None   # environment variables
    timeout_sec: int | None = None  # per-command timeout
```

### CLI Usage

```bash
harbor run \
  --dataset terminal-bench-pro@1.0 \
  --agent-import-path my_package.my_module:MyAgent \
  --model gpt-4o-mini \
  --output-dir ./results
```

## Sources

- https://harborframework.com/docs/agents
- https://harborframework.com/docs/getting-started