# Rough Idea: Implement Evaluation Terminal-Bench

**Source:** Harbor Framework Registry - terminal-bench-pro@1.0

## Summary

Implement and integrate Terminal-Bench Pro (225 tasks) as an evaluation benchmark for the pi CLI agent. This benchmark tests autonomous AI agents in real Unix terminal environments, covering tasks from software compilation to system administration, data science, security, and scientific computing.

## Key Characteristics

- **225 distinct terminal-based tasks** across 11 categories
- **Evaluation harness:** Harbor Framework
- **Task format:** instruction.md + containerized environment + test.sh verification
- **Metrics:** Binary pass/fail based on test script exit code
- **Use case:** Evaluate pi CLI's capability to handle real-world end-to-end terminal tasks

## Motivation

The pi CLI currently lacks a standardized benchmark to measure its autonomous terminal interaction capabilities. Terminal-Bench Pro provides a rigorous, reproducible evaluation framework to:
1. Measure baseline performance
2. Compare against other agents (SWE-agent, Claude Code, etc.)
3. Track improvements over time
4. Identify weaknesses in specific task categories

## What Needs to Be Determined

- How to integrate Harbor Framework with pi-go
- Which subset of tasks to run (full 225 vs. subset)
- How to map pi CLI capabilities to benchmark requirements
- Infrastructure requirements (Docker, environment setup)