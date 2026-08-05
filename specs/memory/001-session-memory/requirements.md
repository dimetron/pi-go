# Requirements: PiGoMemoryService

## Background

Reference: https://adk.dev/sessions/memory/ — Google's ADK defines a
`memory.Service` interface that ingests sessions into long-term, searchable
memory and exposes them via `SearchMemory` for use across user-scoped
sessions. ADK ships an `InMemoryService` reference implementation; this spec
defines a production-grade, persistent **`PiGoMemoryService`** that conforms
to the same `google.golang.org/adk/v2/memory.Service` interface.

## Interface (from ADK — non-negotiable contract)

```go
// google.golang.org/adk/v2/memory.Service
type Service interface {
    AddSessionToMemory(ctx context.Context, s session.Session) error
    SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

type SearchRequest  struct { Query, UserID, AppName string }
type SearchResponse struct { Memories []Entry }
type Entry struct {
    ID             string
    Content        *genai.Content
    Author         string
    Timestamp      time.Time
    CustomMetadata map[string]any
}
```

## Questions & Answers

### Q1 — Scope confirmation

**Q:** Is the goal to implement `PiGoMemoryService` — a persistent
implementation of `google.golang.org/adk/v2/memory.Service` — backed by
pi-go's existing `internal/memory` SQLite store (not the ADK-shipped
`InMemoryService`)? Ingest events from `session.Session`, make them
searchable by `(AppName, UserID, query)`, and wire it into `agent.Config`?
**A:** Yes — same idea as the ADK `InMemoryService` but a `PiGoMemoryService`
backed by the pi-go memory SQLite store (or a new sub-store inside it),
wirable via `agent.New(...)` and used across sessions.

### Q2 — Storage strategy

**Q:** Reuse existing `internal/memory` palace store, add a new sub-store,
wrap palace as backing, or something else?
**A:** d) Other (TBD — needs more discussion).

