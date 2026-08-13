# Research — MemPalace (upstream) vs pi-go port

Upstream: `tmp/memory/mempalace`, MemPalace v3.7.1, Python, ~45k LOC.
Port: `internal/palace` (~14k LOC incl. tests) + `internal/memory`.

The port is a genuine reimplementation, not a wrapper: same conceptual model
(wings → rooms → drawers, temporal knowledge graph, L0–L3 wake-up stack), Go
types, SQLite instead of ChromaDB.

## Ported and working

Verified by `go test ./internal/palace/... ./internal/memory/...` (2.8 s, passes
outside the sandbox).

| Capability | Upstream | pi-go |
|---|---|---|
| Wings / rooms / halls / drawers | `palace.py` | `palace.go`, `sqlite_store.go` |
| Hybrid search (keyword + vector) | `searcher.py` | `drawer_service.go:122` — FTS5 ∪ cosine, merged |
| Dedup | `dedup.py` | content-hash + cosine (`drawer_service.go:60-89`) |
| Temporal knowledge graph | `knowledge_graph.py` | `kg.go` — add/query/invalidate/timeline with validity windows |
| LLM entity/relation extraction | `llm_refine.py` | `tool_kg_extract.go` |
| Room graph traversal, cross-wing tunnels | `palace_graph.py` | `graph.go` |
| Agent diaries | MCP diary tools | `tool_diary.go` |
| L0–L3 memory stack | `layers.py` | `layers.go` |
| Project miner | `miner.py` | `miner_project.go` |
| Conversation miner | `convo_miner.py` | `miner_convo.go` (JSONL + plaintext) |
| Local embeddings | ONNX / sentence-transformers | `embedder_backend_ort.go`, `embedder_backend_go.go` |
| Server embeddings | openai-compat endpoint | `embedder_ollama.go` (Ollama only) |
| Embedding cache | — | `embedding_cache.go` |
| Storage seam | `backends/base.py` | `store.go` — `PalaceStore` interface |
| Schema migration | `migrate.py` | `schema_versions` table |
| CLI | `mempalace <cmd>` | `pi memory model\|init\|status\|mine\|search\|kg\|wake-up\|recent\|clear` |

## Not ported

Catalogued for completeness. **None of these is scheduled in this spec** except
where `plan.md` says otherwise (query sanitisation and the benchmark, in Slice 8).

| Upstream | What it does | pi-go |
|---|---|---|
| `dialect.py` | AAAK compressed index layer — lets an LLM scan thousands of entries and pick a drawer without reading content | absent |
| `entity_detector.py`, `entity_registry.py` | auto-detect and disambiguate people/projects; entity-first indexing | absent — `kg.go` auto-creates entities by name string only |
| `query_sanitizer.py` | prompt-contamination mitigation on search input | absent |
| `repair.py` | palace consistency check and repair | absent |
| `exporter.py` | palace export | absent |
| `onboarding.py` | interactive first-run model/config setup | `pi memory init` only creates the DB |
| `sweeper.py` | one verbatim drawer per user/assistant message, idempotent | absent |
| `spellcheck.py`, `normalize.py`, `encoding_repair.py` | transcript normalisation | partial, inside `miner_convo.go` |
| `mcp_server.py` — 44 MCP tools | palace over MCP for any client | 11 ADK tools, no MCP or HTTP surface |
| ChromaDB HNSW | ANN vector index | brute-force cosine over all embeddings |
| `backends/` — chroma, sqlite_exact, milvus, qdrant, pgvector | pluggable vector stores | SQLite only (interface exists) |
| `benchmarks/` — LongMemEval, LoCoMo, ConvoMem, MemBench | reproducible retrieval numbers | none |
| `hooks/` — Stop + PreCompact auto-save | out-of-process capture | in-process `ObservationBridge` (different design, deliberately) |

## Design divergences worth keeping

Three places where pi-go deviates on purpose and should not be "corrected"
toward upstream:

1. **Storage.** SQLite + FTS5 for everything, including vectors as BLOBs. One
   file, no daemon, no Python. Upstream needs ChromaDB and a ~300 MB model
   download before the first query.
2. **Capture.** Upstream captures through shell hooks that re-read transcripts
   off disk. pi-go captures in-process from the ADK after-tool callback and
   bridges observations into drawers. Lower latency, no transcript parsing, no
   30-day expiry problem — once it is reachable (F1).
3. **Compression.** Upstream stores verbatim and never summarises ("verbatim
   always" is its stated non-negotiable). pi-go compresses each tool call into a
   structured observation. That is a real semantic difference: pi-go's index is
   lossy by design. It buys a much smaller context injection; it costs exact
   recall. Worth stating explicitly rather than drifting into it.

## Bottom line

The gap between pi-go and upstream is not feature coverage. It is that pi-go's
version has never run. Closing F1–F4 gets the port from "0 observations in 6228
sessions" to "working memory system"; the missing-features table above only
starts to matter after that.
