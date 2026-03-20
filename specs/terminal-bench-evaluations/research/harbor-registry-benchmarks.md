# Harbor Registry & Benchmarks Research

Research date: 2026-03-19

## 1. Harbor Framework Overview

Harbor is an evaluation framework created by the Terminal-Bench team (Laude Institute) for assessing and optimizing AI agents and language models. It provides a unified harness for running diverse benchmarks against arbitrary agents.

- **Repository**: https://github.com/harbor-framework/harbor
- **Website**: https://harborframework.com
- **Registry**: https://harborframework.com/registry
- **Install**: `uv tool install harbor` or `pip install harbor`
- **Stack**: Python (87%), TypeScript (6%), Shell (5%), Dockerfile (1%)
- **Released**: November 7, 2025 (alongside Terminal-Bench 2.0)

### Core Capabilities

1. **Agent Evaluation** -- test agents like Claude Code, OpenHands, Codex CLI, and custom agents
2. **Benchmark Development** -- create and share custom evaluation environments
3. **Distributed Execution** -- run thousands of parallel experiments via Daytona and Modal
4. **RL Optimization** -- generate rollouts for reinforcement learning and SFT workflows

### Basic Usage

```bash
export ANTHROPIC_API_KEY=<key>
harbor run --dataset terminal-bench@2.0 \
  --agent claude-code \
  --model anthropic/claude-opus-4-1 \
  --n-concurrent 4
```

Cloud execution (Daytona):
```bash
export DAYTONA_API_KEY=<key>
harbor run -d terminal-bench@2.0 -m anthropic/claude-haiku-4-5 \
  -a claude-code --env daytona -n 32
```

---

## 2. Harbor Registry Structure

The registry is a centralized JSON-based catalog of 70+ datasets/benchmarks. Datasets follow a `name@version` naming convention.

### Dataset Types

- **Registry datasets**: Stored in Git repositories, referenced by name and version
- **Local datasets**: Directories on disk, referenced by path
- **Custom registries**: Point to a local or remote `registry.json` file

### Dataset JSON Structure

```json
{
    "name": "my-dataset",
    "version": "1.0",
    "description": "A description",
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

### Task Directory Structure

Each task is a self-contained directory:

```
task-id/
  task.toml          # metadata (author, labels)
  instruction.md     # prompt delivered to agents
  environment/       # Dockerfile + input files
  solution/          # reference/oracle implementation (solve.sh)
  tests/             # test.sh or pytest scoring (writes reward to /logs/verifier/reward.txt)
