# Terminal-Bench 2.0 Research

Research date: 2026-03-19

## 1. What is Terminal-Bench?

Terminal-Bench is a standardized benchmark for evaluating AI agents on hard, realistic tasks in command-line interface (CLI) environments. It is a joint project between **Stanford University** and the **Laude Institute**.

- Published as a conference paper at **ICLR 2026**
- ArXiv paper: "Terminal-Bench: Benchmarking Agents on Hard, Realistic Tasks in Command Line Interfaces" (2601.11868), 85+ authors led by Mike A. Merrill, with Nicholas Carlini, Ludwig Schmidt, Alex Dimakis and others
- Website: https://www.tbench.ai
- GitHub: https://github.com/laude-institute/terminal-bench
- License: Apache 2.0

**What it evaluates:** How well AI agents can handle real-world, end-to-end tasks autonomously in terminal environments -- compiling code, training ML models, setting up servers, debugging systems, reverse engineering binaries, configuring infrastructure, data processing, and more.

## 2. Task Format

Each Terminal-Bench task consists of:

| Component | Description |
|-----------|-------------|
| **Instruction** | English-language description of what the agent must accomplish |
| **Docker image** | Unique containerized environment for the task |
| **Test script** | Pytest-based verification suite that validates successful completion |
| **Reference solution** | Human-written example solution demonstrating task resolution |
| **Time limit** | Maximum time allowed for the agent to complete the task |

### Task categories (examples from the benchmark):

- **Software engineering**: Building Linux kernel from source with QEMU, configuring git servers
- **Machine learning**: Training fastText models with size constraints, caching ML models offline
- **Security**: Cracking 7z file hashes, reverse engineering binaries
- **Data science**: Resharding large datasets
- **System administration**: Creating self-signed SSL certificates, installing Windows XP in QEMU with VNC
- **Game playing**: Completing Zork

### How tasks work:

Tasks are **interactive**. Once the instruction and Docker container are provided to an agent, it must explore and manipulate the environment by calling tools (e.g., editing files, running Bash commands) to complete the task. The agent operates autonomously within the sandboxed container.

Tasks are located in the `/tasks` folder in the repository. Each task includes a Dockerfile specification, test functions (with weights for partial credit in RL scenarios), and optional additional files.

## 3. Scoring Criteria and Methodology

- **Metric**: Pass@1 -- the model gets a single attempt at each task
- **Pass condition**: The agent must pass **all pytests** to receive credit for a task
- **Overall score**: Task resolution rate (percentage of tasks successfully completed out of 89)
- **Standard error**: Reported alongside accuracy for statistical rigor
- **No partial credit** on the official leaderboard (binary pass/fail per task)

### Additional metrics tracked:

- Token usage (input, reasoning, and answer tokens)
- Evaluation costs (USD)
- Release dates for trend analysis

### For RL training scenarios (community):

Some community projects use weighted scoring:
- 65% weight: Answer verification through Python unit tests (partial credit possible)
- 35% weight: LLM-as-a-Judge evaluation (assessing planning, tool use, workflow adherence)

## 4. Public Leaderboard at tbench.ai

### Terminal-Bench 2.0 leaderboard (as of March 2026):

**Top agents (tbench.ai):**

| Rank | Agent | Model | Accuracy |
|------|-------|-------|----------|
| 1 | ForgeCode | Claude Opus 4.6 | ~82% |
| 2 | ForgeCode | GPT-5.4 | ~82% |
| 3 | TongAgents | Gemini 3.1 Pro | ~80% |
| 4 | ForgeCode | Gemini 3.1 Pro | ~78% |
| 5 | SageAgent | GPT-5.3-Codex | ~78% |

**Artificial Analysis independent evaluation (Terminal-Bench Hard subset):**

| Rank | Model | Score |
|------|-------|-------|
| 1 | GPT-5.4 (xhigh) | 57.6% |
| 2 | Gemini 3.1 Pro Preview | 53.8% |
| 3 | GPT-5.3 Codex (xhigh) | 53.0% |

Note: The tbench.ai leaderboard includes custom agent scaffolds (which boost performance significantly), while Artificial Analysis tests raw models with a standard harness, explaining the score differences.

**Organizations represented**: Anthropic, OpenAI, Google, Meta, and numerous AI startups. The leaderboard includes 26+ models from 316+ available.

### Notable community results:

- Terminal-Agent-Qwen3-32b (RL-trained): 13.75% -- top Qwen3 agent, outperforming DeepSeek R1 and GPT-4.1 with Codex
- Apex2-Terminal-Bench-Agent: Reached #1 at one point (open-source insights shared on GitHub)

## 5. How to Submit Results

### Requirements:

- Must evaluate on dataset: `terminal-bench-core@0.1.1`
- Must use default agent and test timeouts
- Pass@1 evaluation (single attempt per task)

### Two submission pathways:

**A. Custom Agent:**

```bash
# Install
uv tool install terminal-bench
# or: pip install terminal-bench

# Run evaluation
tb run \
    --agent-import-path path.to.agent:ClassName \
    --dataset-name terminal-bench-core \
    --dataset-version 0.1.1 \
    --n-concurrent 8
```

**B. Quick Model Evaluation (using built-in Terminus agent):**

