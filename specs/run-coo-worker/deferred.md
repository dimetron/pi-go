# Deferred — Finding 2: make the run tree a field lookup, not a heuristic

Specified here rather than built, because nothing is blocked on it today and it
touches the session store's persisted format. Finding 1 is what stops runs
completing; this makes the next investigation cheap instead of archaeological.

## Problem

`session.Meta` has ten fields. Reconstructing which workers belonged to which
coordinator required grouping sessions by `workDir` and inferring roles from
title prefixes — see `recomendations.md`, Finding 2. Fourteen of 63 sessions
sat in numerically-named worktrees (`.pi-go/tasks/948887880000`) with no
coordinator session sharing the directory, so they cannot be attributed to a
spec by any recorded field at all.

## Proposed shape

A nested optional block on `Meta`, mirroring the existing `PlanContext`
precedent (`internal/session/store.go:28`) so it stays absent for ordinary
interactive sessions:

```go
// AgentContext records a session's place in a /run tree. Absent for
// interactive sessions.
type AgentContext struct {
    AgentID   string `json:"agentID,omitempty"`   // orchestrator's own ID
    AgentType string `json:"agentType,omitempty"` // task | worker | quick-task | code-reviewer
    ParentID  string `json:"parentSessionID,omitempty"`
    SpecName  string `json:"specName,omitempty"`
    Slice     int    `json:"slice,omitempty"`
    Cycle     int    `json:"cycle,omitempty"`     // /run retry index
    Worktree  string `json:"worktree,omitempty"`
    Branch    string `json:"branch,omitempty"`
    Status    string `json:"status,omitempty"`    // terminal status
}
```

## How it gets populated

The spawner already builds the child environment and now rewrites one variable
in it (`ChildEnv`). The same channel carries this: the parent sets
`PI_AGENT_ID`, `PI_PARENT_SESSION`, `PI_AGENT_TYPE`, `PI_SPEC_NAME`,
`PI_RUN_CYCLE`; the child reads them when it creates its session and stores
them on `Meta.Agent`.

`Status` is the exception — it is only known when the run ends, so it is
written by whoever observes termination rather than at session creation.

## Why it is worth doing

Every question asked during the investigation becomes a field lookup:

| Question | Today | With this |
|---|---|---|
| Which coordinator owns this worker? | group by `workDir`, guess | `parentSessionID` |
| What is this session? | guess from title prefix | `agentType` |
| Which spec/slice? | regex the title prose | `specName`, `slice` |
| Is this a retry? | match prompt text | `cycle` |
| Did it succeed? | scan `events.jsonl` for `ErrorCode` | `status` |

It would also make `session-stats`
(`internal/tools/session_stats.go`) able to report on runs rather than on
individual sessions.

## Risks

- **Format change.** Fields are additive and `omitempty`, so old sessions stay
  readable and new ones stay readable by old code. No migration needed.
- **Env surface.** Five new `PI_`-prefixed variables are forwarded to children.
  They carry no secrets, but they are inherited by *any* nested process, so a
  child must overwrite rather than inherit them — the same trap `ChildEnv` was
  written to avoid for the concurrency budget. Getting this wrong would make
  every grandchild claim the same parent.

## Not included

Linking sessions to the orchestrator's in-memory agent records at runtime. The
IDs above are enough to join after the fact, which is what the investigation
needed.
