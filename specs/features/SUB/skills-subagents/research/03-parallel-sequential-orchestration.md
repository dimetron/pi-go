# Parallel & Sequential Agent Orchestration

## Key Question: Can the main agent dynamically orchestrate subagents?

### Current Reality: Tool Calls Are Sequential

The ADK v0.6.0 runner processes tool calls **sequentially**:

```
LLM response → tool_call_1 → execute → result → tool_call_2 → execute → result → ...
```

Each `agentHandler` call **blocks** on `for ev := range events` until the subagent completes. If the LLM sends 3 agent tool calls in one response, they execute one after another — not in parallel.

### Infrastructure IS Parallel-Ready

The underlying infrastructure supports true concurrency:

| Component | Concurrent? | Details |
|-----------|-------------|---------|
| Orchestrator.Spawn() | Yes | Minimal locking, no serialization |
| Pool.Acquire() | Yes | Multiple pending calls supported |
| Spawner.Spawn() | Yes | No concurrency limits in spawner |
| WorktreeManager | Yes | Mutex-protected but non-blocking |
| TUI event streaming | Yes | activeTools map tracks multiple agents |

### The Bottleneck

The bottleneck is at the **ADK tool execution layer** — each tool call blocks until it returns a result. The `agentHandler` in `tools/agent.go` blocks on event consumption (line 75).

### Background Field (Unused)

`AgentInput.Background bool` exists in types.go but is **never read or acted upon**. This was apparently planned but not implemented.

## Options for True Parallel/Chain Execution

### Option A: Parallel/Chain as a New Tool (pi-superpowers-plus approach)

Create a **single `subagent` tool** that internally handles parallelism:

```go
type SubagentInput struct {
    // Single mode
    Agent string `json:"agent,omitempty"`
    Task  string `json:"task,omitempty"`

    // Parallel mode
    Tasks []TaskItem `json:"tasks,omitempty"`

    // Chain mode
    Chain []ChainItem `json:"chain,omitempty"`
}
```

**One tool call → internally spawns N agents → waits for all → returns aggregated result.**

This is how pi-superpowers-plus does it. The LLM makes ONE tool call, and the tool handler manages concurrency internally using goroutines + WaitGroup/errgroup.

**Pros:**
- Works within ADK's sequential tool model
- Single tool call = single blocking handler that does parallel work internally
- Clean aggregated result back to LLM
- LLM doesn't need to manage concurrency

**Cons:**
- LLM can't react to individual agent results mid-execution
- All-or-nothing for parallel mode

### Option B: Background Mode + Poll

Implement the existing `Background` field:
1. Agent tool returns immediately with agentID
2. Add a "check_agent" tool to poll status
3. LLM can spawn multiple agents, then poll for results

**Pros:**
- LLM can manage concurrency dynamically
- Can react to individual completions

**Cons:**
- More complex LLM orchestration
- Polling overhead
- LLMs are bad at managing async state

### Option C: Enhanced ADK Runner (Custom)

Fork or wrap the ADK runner to support parallel tool execution when tool calls are independent.

**Pros:**
- True parallelism at the tool level
- Works for all tools, not just agents

**Cons:**
- Significant engineering effort
- May break ADK assumptions
- Harder to maintain

## Recommendation: Option A

The pi-superpowers-plus approach (Option A) is the right choice:

1. **Proven pattern** — pi-superpowers-plus has validated this in production
2. **Minimal changes** — works within existing ADK sequential model
3. **Clean API** — single tool call with mode selection
4. **Already architected** — orchestrator, pool, spawner all support concurrent operations
5. **Implementation path clear** — replace `agentHandler` with a richer handler that uses goroutines internally

### Implementation Sketch

```go
func subagentHandler(orch *subagent.Orchestrator, onEvent AgentEventCallback) {
    // Determine mode from input
    switch {
    case input.Tasks != nil:
        // Parallel: spawn all, collect results via errgroup
        g, ctx := errgroup.WithContext(ctx)
        results := make([]Result, len(input.Tasks))
        for i, task := range input.Tasks {
            g.Go(func() error {
                events, id, err := orch.Spawn(ctx, AgentInput{...})
                // consume events, accumulate result
                results[i] = collectResult(events)
                return nil
            })
        }
        g.Wait()
        // Return aggregated results

    case input.Chain != nil:
        // Chain: spawn sequentially, pipe {previous} output
        var previousOutput string
        for _, step := range input.Chain {
            task := strings.Replace(step.Task, "{previous}", previousOutput, -1)
            events, id, err := orch.Spawn(ctx, AgentInput{...})
            previousOutput = collectResult(events)
        }
        // Return final output

    default:
        // Single: existing behavior
        events, id, err := orch.Spawn(ctx, AgentInput{...})
        // ...
    }
}
```

## TUI Considerations

For parallel mode, the TUI needs:
- **Group display** — show all agents in a parallel batch as a group
- **Status per agent** — running/done/failed indicators
- **Aggregated result** — summary when all complete

For chain mode:
- **Step progression** — show current step N of M
- **Output piping** — visualize data flow between steps
- **Failure point** — clearly show where chain broke