```bash
# No repo clone needed, just uv installed
tb run \
    --agent terminus \
    --model anthropic/claude-3-7-latest \
    --dataset-name terminal-bench-core \
    --dataset-version 0.1.1 \
    --n-concurrent 8
```

### Submission process:

1. Run evaluation with the required dataset version
2. Submit results via PR to the HuggingFace leaderboard repo: https://huggingface.co/datasets/alexgshaw/terminal-bench-2-leaderboard
3. OR email results to:
   - mikeam@cs.stanford.edu
   - alex@laude.org
4. Results are integrated into the public leaderboard at https://www.tbench.ai/leaderboard

## 6. Terminal-Bench 2.0 vs Previous Versions

### Version history:

| Version | Tasks | Status | Notes |
|---------|-------|--------|-------|
| 1.0 | 80 | Launched May 2025 | Original benchmark |
| 2.0 | 89 | Current (Nov 2025) | Harder, better verified |
| 3.0 | TBD | In development | Next generation |
| Science | TBD | In development | Domain-specific for scientific computing |

### What changed in 2.0:

1. **Harder tasks**: Substantially more challenging, maintaining a ~50% performance ceiling where there is clear room for improvement
2. **Better verification**: Several hours of manual and LLM-assisted validation per task. Community had discovered fragile tasks in v1 (e.g., `download-youtube` broke due to YouTube's anti-bot changes)
3. **More tasks**: 89 tasks (up from 80)
4. **Quality**: Labs report these are "some of the highest quality environments they have seen"
5. **Harbor framework**: New execution harness replacing the original, supporting:
   - Cloud-deployed containers (horizontal scaling to thousands)
   - RL and SFT rollout interfaces for agent training
   - Framework-agnostic design (works with any agent installable in a container)
   - Daytona cloud environment support

### Harbor framework commands (2.0):

```bash
# Run with oracle (reference solution)
harbor run -d terminal-bench@2.0 -a oracle

# Run with Claude Code on Daytona cloud
harbor run \
  -d terminal-bench@2.0 \
  -m anthropic/claude-haiku-4-5 \
  -a claude-code \
  --env daytona \
  -n 32
```

## 7. Documentation and Papers

### Primary paper:

- **Title**: "Terminal-Bench: Benchmarking Agents on Hard, Realistic Tasks in Command Line Interfaces"
- **ArXiv**: https://arxiv.org/abs/2601.11868
- **Published**: ICLR 2026 (conference paper)
- **PDF**: https://arxiv.org/pdf/2601.11868
- **OpenReview**: https://openreview.net/pdf/417ac3236de7dbf3fc3414c51754dd239271663e.pdf

### Documentation:

- Main docs: https://www.tbench.ai/docs
- Agent introduction: https://www.tbench.ai/docs/agent-introduction
- Leaderboard submission: https://www.tbench.ai/docs/submitting-to-leaderboard
- Task gallery: https://www.tbench.ai/tasks
- Harbor framework: https://harborframework.com/docs/running-tbench

### GitHub repositories:

- Main: https://github.com/laude-institute/terminal-bench
- Terminal-Bench 2.0: https://github.com/laude-institute/terminal-bench-2
- RL training: https://github.com/Danau5tin/terminal-bench-rl
- Apex2 agent: https://github.com/heartyguy/Apex2-Terminal-Bench-Agent

### Blog posts:

- Snorkel AI: https://snorkel.ai/blog/terminal-bench-2-0-raising-the-bar-for-ai-agent-evaluation/
- Snorkel AI (evaluating): https://snorkel.ai/blog/evaluating-coding-agent-capabilities-with-terminal-bench-snorkels-role-in-building-the-next-generation-benchmark/
- VentureBeat: https://venturebeat.com/ai/terminal-bench-2-0-launches-alongside-harbor-a-new-framework-for-testing
- Vals.ai: https://www.vals.ai/benchmarks/terminal-bench-2
- Artificial Analysis: https://artificialanalysis.ai/evaluations/terminalbench-hard

## 8. Agent Integration (Technical Details)

Three integration methods:

### A. AbstractInstalledAgent (simplest)

Install agent directly in task containers. Implement four methods:
- `name()` -- agent identifier
- `_env` -- environment variables (API keys)
- `_install_agent_script_path` -- path to installation script
- `_run_agent_commands()` -- commands to execute agent with task description

### B. BaseAgent Interface (more control)

Implement single method:
```python
perform_task(task_description, session, logging_dir) -> AgentResult
```
- `session`: tmux session for command execution
- Returns `AgentResult` with token counts and optional failure mode info

### C. MCPAgent Interface

Coming soon -- MCP-based agent integration.

### Running:

```bash
tb run --agent-import-path path.to.agent:ClassName --task-id hello-world
```

## 9. Key Takeaways

1. Terminal-Bench 2.0 is the current gold standard for evaluating coding/terminal agents, adopted by all major frontier labs
2. The benchmark is intentionally hard -- even top agents with custom scaffolding reach only ~82%, raw models ~57%
3. Tasks are realistic end-to-end problems, not synthetic puzzles
4. The Harbor framework enables scaled evaluation and RL training
5. 1,000+ GitHub stars, 100+ contributors, ICLR 2026 publication
6. Terminal-Bench 3.0 and Terminal-Bench Science are in development
7. Community is actively building RL-trained agents specifically for this benchmark
