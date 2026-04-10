# TUI Pipeline Visualization — State Machine Blocks

## Concept

Render active subagents as a visual pipeline in the TUI, using box-drawing characters to show agent state and data flow — similar to a DAG/state machine diagram in the terminal.

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│ implementer │───▶│ spec-review │───▶│ code-review │
│   ▶ running │    │   ○ pending │    │   ○ pending │
└─────────────┘    └─────────────┘    └─────────────┘
```

## Available Rendering Primitives

### Already used in pi-go TUI
- Box lines: `─` `│`
- Status: `▶` (running) `✓` (done) `✗` (failed) `◼` (cancelled)
- Icons: `●` `⚙` `→` `💭`
- Styling: lipgloss v2 with 256-color ANSI palette
- Green (35), Orange (214), Red (196), Blue (39), Purple (63)

### Box-drawing characters available
```
┌ ─ ┐    Corners and lines
│   │    Vertical
└ ─ ┘    Bottom corners
├ ┤ ┬ ┴  Tee connectors
───▶     Arrow (flow direction)
═══      Double line (emphasis)
```

## Design Proposals

### Parallel Mode — Side-by-Side Blocks

```
┌─ parallel (3 agents) ─────────────────────────────┐
│                                                     │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐       │
│  │ worker   │   │ worker   │   │ worker   │       │
│  │ ✓ 12.3s  │   │ ▶ 8.1s  │   │ ▶ 6.4s  │       │
│  │ 3 files  │   │ ⚙ edit   │   │ ⚙ bash  │       │
│  └──────────┘   └──────────┘   └──────────┘       │
│                                                     │
│  Progress: 1/3 done                                 │
└─────────────────────────────────────────────────────┘
```

State colors:
- `○ pending` — dim gray
- `▶ running` — orange/yellow
- `✓ done` — green
- `✗ failed` — red

### Chain Mode — Sequential Flow

```
┌─ chain (3 steps) ──────────────────────────────────┐
│                                                     │
│  ┌────────────┐     ┌────────────┐     ┌──────────┐│
│  │ implementer│────▶│ spec-review│────▶│ code-rev ││
│  │ ✓ 45.2s   │     │ ▶ 12.1s   │     │ ○ pending││
│  └────────────┘     └────────────┘     └──────────┘│
│                                                     │
│  Step 2/3: spec-review                              │
└─────────────────────────────────────────────────────┘
```

### Single Mode — Current Behavior Enhanced

```
┌──────────────┐
│ implementer  │
│ ▶ 23.4s      │
│ ⚙ edit main  │
│ 2 files, 1t  │
└──────────────┘
```

### Compact Mode (for Status Bar)

When space is limited, degrade to inline:

```
agents: [✓ impl] [▶ spec-rev] [○ code-rev]  chain 2/3
```

Or for parallel:
```
agents: [✓ w1] [▶ w2] [▶ w3]  parallel 1/3
```

## Adaptive Layout Strategy

### Width Detection

```
Terminal width ≥ 100 → Full box layout (side-by-side for parallel)
Terminal width ≥ 60  → Stacked boxes (one per line)
Terminal width < 60  → Compact inline status
```

### When to Show Pipeline

1. **During execution** — live view with real-time state updates
2. **On completion** — summary with timing and results
3. **In status bar** — always show compact progress

### Rendering Location

Two options:

**Option A: Inline with messages** (recommended)
- Pipeline renders as part of the tool result message
- Updates in-place as agents progress
- Consistent with current agent event rendering pattern

**Option B: Dedicated panel**
- Fixed area above status bar
- Always visible during execution
- More complex layout management

## Implementation Approach

### Data Model

```go
type PipelineView struct {
    Mode    string        // "single", "parallel", "chain"
    Agents  []AgentBlock  // Ordered list of agents
    StartAt time.Time
}

type AgentBlock struct {
    Name     string       // Agent name (e.g., "implementer")
    State    AgentState   // pending, running, done, failed
    Duration time.Duration
    CurrentTool string    // Currently executing tool
    FilesChanged int
    Events   []agentEv    // Event history
}

type AgentState int
const (
    AgentPending AgentState = iota
    AgentRunning
    AgentDone
    AgentFailed
)
```

### Rendering Function

```go
func renderPipeline(p PipelineView, width int) string {
    switch {
    case width >= 100:
        return renderPipelineBoxes(p)
    case width >= 60:
        return renderPipelineStacked(p)
    default:
        return renderPipelineCompact(p)
    }
}
```

### State Machine Transitions

```
                  spawn
  ○ pending ────────────▶ ▶ running
                              │
                    ┌─────────┼─────────┐
                    │         │         │
                 success   error    cancel
                    │         │         │
                    ▼         ▼         ▼
                 ✓ done    ✗ failed  ◼ cancelled
```

## Color Scheme

| State | Icon | Color (256) | Hex Approx |
|-------|------|-------------|------------|
| Pending | ○ | 245 (dim gray) | #8a8a8a |
| Running | ▶ | 214 (orange) | #ffaf00 |
| Done | ✓ | 35 (green) | #00af5f |
| Failed | ✗ | 196 (red) | #ff0000 |
| Cancelled | ◼ | 243 (gray) | #767676 |

## Live Update Pattern

Agent events stream via existing `AgentSubEvent` channel. Extended with pipeline metadata:

```go
type AgentSubEvent struct {
    AgentID    string
    Kind       string
    Content    string
    // New fields for pipeline:
    PipelineID string // Groups agents in same pipeline
    Mode       string // "parallel", "chain"
    Step       int    // Position in chain (1-based)
    Total      int    // Total agents in pipeline
}
```

The TUI `Update()` loop uses PipelineID to group events and update the pipeline view as a whole, rather than individual messages.
