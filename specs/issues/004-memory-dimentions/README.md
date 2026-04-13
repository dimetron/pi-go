# pi-go MemPalace — Embedding Panic Root Cause + Long-Context Model Swap

**Status:** Phase 1 complete (truncation guardrail shipped); Phase 2 pending (model swap to nomic-embed-text-v1.5)
**Date:** 2026-04-13
**Repo:** `github.com/dimetron/pi-go`
**Subsystem:** MemPalace semantic memory (SQLite+BLOB embeddings)
**Stack confirmed from stack trace:** `github.com/gomlx/go-huggingface` + `gomlx/onnx-gomlx` + `gomlx/gomlx` executor

---

## 1. Root cause (what the screenshot actually shows)

The panic `(Float32)[1 542 384]` vs `(Float32)[1 512 384]` is a **sequence-length** mismatch, not embedding-dim.

| Axis | Value | Meaning |
|---|---|---|
| 0 | `1` | batch |
| 1 | `542` vs `512` | **sequence length** — tokenizer output vs ONNX graph static axis |
| 2 | `384` | embedding dim (correct for `all-MiniLM-L6-v2`, matches `hidden_size`) |

**The inverted diagnosis in the subagent trace** ("512-dim model, not all-MiniLM-L6-v2's 384-dim") is wrong — `384` is the MiniLM hidden size and matches. The failing axis is axis 1.

### Why it panics

1. `all-MiniLM-L6-v2` has `max_position_embeddings = 512`. The HF ONNX export (`onnx/model.onnx`) frequently ships with `input_ids` as **static shape `[batch, 512]`** unless re-exported with `--dynamic_axes`.
2. pi-go's tokenizer is producing 542 token IDs for some chunk of ingested content (likely a long code file or concatenated session transcript).
3. `gomlx/onnx-gomlx` builds a GoMLX graph from the ONNX spec. When ONNX declares a concrete dim, GoMLX treats it as a shape constraint and rejects mismatched inputs at execution — hence the panic, not a silent truncation.
4. **There is no truncation guardrail between tokenizer and executor.** The call site builds `inputIDs` directly from tokenizer output and feeds them to `model.Call(...)`.

### Two orthogonal problems

| Problem | Fix class |
|---|---|
| **A. No truncation** — tokens beyond model max are passed through | Tokenizer-level hard limit + chunking policy |
| **B. 512-token ceiling is too low for pi-go's RAG use case** (code files, session JSONL, Claude Code transcripts regularly exceed 512 tokens) | Swap to a long-context embedding model |

**Neither fix alone is sufficient.** You need both: truncation as a safety net, long context to avoid excessive chunking that destroys semantic coherence.

---

## 2. Long-context embedding model swap — options

Filtered for: Apache-2.0/MIT license, ONNX available, ≤1B params, proven with `go-huggingface` or pure-ONNX path, works on M5 Max CPU+Metal / production x86.

| Model | Ctx | Dim | Params | License | ONNX | Notes |
|---|---|---|---|---|---|---|
| **`nomic-ai/nomic-embed-text-v1.5`** | **8192** | 768 (Matryoshka: 64/128/256/512/768) | 137M | Apache-2.0 | ✅ official | BERT + RoPE. Matryoshka lets you truncate vector at storage time → **keep SQLite BLOB size small**. Best cost/quality for code+prose. |
| **`BAAI/bge-m3`** | 8192 | 1024 | 568M | MIT | ✅ official | Multilingual (100+ langs incl. Czech/Ukrainian — relevant for you). Supports dense + sparse + ColBERT in one pass. Larger footprint. |
| **`Snowflake/snowflake-arctic-embed-m-v2.0`** | 8192 | 768 | 305M | Apache-2.0 | ✅ official | Multilingual, Matryoshka to 256. Strong on code retrieval benchmarks. Middle ground. |
| `jinaai/jina-embeddings-v3` | 8192 | 1024 (Matryoshka to 32) | 572M | **CC-BY-NC-4.0** | ✅ | **❌ non-commercial license — skip for Solo.io/Amdocs scenarios.** |
| `mixedbread-ai/mxbai-embed-large-v1` | 512 | 1024 | 335M | Apache-2.0 | ✅ | Same ceiling as current — no context improvement. Skip. |
| `tencent/KaLM-Embedding-Gemma3-12B-2511` | 32k | 3840 | 12B | Gemma License | ✅ (via go-huggingface ref) | Overkill for local MemPalace. GoMLX supports it but 12B params on 128GB M5 is ~24GB loaded. Keep in mind for future reranker/server use. |

### Recommendation: **`nomic-embed-text-v1.5`**

