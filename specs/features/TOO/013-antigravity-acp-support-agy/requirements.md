# Requirements

## Questions & Answers

### Q1: What does “Antigravity ACP support” mean in this task: should pi-go add a new ACP subagent adapter for Google Antigravity, or should it make existing ACP support compatible with Antigravity’s ACP implementation?

A1: pi-go should add a new ACP subagent adapter for Google Antigravity.

### Q2: How should users invoke/select the new Google Antigravity ACP subagent adapter — should it appear as a named subagent like `antigravity`, as a provider option for an existing `acp` subagent, or both?

A2: It should appear as a named subagent like `antigravity`.

### Q3: What is the blocker: do we not know the Antigravity ACP launch command/protocol details yet, or is Google Antigravity not currently exposing ACP support that pi-go can call?

A3: The feature appears blocked because `agy` does not have ACP options.

### Q4: Given that `agy` does not expose ACP options, what should this spec produce: a blocked/no-op spec, a fallback integration plan, or a placeholder adapter design?

A4: Research what can be done given the current blocker.

### Q5: Given the research findings, which path should the implementation take?

A5: Build an in-process proxy that presents ACP to pi-go while internally driving non-ACP `agy`, and register it as a subagent.