```

### Adapter System

Adapters translate existing benchmarks into Harbor's standardized task format. Every adapter must identify four elements:

1. **Task Instructions** -- what agents see
2. **Environments** -- Docker containers, dependencies, file structures
3. **Tests** -- unit tests or LLM-as-Judge evaluation
4. **Solutions** -- oracle/reference solutions for validation

Adapters require parity verification: comparable performance between the original benchmark harness and the Harbor adapter.

### CLI Commands

```bash
harbor datasets list              # list available datasets
harbor run -d <name>@<version>    # run a registry dataset
harbor run -p <path>              # run a local dataset
```

---

## 3. Complete Registry Catalog (70+ datasets)

| Dataset | Version | Tasks | Description |
|---------|---------|------:|-------------|
| terminal-bench | 2.0 | 89 | Terminal environment agent tasks |
| terminal-bench-pro | 1.0 | 200 | Real-world terminal environment tasks |
| terminal-bench-sample | 2.0 | 10 | Terminal-Bench sample subset |
| swebench-verified | 1.0 | 500 | Human-validated SWE-bench tasks |
| swebenchpro | 1.0 | 731 | Multi-language SWE benchmark (Python, JS/TS, Go) |
| swe-gen-js | 1.0 | 1,000 | JavaScript/TypeScript bug fixes |
| swe-lancer-diamond | manager/all/ic | 265/463/198 | Software engineering task variants |
| swtbench-verified | 1.0 | 433 | Software testing code generation |
| swesmith | 1.0 | 100 | Synthetically generated engineering tasks |
| featurebench | 1.0 | 200 | Feature implementation across Python repos |
| featurebench-lite | 1.0 | 30 | Feature implementation (lite) |
| featurebench-modal | 1.0 | 200 | Feature implementation (Modal GPU) |
| featurebench-lite-modal | 1.0 | 30 | Feature implementation (lite, Modal GPU) |
| otel-bench | 1.0 | 26 | OpenTelemetry instrumentation tasks |
| compilebench | 1.0 | 15 | Build system and compilation challenges |
| bixbench-cli | 1.5 | 205 | Bioinformatics and computational biology |
| ade-bench | 1.0 | 48 | dbt/SQL data analytics engineering bugs |
| medagentbench | 1.0 | 300 | Clinical healthcare environment tasks |
| labbench | 1.0 | 181 | Scientific figure reasoning in biology |
| financeagent | public | 50 | Financial research and analysis |
| lawbench | 1.0 | 1,000 | Legal knowledge evaluation |
| dabstep | 1.0 | 450 | Data analysis with Python/pandas |
| ds-1000 | head | 1,000 | Data science code generation |
| bird-bench | parity | 150 | SQL database query tasks |
| spider2-dbt | 1.0 | 64 | Complex SQL code generation |
| gaia | 1.0 | 165 | Multi-step reasoning and tool use |
| simpleqa | 1.0 | 4,326 | Fact-seeking questions (OpenAI) |
| gpqa-diamond | 1.0 | 198 | Graduate-level multiple-choice questions |
| arc_agi_2 | 1.0 | 167 | Visual grid puzzles requiring rule inference |
| satbench | 1.0 | 2,100 | Boolean satisfiability logical reasoning |
| bfcl | 1.0 | 3,641 | Function calling evaluation |
| bfcl_parity | 1.0 | 123 | Function calling parity validation |
| bigcodebench-hard-complete | 1.0.0 | 145 | Challenging Python programming tasks |
| code-contests | 1.0 | 9,644 | Competitive programming (DeepMind) |
| deveval | 1.0 | 63 | Full software development lifecycle |
| evoeval | 1.0 | 100 | Evolved challenging Python tasks |
| livecodebench | 6.0 | 100 | Live code generation benchmark |
| humanevalfix | 1.0 | 164 | Python code repair tasks |
| aider-polyglot | 1.0 | 225 | Multi-language code editing |
| autocodebench | lite200 | 200 | Adapter-based code benchmark |
| usaco | 2.0 | 304 | USACO competition problems |
| aime | 1.0 | 60 | Competition mathematics |
| ineqmath | 1.0 | 100 | Mathematical reasoning and proof |
| algotune | 1.0 | 154 | Algorithm optimization with speedup scoring |
| crustbench | 1.0 | 100 | C-to-Rust transpilation |
| quixbugs | 1.0 | 80 | Multi-lingual program repair (Python/Java) |
| qcircuitbench | 1.0 | 28 | Quantum algorithm design |
| binary-audit | 1.0 | 46 | Backdoor detection in binaries |
| replicationbench | 1.0 | 90 | Computational reproducibility from papers |
| mlgym-bench | 1.0 | 12 | Machine learning across domains |
| sldbench | 1.0 | 8 | Scaling law discovery |
| codepde | 1.0 | 5 | PDE-focused scientific computing |
| mmau | 1.0 | 1,000 | Audio understanding with Q&A |
| mmmlu | parity | 150 | Multilingual multiple-choice questions |
| strongreject | parity | 150 | LLM safety and jailbreak resistance |
| kumo | 1.0/easy/hard/parity | varies | Multiple difficulty splits |
| termigen-environments | 1.0 | 3,566 | Docker environments for terminal agents |
| openthoughts-tblite | 2.0 | 100 | Difficulty-calibrated terminal tasks |
| seta-env | 1.0 | 1,376 | CAMEL SETA RL training environment |
| vmax-tasks | 1.0 | 1,043 | JavaScript bug-fixing from real projects |
| reasoning-gym | hard/easy | 288 each | Abstract reasoning benchmarks |
| hello-world | 1.0 | 1 | Simple example benchmark |

---

## 4. Terminal-Bench 2.0 on Harbor

### Overview

Terminal-Bench 2.0 is the flagship benchmark for Harbor, with 89 tasks testing AI agents in terminal environments. It is significantly harder than v1.0, with substantial manual and LM-assisted verification for task reliability.

- **Tasks**: 89 (v2.0), 10 (sample subset)
- **Leaderboard**: https://www.tbench.ai/leaderboard/terminal-bench/2.0
- **Dataset on HuggingFace**: harborframework/terminal-bench-2.0

### Running Terminal-Bench 2.0

```bash
# Oracle agent (validation)
harbor run -d terminal-bench@2.0 -a oracle

