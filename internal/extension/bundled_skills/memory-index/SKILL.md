---
name: memory-index
description: Index a folder's contents into the MemPalace semantic memory for search and retrieval. Use this skill whenever the user asks to "index a folder", "index a directory", "index memory", "mine a project into memory", "make a folder searchable", "embed a folder", "ingest code into the palace", or "index this directory for semantic search". Covers the full flow: model download, mempalace.yaml room configuration, mining, and verification via search. Also trigger when the user says "memory index", "index memory of folder", or asks how to make a project's code semantically searchable with pi-go's MemPalace.
---

# Index a Folder into MemPalace

This skill walks through indexing a folder's source code into pi-go's MemPalace —
the 4-layer semantic memory system with SQLite storage and embedding-based search.

Once indexed, the agent can search the folder's contents by meaning, not just keywords,
using the `palace-search` tool or `pi memory search` CLI.

## Prerequisites

The embedding model (`all-MiniLM-L6-v2`) must be downloaded. Check with:

```bash
pi memory model status
```

If not downloaded:

```bash
pi memory model download
```

This fetches ~90 MB of ONNX model files to `~/.pi-go/models/`. It auto-selects the
optimal ONNX variant for the platform (quantized ARM64 on Apple Silicon, base on x86_64).

> The model is optional. Without it, search falls back to FTS5 keyword matching.
> Semantic search requires the model; keyword search works without it.

## Workflow

### 1. Verify the model is ready

```bash
pi memory model status
```

Output should show `Model: all-MiniLM-L6-v2` with a path and size. If it says
`Model: not downloaded`, run `pi memory model download` first.

### 2. Configure rooms (optional but recommended)

Create a `mempalace.yaml` at the repo root to map file patterns to semantic "rooms".
This narrows the search space before embeddings fire, improving retrieval significantly
(benchmark: 60.9% recall unfiltered → 94.8% with wing+room filtering).

```yaml
wing: my-project          # top-level name (defaults to directory basename)

rooms:
  - name: api
    patterns:
      - "internal/api/**"
      - "internal/handler/**"
    keywords:
      - http
      - handler
      - route
  - name: models
    patterns:
      - "internal/models/**"
    keywords:
      - schema
      - struct
      - database
  - name: tests
    patterns:
      - "**/*_test.go"
    keywords:
      - test
      - mock
      - fixture
```

**Rules for good rooms:**

- 3–8 rooms is the sweet spot. Too few = no filtering power. Too many = sparse drawers.
- Use `patterns` (glob) for path-based assignment. Use `keywords` for content-based hints.
- Files not matching any room go to a default room named `general`.
- The `wing` defaults to the directory basename if omitted.

### 3. Mine the folder

```bash
pi memory mine .
```

This command:

- Walks the directory recursively
- Respects `.gitignore` and skips `node_modules/`, `vendor/`, `.git/`, `dist/`, `build/`, etc.
- Chunks each supported file into semantic units
- Embeds each chunk with `all-MiniLM-L6-v2` (384-dim vectors)
- Stores drawers in `.pi-go/palace.db` (SQLite + FTS5 + embedding BLOBs)
- Detects and skips near-duplicate content (cosine similarity > threshold)

**Flags:**

```bash
pi memory mine .                    # mine source files (default)
pi memory mine . --wing myapp       # override wing name
pi memory mine . --convos            # mine conversation files (.jsonl, .txt, .md) instead of source
```

**Supported file extensions** (source mode): `.go`, `.py`, `.js`, `.ts`, `.tsx`, `.jsx`,
`.java`, `.c`, `.cpp`, `.h`, `.hpp`, `.rs`, `.rb`, `.php`, `.swift`, `.kt`, `.scala`,
`.md`, `.txt`, `.json`, `.yaml`, `.yml`, `.toml`, `.xml`, `.html`, `.css`, `.scss`,
`.sql`, `.sh`, `.bash`, `.zsh`, `.fish`.

**File size limit:** 512 KB per file. Larger files are skipped silently.

**Progress:** The command shows a live spinner with per-file progress, an embedding
phase, and a final summary:

```
Mining complete:
  Processed: 142
  Added:     138
  Skipped:   4 (duplicates)
  Errors:    0
```

### 4. Verify with search

Search the indexed content semantically:

```bash
pi memory search "authentication flow" --limit 5
pi memory search "database connection pool" --wing myapp --room models
```

Or from within an agent session, the `palace-search` tool is available automatically
when a palace database exists at `.pi-go/palace.db`.

### 5. Check status

```bash
pi memory status
```

Shows drawer counts per wing/room, database size, and model status.

## Re-indexing

Mining is idempotent for identical content — duplicates are detected by embedding
similarity and skipped. To re-index after significant changes:

```bash
pi memory mine .
```

Changed files will be re-embedded; unchanged files will be skipped as duplicates.
For a full clean re-index, delete the database first:

```bash
rm .pi-go/palace.db
pi memory mine .
```

## Tips

- **Index at the repo root** so `.gitignore` and `mempalace.yaml` are picked up.
- **Run from the project directory** — the palace database is created at `.pi-go/palace.db`
  relative to the mined directory.
- **Index conversations separately** with `--convos` to search past agent sessions.
- **Search with filters** — `--wing` and `--room` dramatically improve recall by
  narrowing the search space before similarity ranking.
- **No model? Still works.** If the embedding model isn't loaded, search falls back to
  FTS5 keyword matching automatically. Semantic search is better, keyword is the floor.