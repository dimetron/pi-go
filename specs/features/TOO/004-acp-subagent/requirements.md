# Requirements

## Questions & Answers

### Q1

What should “acp-subagent” do from the user’s perspective? Please describe the intended behavior, inputs, and outputs in
2–5 sentences.

### A1

Agent Client Protocol (ACP) subagent in Go

### Q2

What capabilities must this ACP subagent support in the first version? For example: discovery/registration, listing
tools, invoking tools, streaming responses, authentication, lifecycle management, cancellation, or something else.

### A2

all

### Q3

Where should this subagent run and how should it connect? For example: as a local subprocess over stdio, over
HTTP/WebSocket to a remote ACP server, or both.

### A3

local - take examples from https://github.com/coder/acp-go-sdk

### Q4

What is the relationship between the ACP subagent and the existing subagent system in this repo? Should it be:

1. a new subagent type selectable like existing agents,
2. an adapter that wraps existing subagents behind ACP,
3. both,
4. or a separate experimental path?

### A4

should be a regular subagent adapter via acp

### Q5

What constraints or non-goals should we enforce for the first version? For example: no remote transport, no auth, no
persistent sessions, no multi-agent orchestration, or strict compatibility with current subagent UX.

### A5

research ok bestpractices

### Q6

What should count as success for users? Please give 2–4 acceptance criteria, such as “an existing subagent can be
exposed over ACP and invoked locally” or “tool calls stream progress/errors back correctly.”

### A6

claude code or codex can be used over acp

### Q7

Does “Claude Code or Codex can be used over ACP” mean:

1. pi should act as an ACP server that those clients connect to,
2. pi should act as an ACP client to drive those tools,
3. both,
4. or whichever matches ACP best practices from research?

### A7

client