# Claude Code agent
harbor run -d terminal-bench@2.0 -a claude-code -m anthropic/claude-opus-4-1 -n 4

# Cloud deployment (Daytona)
harbor run -d terminal-bench@2.0 -m anthropic/claude-haiku-4-5 \
  -a claude-code --env daytona -n 32

# Single task validation
harbor run -d terminal-bench@2.0 --task-name "<task-pattern>" -a <agent>
```

### Key Features

- Docker-based containerized execution
- Cloud deployment via Daytona, Modal, E2B
- Support for custom agents (any agent installable in a container)
- Agent Trajectory Interchange Format (ATIF) for standardized logging
- Leaderboard submission via HuggingFace PRs

### Task Format

Each task provides:
- An instruction in `instruction.md`
- A Docker environment with all dependencies
- Test scripts that verify correctness
- Reference oracle solutions

### Known Issue

There is a documented bug in Harbor's file upload mechanism: when agents create directories during execution, subsequent file uploads can nest incorrectly. Patching instructions are available in the pi-terminal-bench repo.

---

## 5. SWE-bench Pro on Harbor

### Overview

SWE-bench Pro is a substantially harder benchmark than standard SWE-bench, designed for realistic enterprise-level software engineering problems. It is available in the Harbor registry as `swebenchpro@1.0`.

- **Registry name**: `swebenchpro`
- **Tasks**: 731 (public set), 1,865 total
- **Repositories**: 41 actively maintained repos (11 public, 12 held-out, 18 commercial)
- **Languages**: Python, JavaScript/TypeScript, Go
- **Paper**: https://arxiv.org/abs/2509.16941
- **Created by**: Scale AI

### Key Differences from Standard SWE-bench

| Aspect | SWE-bench Verified | SWE-bench Pro |
|--------|-------------------|---------------|
| Tasks | 500 | 1,865 (731 public) |
| Repos | ~12 Python repos | 41 repos, multi-language |
| Complexity | Single-file patches common | Avg 4.1 files per patch |
| Duration | Minutes to hours | Hours to days for a human |
| Focus | Bug fixes | Bug fixes + feature implementation |
| Top performance | ~81% resolve rate | ~46% resolve rate |

### Evaluation Methodology

A task is "resolved" when:
1. **Issue Resolution**: fail-to-pass tests now pass (bug fixed / feature implemented)
2. **No Regressions**: all pass-to-pass tests continue to pass

### Running on Harbor

```bash
harbor run -d swebenchpro@1.0 -a claude-code -m anthropic/claude-opus-4-1
```

The adapter is available at: https://github.com/laude-institute/harbor/tree/main/adapters/swebenchpro

### Integration with Inspect AI

```python
from inspect_harbor import swebenchpro
# Runs tasks with ReAct agent scaffold in Docker sandbox
```

---

## 6. OpenTelemetry Benchmark (OTelBench) on Harbor

### Overview

OTelBench evaluates whether AI agents can correctly add distributed tracing instrumentation to codebases using OpenTelemetry. Built by Quesma, it runs on the Harbor framework.

- **Registry name**: `otel-bench@1.0`
- **Tasks**: 23 (later expanded to 26 in registry)
- **Languages**: 11 (Go, Java, C++, Python, JavaScript, PHP, Ruby, Rust, Erlang, .NET, Swift)
- **Source**: https://github.com/QuesmaOrg/otel-bench
- **Leaderboard**: https://quesma.com/benchmarks/otel/

### What It Evaluates

Each task presents agents with a small microservice (~300 lines of code) and asks them to add OpenTelemetry tracing instrumentation. Evaluation verifies:

- Correct compilation
- Correct span names
- Parent-child trace relationships
- Valid trace IDs
- Proper context propagation

### Task Difficulty Tiers

1. **Simple instrumentation** (highest pass rates, e.g. cpp-simple: 76%)
2. **Microservices tracing** (moderate, 10-55% pass rates)
3. **Distributed context propagation** (hardest, 0-10% pass rates)

### Top Model Results (as of Jan 2026)

| Model | Pass Rate |
|-------|-----------|
| Claude Opus 4.5 | 29% |
| GPT-5.2 | 26% |
| Claude Sonnet 4.5 | 22% |
| Gemini 3 Flash Preview | 19% |
| Gemini 3 Pro Preview | 16% |

Overall pass rate across all models: 14%.

### Running on Harbor

```bash
harbor run -d otel-bench@1.0 -a <agent> -m <model>
```

### Cost

Full benchmark run (966 total runs = 23 tasks x 3 attempts x 14 models) cost $522 in LLM tokens.

---

## 7. pi-terminal-bench (Harbor Agent Adapter for Pi)

Repository: https://github.com/badlogic/pi-terminal-bench

This is a Harbor agent adapter that enables the **pi** coding agent to execute Terminal-Bench evaluations. Key details:

- Integration layer between pi and Terminal-Bench 2.0
- Docker-based execution using Harbor
- Supports multiple LLM providers (Anthropic, OpenAI, Google)
- Includes shell scripts for benchmark execution
- JavaScript utilities for result visualization and leaderboard comparison
- Documents a Harbor file-upload bug and patching instructions
- MIT licensed

---

## 8. Creating a Benchmark for Harbor (Adapter Workflow)

The 9-step process for migrating a benchmark to Harbor:

1. Understand the original benchmark format
2. Fork Harbor repo, develop adapter code in Python
3. Verify oracle solutions achieve 100% pass rate
4. Coordinate parity experiment strategy with Harbor team
5. Run parity experiments comparing original vs Harbor adapter
6. Document results in `parity_experiment.json`
7. Upload results to Harbor's HuggingFace parity experiments dataset
8. Register dataset in official Harbor registry with task-level Git URLs
9. Submit comprehensive README and PR

The key design principle: running a benchmark via Harbor must produce equivalent results to the original harness.

---

## Sources

- [Harbor Framework GitHub](https://github.com/harbor-framework/harbor)
- [Harbor Website](https://harborframework.com/)
- [Harbor Registry](https://harborframework.com/registry)
- [Harbor Datasets Documentation](https://harborframework.com/docs/datasets)
- [Harbor Adapters Documentation](https://harborframework.com/docs/datasets/adapters)
- [Running Terminal-Bench on Harbor](https://harborframework.com/docs/running-tbench)
- [Terminal-Bench 2.0 Announcement](https://www.tbench.ai/news/announcement-2-0)
- [Terminal-Bench Registry & Adapters Announcement](https://www.tbench.ai/news/registry-and-adapters)
- [Terminal-Bench Leaderboard](https://www.tbench.ai/leaderboard/terminal-bench/2.0)
- [SWE-Bench Pro Paper](https://arxiv.org/abs/2509.16941)
- [SWE-Bench Pro Leaderboard (Scale Labs)](https://labs.scale.com/leaderboard/swe_bench_pro_public)
- [OTelBench Blog Post](https://quesma.com/blog/introducing-otel-bench/)
- [OTelBench Leaderboard](https://quesma.com/benchmarks/otel/)
- [CompileBench Migration to Harbor](https://quesma.com/blog/compilebench-in-harbor/)
- [pi-terminal-bench](https://github.com/badlogic/pi-terminal-bench)
- [Harbor on liteLLM docs](https://docs.litellm.ai/docs/projects/Harbor)
- [How to Evaluate AI Agents: Introduction to Harbor (Tessl)](https://tessl.io/blog/how-to-evaluate-ai-agents-an-introduction-to-harbor/)
