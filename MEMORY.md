# Memory System

Persistent memory compression system for pi-go, inspired by [claude-mem](https://github.com/thedotmack/claude-mem), implemented natively in Go.

## Overview

pi-go's memory system captures tool usage patterns, compresses them via AI into structured observations, and retrieves relevant context at session start — all without bloating context windows.

## Architecture

```mermaid
graph TD
    subgraph Capture["1. Observation Capture"]
        adk["ADK Runner"]
        after_cb["AfterToolCallback"]
        queue["Buffered Channel<br/>(non-blocking)"]
        
        adk --> after_cb
        after_cb --> queue
    end

    subgraph Process["2. Background Processing"]
        queue --> worker["Worker Goroutine"]
        worker --> privacy["Privacy Filter"]
        privacy --> compressor["AI Compressor<br/>(memory-compressor subagent)"]
        compressor --> store["SQLite Store"]
    end

    subgraph Store2["3. Storage Layer (~/.pi-go/memory/)"]
        store --> db["claude-mem.db<br/>(WAL mode, FTS5)"]
        db --> sessions_t["sessions"]
        db --> obs_t["observations + FTS5"]
        db --> sum_t["session_summaries + FTS5"]
    end

    subgraph Retrieve["4. Retrieval & Context"]
        start["Session Start"]
        ctx_gen["ContextGenerator"]
        mem_search["mem-search tool"]
        mem_timeline["mem-timeline tool"]
        mem_get["mem-get tool"]
        
        start --> ctx_gen
        ctx_gen -->|"inject into system instruction"| adk
        mem_search --> db
        mem_timeline --> db
        mem_get --> db
    end

    style Capture fill:#1a2a1a,color:#fff
    style Process fill:#2a1a2a,color:#fff
    style Store2 fill:#1a1a2a,color:#fff
    style Retrieve fill:#1a2a3a,color:#fff
    style adk fill:#1a3a5c,color:#fff
    style compressor fill:#5c1a3a,color:#fff
    style db fill:#3a1a5c,color:#fff
```

## Component Details

### 1. Observation Capture

```mermaid
sequenceDiagram
    participant ADK as ADK Runner
    participant CB as AfterToolCallback
    participant W as Worker
    participant DB as SQLite

    ADK->>CB: tool call completed
    CB->>W: Enqueue(RawObservation)
    Note over CB,W: Non-blocking (drops if full)
    
    ADK->>ADK: continues immediately
    
    W->>W: privacy.StripPrivate()
    W->>CB: Spawn memory-compressor
    CB-->>W: compressed Observation
    W->>DB: InsertObservation()
```

- **AfterToolCallback** (`internal/memory/worker.go:187-205`): ADK callback that fires after every tool call
- **Non-blocking enqueue**: Prevents memory pipeline from slowing down the agent
- **Configurable exclusion**: `screen`, `restart` and other noisy tools can be excluded

### 2. Background Processing

```mermaid
graph LR
    subgraph Pipeline
        raw["RawObservation<br/>(tool name, args, result)"]
        priv["Privacy Filter<br/><private>→[PRIVATE]</private>"]
        ai["AI Compression<br/>(memory-compressor subagent)"]
        fallback["Fallback<br/>(if AI fails)"]
        obs["Structured Observation"]
    end

    raw --> priv
    priv --> ai
    ai -->|"success"| obs
    ai -->|"error"| fallback
    fallback --> obs

    style raw fill:#1a1a2a,color:#fff
    style priv fill:#3a2a1a,color:#fff
    style ai fill:#2a1a2a,color:#fff
    style obs fill:#1a3a1a,color:#fff
```

**Privacy Filtering** (`internal/memory/privacy.go`):
- Strips `<private>...</private>` tags from tool input/output
- Deep recursion into nested maps and arrays
- Prevents PII from entering long-term memory

**AI Compression** (`internal/memory/compress.go:36-72`):
- Spawns `memory-compressor` subagent (smol model)
- Receives: tool name, JSON args, JSON result (truncated to 4096 chars)
- Returns structured JSON: `{title, type, text, source_files}`

### 3. Storage Layer

**Database** (`internal/memory/db.go`):
- SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- WAL mode for concurrent reads
- FTS5 full-text search virtual tables

**Schema** (`internal/memory/db.go:15-113`):

```mermaid
graph TD
    subgraph Tables["Tables"]
        sessions["sessions<br/>id, session_id, project,<br/>started_at, status"]
        obs["observations<br/>id, session_id, project,<br/>title, type, text,<br/>source_files, tool_name,<br/>discovery_tokens, created_at"]
        sums["session_summaries<br/>id, session_id, project,<br/>request, investigated,<br/>learned, completed,<br/>next_steps"]
    end

    subgraph FTS["FTS5 Indexes"]
        obs_fts["observations_fts<br/>(title, text, source_files)"]
        sums_fts["session_summaries_fts<br/>(request, investigated,<br/>learned, completed, next_steps)"]
    end

    obs --> obs_fts
    sums --> sums_fts

    style Tables fill:#1a2a3a,color:#fff
    style FTS fill:#2a1a2a,color:#fff
```

**Observation Types** (`internal/memory/types.go:6-14`):
| Type | Description |
|------|-------------|
| `decision` | Architectural decisions |
| `bugfix` | Bug fixes |
| `feature` | New features |
| `refactor` | Refactoring |
| `discovery` | Codebase insights |
| `change` | General changes |

### 4. Context Retrieval

**Session Start** (`internal/memory/context.go:32-116`):
```mermaid
graph TD
    start["New Session"] --> ctx["ContextGenerator.Generate()"]
    ctx --> sum["RecentSummaries(3)"]
    ctx --> obs["RecentObservations(200)"]
    sum --> filter["Filter: last 72 hours"]
    obs --> filter
    filter --> group["Group by session_id"]
    group --> budget["Token budget check"]
    budget --> inject["Inject into system instruction"]
    
    style inject fill:#1a5c3a,color:#fff
```

**Output Format**:
```markdown
# [project-name] recent context, 2024-01-15 3:04pm MST

**Legend:** session-request | bugfix | feature | refactor | change | discovery

**Column Key**:
- **Read**: Tokens to read this observation (cost to learn it now)
- **Work**: Tokens spent on work that produced this record

## Session: User asked to fix auth bug (Jan 15 at 2:30 PM)

**internal/auth/login.go**

| ID | Time | T | Title | Read | Work |
|----|------|---|-------|------|------|
| #42 | 2:31 PM | bugfix | Auth token validation | 45 | 120 |
| #43 | 2:35 PM | bugfix | Fixed token expiry check | 38 | 85 |

Access past observations with mem-search, mem-timeline, mem-get tools.
```

## Search Tools

```mermaid
graph LR
    subgraph "3-Layer Search"
        search["mem-search<br/>Full-text FTS5 search<br/>Returns compact index"]
        timeline["mem-timeline<br/>Chronological context<br/>Around anchor ID"]
        get["mem-get<br/>Batch fetch<br/>Full details by IDs"]
    end

    search -->|"filter results"| timeline
    timeline -->|"select IDs"| get
    get -->|"learn details"| action["Act on knowledge"]

    style search fill:#1a2a2a,color:#fff
    style timeline fill:#2a1a2a,color:#fff
    style get fill:#1a2a3a,color:#fff
    style action fill:#1a5c1a,color:#fff
```

| Tool | Purpose | Output Size |
|------|---------|------------|
| `mem-search(query)` | FTS5 full-text search | ~50-100 tokens/result |
| `mem-timeline(anchor)` | Chronological context window | ~200-500 tokens |
| `mem-get(ids)` | Full observation details | ~500-1000 tokens/result |

## Data Flow Summary

```mermaid
flowchart LR
    subgraph Input["During Session"]
        T1[Tool Call] --> CB[Callback]
    end

    subgraph Capture["Capture & Compress"]
        CB --> Q[Queue]
        Q --> W[Worker]
        W --> P[Privacy]
        P --> C[Compressor]
        C --> O[Observation]
    end

    subgraph Store["Store"]
        O --> S[SQLite]
    end

    subgraph Output["On Restart"]
        S --> CG[ContextGen]
        CG --> SI[System Instruction]
        SI --> A[Agent]
        
        S --> mS[mem-search]
        S --> mT[mem-timeline]
        S --> mG[mem-get]
        
        mS --> A
        mT --> A
        mG --> A
    end

    style Input fill:#1a2a1a,color:#fff
    style Capture fill:#2a1a2a,color:#fff
    style Store fill:#1a1a2a,color:#fff
    style Output fill:#1a2a3a,color:#fff
```

## Configuration

```json
{
  "memory": {
    "enabled": true,
    "db_path": "~/.pi-go/memory/claude-mem.db",
    "token_budget": 5000,
    "max_pending": 100,
    "lookback_hours": 168,
    "excluded_tools": ["screen", "restart"]
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` | Enable/disable memory system |
| `token_budget` | `5000` | Max tokens for injected context |
| `max_pending` | `100` | Observation queue buffer size |
| `lookback_hours` | `168` | (7 days) how far back to look |
| `excluded_tools` | `["screen", "restart"]` | Tools to skip |

## File Structure

```
internal/memory/
├── db.go           # SQLite open, migrations, schema
├── store.go        # Store interface + SQLiteStore implementation
├── worker.go       # Background worker + ADK callbacks
├── compress.go     # AI compression via subagent
├── context.go      # Context generation for session start
├── search.go       # FTS5 search + timeline queries
├── privacy.go      # <private> tag stripping
└── types.go        # Data structures (Observation, SessionSummary, etc.)

internal/tools/
└── mem_search.go   # mem-search, mem-timeline, mem-get tools
```

## Key Design Decisions

1. **Non-blocking capture**: Tool callbacks enqueue and return immediately — never slows down the agent
2. **Drop-on-full**: If the queue fills, oldest observations are dropped (preferring liveness)
3. **Fallback compression**: If AI compression fails, store raw JSON with reduced fidelity
4. **Pure Go SQLite**: Uses `modernc.org/sqlite` — no CGO, portable binary
5. **FTS5 for search**: SQLite's full-text search with BM25 ranking
6. **Token budgeting**: Context injection stops when token budget is exhausted
7. **Session grouping**: Observations grouped by session for coherent context

## Session Summary

At session end, the system can generate a `SessionSummary` with:

- **Request**: What the user was trying to do
- **Investigated**: What was explored
- **Learned**: Key discoveries
- **Completed**: What was accomplished
- **Next Steps**: Suggested follow-ups

This is stored in `session_summaries` and surfaced at future session starts.

---

# MemPalace System

Structured knowledge storage with semantic embeddings, temporal knowledge graph, diary, and multi-layer context injection. Built on top of the observation pipeline and designed for long-term agent memory.

## Full System Overview

```mermaid
graph TB
    subgraph Sources["Ingestion Sources"]
        obs["Observation Pipeline<br/>(claude-mem)"]
        mine_proj["Project Miner<br/>(source files)"]
        mine_convo["Conversation Miner<br/>(JSONL/text)"]
        agent_tool["Agent ADK Tools<br/>(palace-add-drawer)"]
    end

    subgraph Palace["MemPalace Core"]
        bridge["ObservationBridge"]
        ds["DrawerService"]
        embedder["Embedder<br/>(all-MiniLM-L6-v2)"]
        dedup["Deduplication<br/>(cosine ≥ 0.9)"]
        kg["Knowledge Graph<br/>(temporal triples)"]
        diary["Diary<br/>(agent journal)"]
        layers["Memory Stack<br/>(L0/L1/L2/L3)"]
    end

    subgraph Storage["palace.db (SQLite)"]
        drawers_t["drawers + FTS5"]
        entities_t["entities"]
        triples_t["triples"]
        diary_t["diary_entries"]
    end

    subgraph Output["Context Injection"]
        wakeup["WakeUp()"]
        search["Semantic Search"]
        kg_query["KG Query"]
        traverse["Graph Traversal"]
        sys_instr["→ System Instruction"]
    end

    obs --> bridge --> ds
    mine_proj --> ds
    mine_convo --> ds
    agent_tool --> ds

    ds --> embedder --> dedup
    ds --> drawers_t
    kg --> entities_t
    kg --> triples_t
    diary --> diary_t

    layers --> wakeup --> sys_instr
    drawers_t --> search
    entities_t --> kg_query
    triples_t --> kg_query
    drawers_t --> traverse

    style Sources fill:#1a2a1a,color:#fff
    style Palace fill:#2a1a3a,color:#fff
    style Storage fill:#1a1a2a,color:#fff
    style Output fill:#1a2a3a,color:#fff
```

## Palace Metaphor

The MemPalace uses a spatial metaphor for organizing knowledge:

| Concept | Meaning | Example |
|---------|---------|---------|
| **Wing** | Project namespace | `pi-go`, `my-webapp` |
| **Room** | Topical area within a wing | `code`, `docs`, `tests`, `config` |
| **Hall** | Category of knowledge | `hall_decisions`, `hall_bugs`, `hall_features` |
| **Drawer** | A single unit of knowledge | A code chunk, an observation, a fact |
| **Triple** | A KG fact (subject-predicate-object) | `pi-go → uses → hugot` |

## Memory Layers

```mermaid
graph TD
    subgraph Stack["Memory Stack (Layered Context)"]
        L0["L0 — Identity<br/>Static identity file<br/>(who am I, personality)"]
        L1["L1 — Essential Story<br/>Top 15 drawers by importance<br/>(grouped by room, ≤3200 chars)"]
        L2["L2 — On-Demand Recall<br/>Up to 10 drawers per query<br/>(300 chars each)"]
        L3["L3 — Search<br/>Semantic or FTS5 search<br/>(unbounded, on request)"]
    end

    L0 --> L1 --> L2 --> L3

    style L0 fill:#5c3a1a,color:#fff
    style L1 fill:#3a5c1a,color:#fff
    style L2 fill:#1a3a5c,color:#fff
    style L3 fill:#3a1a5c,color:#fff
```

- **L0 + L1** are injected automatically at session start via `WakeUp()`
- **L2 + L3** are accessed on-demand via ADK tools during the session

## Semantic Embeddings

```mermaid
graph LR
    text["Input Text"] --> tokenizer["Tokenizer"]
    tokenizer --> model["all-MiniLM-L6-v2<br/>(ONNX, pure Go via hugot)"]
    model --> vec["384-dim float32 vector"]
    vec --> blob["Little-endian BLOB<br/>(SQLite storage)"]

    vec --> cosine["Cosine Similarity"]
    cosine -->|"≥ 0.9"| dup["Duplicate → reject"]
    cosine -->|"< 0.9"| store["Store drawer"]

    style model fill:#3a1a5c,color:#fff
    style cosine fill:#5c3a1a,color:#fff
```

- **Model**: `sentence-transformers/all-MiniLM-L6-v2` — small, fast, 384-dim
- **Runtime**: `knights-analytics/hugot` — pure Go (GoMLX), no CGO
- **Platform**: arm64 uses `qint8_arm64.onnx`, amd64 uses `model.onnx`
- **Fallback**: If model not loaded, search degrades to FTS5 keyword matching

## Knowledge Graph

```mermaid
graph LR
    subgraph KG["Temporal Knowledge Graph"]
        e1["Entity: pi-go"]
        e2["Entity: hugot"]
        e3["Entity: sqlite"]

        e1 -->|"uses<br/>(2026-04-05 → ∞)"| e2
        e1 -->|"stores_in<br/>(2026-04-05 → ∞)"| e3
        e2 -->|"provides<br/>(2026-04-07 → ∞)"| e4["Entity: embeddings"]
    end

    style KG fill:#1a2a2a,color:#fff
```

- **Temporal triples**: Each fact has `valid_from` / `valid_to` — facts can be invalidated without deletion
- **Idempotent**: Re-adding the same triple returns the existing one
- **Point-in-time queries**: `QueryEntity(entity, asOf: "2026-04-01")` returns only facts valid at that date
- **Timeline**: Full chronological history of all facts (including invalidated) for any entity
- **Auto-entities**: Referenced subjects/objects are created automatically on first use

## Observation Bridge

```mermaid
sequenceDiagram
    participant ADK as ADK Tool Call
    participant W as Memory Worker
    participant DB1 as claude-mem.db
    participant B as ObservationBridge
    participant P as Palace DrawerService
    participant DB2 as palace.db

    ADK->>W: AfterToolCallback
    W->>W: Privacy + AI Compress
    W->>DB1: InsertObservation()
    DB1->>B: OnAfterStore callback
    B->>B: deriveWing, deriveRoom, hallFromType
    B->>P: AddDrawer(DrawerInput)
    P->>P: Embed + Dedup check
    P->>DB2: InsertDrawer()
```

**Type → Hall mapping**:

| Observation Type | Palace Hall | Importance |
|-----------------|-------------|------------|
| `decision` | `hall_decisions` | 8 |
| `bugfix` | `hall_bugs` | 7 |
| `feature` | `hall_features` | 7 |
| `discovery` | `hall_discoveries` | 6 |
| `refactor` | `hall_refactors` | 5 |
| `change` | `hall_changes` | 4 |

## Mining Pipeline

```mermaid
graph TD
    subgraph ProjectMining["Project Mining"]
        dir["Source Directory"]
        walk["WalkDir<br/>(skip: .git, vendor, node_modules...)"]
        chunk["chunkText<br/>(1500 chars, 200 overlap)"]
        detect["detectRoom<br/>(glob patterns → dir → keywords)"]
        drawer1["Drawer (importance=3)"]

        dir --> walk --> chunk --> detect --> drawer1
    end

    subgraph ConvoMining["Conversation Mining"]
        files["JSONL / Text files"]
        parse["Parse turns<br/>(pi-go JSONL, Claude Code, plaintext)"]
        pair["Pair user+assistant<br/>(truncate to 3000 chars)"]
        drawer2["Drawer (importance=4)"]

        files --> parse --> pair --> drawer2
    end

    subgraph Config["mempalace.yaml"]
        yaml["wing: pi-go<br/>rooms:<br/>  - name: code<br/>    patterns: ['internal/**']<br/>  - name: docs<br/>    patterns: ['docs/**']"]
    end

    Config -.-> detect
    Config -.-> walk

    style ProjectMining fill:#1a2a1a,color:#fff
    style ConvoMining fill:#2a1a2a,color:#fff
    style Config fill:#1a1a2a,color:#fff
```

## ADK Agent Tools (10 tools)

```mermaid
graph TD
    subgraph Drawers["Drawer Tools"]
        t_status["palace-status<br/>Stats: drawers, wings, rooms, KG"]
        t_search["palace-search<br/>Semantic or FTS5 search"]
        t_add["palace-add-drawer<br/>Store knowledge + auto-embed"]
    end

    subgraph KGTools["Knowledge Graph Tools"]
        t_kg_add["palace-kg-add<br/>Add S-P-O triple"]
        t_kg_query["palace-kg-query<br/>Query entity facts"]
        t_kg_inv["palace-kg-invalidate<br/>Expire a triple"]
        t_kg_time["palace-kg-timeline<br/>Chronological history"]
    end

    subgraph DiaryTools["Diary Tools"]
        t_diary_w["palace-diary-write<br/>Write journal entry"]
        t_diary_r["palace-diary-read<br/>Read recent entries"]
    end

    subgraph GraphTools["Graph Tools"]
        t_traverse["palace-traverse<br/>BFS from room, N hops"]
    end

    style Drawers fill:#1a3a1a,color:#fff
    style KGTools fill:#3a1a1a,color:#fff
    style DiaryTools fill:#1a1a3a,color:#fff
    style GraphTools fill:#3a3a1a,color:#fff
```

## CLI Commands

All under `pi memory`:

| Command | Description |
|---------|-------------|
| `pi memory init [dir]` | Create `palace.db`, generate `mempalace.yaml` template |
| `pi memory model download` | Download all-MiniLM-L6-v2 ONNX model |
| `pi memory model status` | Show model path, size, file count |
| `pi memory status` | Drawer/wing/room/KG stats, model status |
| `pi memory mine <dir>` | Index source files (add `--convos` for conversations) |
| `pi memory search <query>` | Semantic or FTS5 search (`--wing`, `--room`, `--limit`) |
| `pi memory kg query <entity>` | Query KG triples (`--as-of`, `--direction`) |
| `pi memory kg add <s> <p> <o>` | Add triple (`--valid-from`) |
| `pi memory kg timeline <entity>` | Show full triple history |
| `pi memory wake-up` | Print L0+L1 context (debug/pipe) |
| `pi memory recent [project]` | Show recent observations (`--type`, `--limit`, `--json`) |

## Configuration

### `mempalace.yaml` (project-level)

```yaml
wing: pi-go
rooms:
  - name: code
    patterns:
      - "internal/**"
  - name: docs
    patterns:
      - "docs/**"
    keywords:
      - "documentation"
```

### Application config (JSON)

```json
{
  "palace": {
    "enabled": true,
    "db_path": "~/.pi-go/palace.db",
    "model_path": "~/.pi-go/models/KnightsAnalytics_all-MiniLM-L6-v2"
  }
}
```

### Internal defaults (`PalaceConfig`)

| Setting | Default | Purpose |
|---------|---------|---------|
| `DeduplicationThreshold` | `0.9` | Cosine similarity cutoff for duplicates |
| `L1TopK` | `15` | Max drawers in essential story |
| `L1MaxChars` | `3200` | Hard cap on L1 context |
| `L2MaxDrawers` | `10` | Max drawers in on-demand recall |
| `L2MaxCharsPerDrawer` | `300` | Per-drawer truncation for L2 |

## Use Cases

### 1. Long-term project context

```
# Mine your codebase once
pi memory init .
pi memory model download
pi memory mine .

# Agent now has semantic search over your entire project
# L1 injects the most important knowledge at session start
```

The agent starts every session with key architectural decisions, known bugs, and important features — without re-reading the entire codebase.

### 2. Cross-session knowledge transfer

```mermaid
graph LR
    S1["Session 1<br/>Discovers auth bug"] -->|"observation → bridge → drawer"| DB["palace.db"]
    DB -->|"WakeUp() at start"| S2["Session 2<br/>Knows about auth bug"]
    S2 -->|"new discovery"| DB
    DB -->|"WakeUp()"| S3["Session 3<br/>Knows full history"]
```

The bridge automatically funnels observations from each session into palace drawers with importance scoring. Future sessions see the most important discoveries in their L1 context.

### 3. Building a project knowledge graph

```
# Agent learns facts during work
palace-kg-add "auth_service" "depends_on" "redis"
palace-kg-add "auth_service" "owned_by" "backend_team"
palace-kg-add "redis" "version" "7.2"

# Later, query relationships
palace-kg-query "auth_service"
# → depends_on redis, owned_by backend_team

# When facts change, invalidate (history preserved)
palace-kg-invalidate <triple-id>
palace-kg-add "redis" "version" "7.4"

# Full timeline shows evolution
palace-kg-timeline "redis"
# → version 7.2 (2026-04-01 → 2026-04-08)
# → version 7.4 (2026-04-08 → ∞)
```

### 4. Conversation mining for onboarding

```
# Mine past conversations to bootstrap memory
pi memory mine ./conversations --convos

# New team members' agents get context from past discussions
# without reading thousands of chat messages
```

### 5. Agent diary for self-reflection

The agent can keep a journal of its reasoning, mistakes, and learnings:

```
palace-diary-write "Spent too long on FTS5 query optimization.
The real bottleneck was the embedding computation. Next time,
profile before optimizing."

palace-diary-read --agent pi-agent --limit 5
```

### 6. Graph traversal for discovery

```
palace-traverse --start "internal/palace" --hops 2

# Discovers: internal/palace → connects to → internal/cli (1 hop)
#            internal/cli → connects to → internal/tui (2 hops)
# Reveals architectural dependencies the agent wasn't aware of
```

## File Structure

```
internal/palace/
├── palace.go           # Palace facade (wires all components)
├── types.go            # Drawer, Triple, Entity, DiaryEntry, WingSummary
├── config.go           # PalaceConfig + functional options
├── db.go               # SQLite open + 3 schema migrations
├── store.go            # PalaceStore interface (19 methods)
├── sqlite_store.go     # SQLite implementation
├── drawer_service.go   # Add/Search with embedding + dedup
├── embedder.go         # hugot pipeline + cosine similarity
├── embedding.go        # Little-endian BLOB serialization
├── layers.go           # L0/L1/L2 memory stack
├── graph.go            # BFS traversal + tunnel discovery
├── kg.go               # Temporal knowledge graph
├── miner.go            # MineConfig + chunk/detect helpers
├── miner_project.go    # Source file mining
├── miner_convo.go      # Conversation mining
├── bridge.go           # Observation → Drawer bridge
├── tools.go            # PalaceTools() — 10 ADK tools
├── tool_add.go         # palace-add-drawer
├── tool_search.go      # palace-search
├── tool_status.go      # palace-status
├── tool_kg.go          # palace-kg-* (4 tools)
├── tool_diary.go       # palace-diary-* (2 tools)
└── tool_traverse.go    # palace-traverse

internal/cli/
├── memory.go           # `pi memory` root command
├── memory_init.go      # `pi memory init`
├── memory_model.go     # `pi memory model download/status`
├── memory_status.go    # `pi memory status`
├── memory_mine.go      # `pi memory mine`
├── memory_search.go    # `pi memory search`
├── memory_kg.go        # `pi memory kg query/add/timeline`
├── memory_wakeup.go    # `pi memory wake-up`
└── memory_recent.go    # `pi memory recent`

mempalace.yaml              # Project-level room/wing config
```

## Key Design Decisions

1. **Dual-database architecture**: `claude-mem.db` for raw observations, `palace.db` for structured knowledge — separation of concerns
2. **Observation bridge**: Automatic flow from observation pipeline into palace drawers with type→hall mapping and importance scoring
3. **Semantic + FTS5 fallback**: Embeddings enable similarity search; FTS5 provides keyword search when model isn't loaded
4. **Deduplication at ingestion**: Cosine similarity ≥ 0.9 prevents redundant knowledge storage
5. **Temporal KG**: Facts have validity windows — no destructive updates, full audit trail
6. **Layered context injection**: L0/L1 auto-injected, L2/L3 on-demand — balances richness vs. token cost
7. **Pure Go stack**: hugot (GoMLX) + modernc.org/sqlite — single binary, no CGO, cross-platform
8. **Spatial metaphor**: Wing/room/hall/drawer organization mirrors how humans navigate knowledge spatially
