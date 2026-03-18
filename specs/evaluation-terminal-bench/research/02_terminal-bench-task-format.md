# Research: Terminal-Bench Pro Task Format

## Task Structure

Each task contains:

```
<task-name>/
├── Dockerfile              # Container environment
├── instruction.md          # Natural language goal
├── task.yaml / task.toml   # Metadata and config
├── solution.sh             # Optional oracle solution
└── tests/
    └── test.sh             # Verification script (exit 0 = success)
```

## instruction.md

Markdown with:
- **Goal** - concise statement of what to achieve
- **Constraints** - time limits, prohibited actions
- **Artifacts** - files, env vars to produce

## task.yaml Fields

```yaml
descriptions:
  - key: base
    description: |
      Your goal is to …
author_email: your@email.com
difficulty: easy | medium | hard
tags: [tag1, tag2]
max_agent_timeout_sec: 180      # Agent time limit
max_test_timeout_sec: 30        # Test time limit
test_scripts:
  - setup-uv-pytest.sh
  - run-uv-pytest.sh
```

## tests/test.sh

Must exit 0 for success, non-zero for failure. Can write reward to `/logs/verifier/reward.txt`.

## Categories (225 tasks)

- Software Engineering (26)
- System Administration (9)
- Data Science (28)
- Security (62)
- Scientific Computing (142)
- File Operations (115)
- Debugging (62)
- Data Processing (95)
- Model Training (171)
- Mathematics/ML (7)
- Games/Video/Personal Assistant/Optimization/Data Querying

## Sources

- https://harborframework.com/registry
- https://huggingface.co/datasets/alibabagroup/terminal-bench-pro
- https://arxiv.org/html/2601.11868v1