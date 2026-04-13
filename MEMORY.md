# Pi-Go Palace Memory

## Architecture

```
Palace (pi-go)
├── Wings (Project Namespaces)
│   └── pi-go (19,196 drawers, 9 rooms)
│       ├── specs
│       ├── research
│       ├── tools
│       └── sessions
└── Knowledge Graph
    ├── Entities: 0
    └── Triples: 0 (0 active)
```

## Current State

| Metric | Value |
|--------|-------|
| **Total Drawers** | 19,196 |
| **Wings** | 1 |
| **Rooms** | 9 |
| **Embedding Model** | not loaded (FTS5 fallback) |

## Rooms

| Room | Purpose |
|------|---------|
| specs | Specification documents and design decisions |
| research | Codebase research and analysis |
| tools | Tool implementations and extensions |
| sessions | Session history and logs |
| agent | Agent configuration and behavior |
| config | Configuration management |
| tests | Test coverage and quality |
| cli | Command-line interface |
| tui | Terminal UI components |

## Memory System Commands

```bash
# Status overview
pi memory status

# Search drawers
pi memory search "query"

# Recent observations
pi memory recent --limit 10

# Wake-up (L0+L1 context)
pi memory wake-up

# Knowledge graph
pi memory kg query "entity"

# Initialize new palace
pi memory init <path>

# Download embedding model
pi memory model download
```

## Test Coverage

| Function | Coverage |
|----------|----------|
| `newMemoryCmd` | 100% |
| `scanRoomCandidates` | 92.3% |
| `runMemoryStatus` | 90.0% |
| `runMemoryModelStatus` | 90.0% |
| `runMemoryRecent` | 82.8% |
| `runMemoryMine` | 84.8% |
| `runMemoryWakeUp` | 81.2% |
| `runMemorySearch` | 81.0% |
| `runMemoryKGQuery` | 82.6% |
| `runMemoryInit` | 79.2% |

**Overall CLI package coverage**: 59%

## Notes

- Model not loaded → semantic search uses FTS5 keyword fallback
- Knowledge graph is empty (mining not yet run)
- Run `pi memory mine` to extract entities/triples from observations
