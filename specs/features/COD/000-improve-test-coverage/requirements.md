# Requirements

## Questions & Answers

**Q1: What is the current state of testing in this project?**
A: The project has existing tests using Go's standard `testing` package. Current overall coverage is 58.0%.

**Q2: Which specific packages need improved test coverage?**
A: All internal packages (exclude main/cmd/pi).

**Q3: Are there any existing testing patterns?**
A: Using Go's standard testing package, exclude cmd/pi (main) from coverage targets.

## Summary of Current Coverage by Package

| Package | Coverage |
|---------|----------|
| cmd/pi | 0.0% (excluded) |
| internal/provider | 46.7% |
| internal/tui | 44.9% |
| internal/lsp | 58.2% |
| internal/tools | 63.8% |
| internal/cli | 67.2% |
| internal/subagent | 72.8% |
| internal/extension | 71.1% |
| internal/session | 74.4% |
| internal/rpc | 82.1% |
| internal/sop | 83.3% |
| internal/config | 87.5% |
| internal/agent | 89.7% |
| internal/logger | 94.1% |

**Total: 58.0%**