Rationale for pi-go specifically:
- **8192 ctx** covers ~32KB of code per embed call — eliminates panic class for ~99% of session JSONL and file chunks.
- **Matryoshka truncation to 256 dims** → SQLite BLOB stays at 256·4 = **1024 bytes per vector** (same as current MiniLM's 384·4 = 1536 bytes, actually smaller). No storage regression.
- Smallest long-ctx model here (137M) — fastest on CPU, lightest for M2 16GB laptop build path.
- Same architecture family as MiniLM (BERT-style encoder) → minimal change to the GoMLX graph execution path.
- Requires a **task prefix** at query time: `"search_document: "` for indexing, `"search_query: "` for querying. Simple wrapper.

Fallback if nomic's RoPE handling surfaces issues in `gomlx/onnx-gomlx`: **`snowflake-arctic-embed-m-v2.0`** — pure BERT-style positional embeddings (interpolated), safer on the ONNX path.

---

## 3. Implementation plan

### 3.1 Truncation guardrail ✅ SHIPPED (2026-04-13)

**Location:** `internal/palace/embedder.go`

At the embedder call site, before `pipeline.RunPipeline`:

```go
// internal/palace/embedder.go

const (
    maxTokenLength = 128  // matches tokenizer truncation config
    maxCharLength  = maxTokenLength * 4  // 512 chars; BERT ~1 tok per 3-4 chars
)

func (e *Embedder) Embed(texts []string) ([][]float32, error) {
    for i := range texts {
        if len(texts[i]) > maxCharLength {
            texts[i] = texts[i][:maxCharLength]
        }
    }
    result, err := e.pipeline.RunPipeline(texts)
    // ...
}
```

**Why character-length vs token-count truncation:** The tokenizer (`go-huggingface` Go backend) does not apply tokenizer-level truncation from `tokenizer.json` (truncation config is set to `max_length=128` but `go-huggingface` Go tokenizer does not respect this field during `EncodeWithAnnotations`). A hard character limit is the safest available mitigation.

**Validation:** Test `TestEmbedder_Truncation` passes: long text (~6000+ chars) produces correct 384-dim embedding without panic.

**Future:** Upgrade to nomic-embed-text-v1.5 (8192 ctx) per §3.3 to eliminate truncation for most RAG use cases.

### 3.2 Chunking policy (before tokenizer)

Even with 8192 ctx, some inputs (full session JSONL dumps) will exceed it. Apply a character-budget pre-chunker upstream in the mining pipeline:

- **Rough heuristic:** 4 chars/token → for 8192 ctx, chunk at **~28k chars** with **10% overlap**.
- Respect structural boundaries: JSONL line boundaries for session logs, function/type boundaries for Go code (use `go/parser` AST or tree-sitter — pi-go already has LSP integration, this is cheap).
- Store chunk metadata: `source_path`, `chunk_idx`, `total_chunks`, `byte_range`.

### 3.3 Model swap

**Step 1 — download via `gomlx/go-huggingface`:**

```go
repo := hub.New("nomic-ai/nomic-embed-text-v1.5")
onnxPath, _ := repo.DownloadFile("onnx/model.onnx")
tokPath, _  := repo.DownloadFile("tokenizer.json")
cfgPath, _  := repo.DownloadFile("config.json")
```

**Step 2 — verify ONNX input shape is dynamic:**

```bash
python -c "import onnx; m = onnx.load('model.onnx'); \
  print([(i.name, [d.dim_param or d.dim_value for d in i.type.tensor_type.shape.dim]) \
  for i in m.graph.input])"
```

Expect: `[('input_ids', ['batch_size', 'sequence_length']), ...]`. If any dim is a hard int, re-export with `optimum-cli export onnx --model ... --dynamic-axes`.

**Step 3 — update MemPalace embedder config:**

```go
type EmbedderConfig struct {
    Model        string // "nomic-ai/nomic-embed-text-v1.5"
    MaxSeqLen    int    // 8192
    EmbedDim     int    // 256 (Matryoshka truncated)
    QueryPrefix  string // "search_query: "
    DocPrefix    string // "search_document: "
}

func (e *Embedder) EmbedDocument(ctx context.Context, text string) ([]float32, error) {
    return e.embed(ctx, e.cfg.DocPrefix+text)
}

func (e *Embedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
    return e.embed(ctx, e.cfg.QueryPrefix+text)
}
```

**Step 4 — Matryoshka truncation + L2-normalize:**

```go
func matryoshkaTruncate(full []float32, dim int) []float32 {
    v := make([]float32, dim)
    copy(v, full[:dim])
    var norm float32
    for _, x := range v { norm += x * x }
    norm = float32(math.Sqrt(float64(norm)))
    if norm > 0 {
        for i := range v { v[i] /= norm }
    }
    return v
}
```

### 3.4 SQLite migration

The embedding dim changes from 384 → 256 (or 768 if you don't Matryoshka). Options:

- **Clean reindex** (recommended for pi-go's current scale): drop `embeddings` table, re-mine. Fast on M5 Max with 137M model — a full ~20k-drawer MemPalace reindexes in minutes.
- **Dual-column migration** (only if you have users with large existing indices): add `embedding_v2 BLOB`, `embedding_model TEXT`, populate lazily on query miss.

Version the embedder in metadata:

```sql
CREATE TABLE IF NOT EXISTS embedding_meta (
    model_name TEXT PRIMARY KEY,
    dim INTEGER NOT NULL,
    max_seq_len INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
```

### 3.5 Tests (aligns with `code-guidelines-go` + `bubbletea-testing` skills)

- **Unit:** truncation guardrail table-driven test — inputs of 0, 511, 512, 513, 8000, 10000 tokens.
- **Property/fuzz:** `go test -fuzz=FuzzEmbedNeverPanics` feeding random UTF-8 up to 64KB.
- **Integration (`envtest`-style, offline):** pre-download nomic ONNX into testdata fixture, run EmbedDocument on known strings, assert cosine similarity shape and known pairs cluster.
- **Regression:** the exact 542-token input that triggered the original panic → assert no panic, returns a 256-dim unit vector.

---

## 4. Risk & rollback

| Risk | Likelihood | Mitigation |
|---|---|---|
| nomic's RoPE not cleanly handled by `gomlx/onnx-gomlx` | Medium | Fallback to `snowflake-arctic-embed-m-v2.0` (classic BERT positional). Gate via `EmbedderConfig.Model`. |
| 8192-ctx embedding ~3–5× slower per call than MiniLM@512 | High | Batch more aggressively (`EmbedBatch`); most RAG flows embed once at ingest, so amortized cost is fine. |
| SQLite BLOB size regression if you don't Matryoshka | Medium | Enforce `MatryoshkaDim` in config; default 256. |
| Breaking existing MemPalace indices | High for existing users, low for you | Version table + clean reindex path. |

**Rollback:** keep the MiniLM code path behind a feature flag (`PI_EMBEDDER=minilm|nomic`) for one release cycle.

---

## 5. References

- go-huggingface + ONNX path: https://github.com/gomlx/go-huggingface (`onnx-gomlx` example in README)
- Nomic v1.5 model card: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5
- BGE-M3: https://huggingface.co/BAAI/bge-m3
- Snowflake Arctic v2: https://huggingface.co/Snowflake/snowflake-arctic-embed-m-v2.0
- Matryoshka representation learning (Kusupati et al., 2022): https://arxiv.org/abs/2205.13147
- ONNX dynamic axes export: https://huggingface.co/docs/optimum/exporters/onnx/overview

---

## 6. Linear task draft — KBX team

```
Title: [KBX-XX] pi-go MemPalace: fix embedding dim-mismatch panic + swap to long-context embedder

Team: KubeX
Priority: High
Labels: pi-go, mempalace, embedding, bug, tech-debt
Estimate: 3d

## Summary
MemPalace embedder panics with (Float32)[1 542 384] vs [1 512 384] when ingesting
content >512 tokens. Root cause: all-MiniLM-L6-v2 ONNX has static seq_len=512 and
no truncation guardrail exists between tokenizer and gomlx/onnx-gomlx executor.

## Scope
1. Add tokenizer truncation guardrail + truncation metric (ships independently).
2. Add structural chunker upstream in mining pipeline (JSONL-line & Go-AST aware).
3. Swap default embedder to nomic-embed-text-v1.5 (8192 ctx, 768-dim Matryoshka→256).
   Fallback: snowflake-arctic-embed-m-v2.0 behind PI_EMBEDDER env flag.
4. SQLite: add embedding_meta table; provide reindex command `pi mempalace reindex`.
5. Tests: unit (truncation table), fuzz (FuzzEmbedNeverPanics), regression (542-tok
   input from original panic), integration (offline ONNX fixture).
6. Docs: README section + SKILL.md update for mempalace skill.

## Acceptance criteria
- [x] No panic on inputs up to 64KB / ~16k tokens. **Done (2026-04-13):** Character-length truncation at 512 chars in `internal/palace/embedder.go` prevents GoMLX bucket mismatch panics. Short-term safety net; long-term fix is model swap in §3.3.
- [ ] mempalace_embedding_truncations_total metric exposed. <!-- TODO: add prometheus counter in palace/metrics.go -->
- [ ] Cosine similarity sanity test (known pairs) passes in CI. <!-- TODO -->
- [ ] `pi mempalace reindex` migrates existing DBs cleanly. <!-- TODO: future work -->
- [ ] Benchmark: p50 embed latency <200ms on M5 Max CPU for 1KB input. <!-- TODO -->
- [ ] `go test ./internal/memory/...` green incl. -race and -fuzz=30s. <!-- TODO -->

## Out of scope
- Reranker model (separate KBX task, consider MixedBread reranker v1 via GoMLX).
- Multi-vector / ColBERT retrieval (future, bge-m3 enables this path).

## Links
- Root-cause write-up: <paste Drive/local path>
- Original panic screenshot: <attach>
- go-huggingface: https://github.com/gomlx/go-huggingface
- Nomic v1.5: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5
```