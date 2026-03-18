# Rough Idea: Terminal Bench Evaluations

## Source
https://github.com/badlogic/pi-terminal-bench

## Concept
Integrate pi-go with Terminal-Bench evaluations using the pi-terminal-bench adapter framework. Terminal-Bench is a standardized evaluation/benchmark framework for coding agents, with a public leaderboard at tbench.ai.

## Key Details from Reference Repo

**What pi-terminal-bench is:**
- A Harbor agent adapter for the pi coding agent to run Terminal-Bench evaluations
- Uses a `PiAgent` class to integrate pi with Harbor (agent evaluation framework)
- Supports Anthropic (Claude), OpenAI, and Google Gemini providers

**How it works:**
- Agents run against Terminal-Bench tasks in isolated Docker environments
- Harbor orchestrates task distribution and result collection
- A verifier validates test execution outcomes
- Results are structured in job directories, parsed by `show-results.js`

**Deployment options:**
- Local execution via Docker
- Cloud-based via Daytona (supports up to 32 concurrent tasks)

**Known issues:**
- Harbor's `upload_dir` function has a bug with `/tests` directory copying (creates nested paths), requiring a patch

**Results:**
- Job directories with structured results
- Rankings against Terminal-Bench 2.0 leaderboard
- Results can be submitted for official leaderboard inclusion
