---
name: memory-mine
description: >
  Mine project files or conversations into the MemPalace memory system.
  Indexes source code and conversation history as searchable palace drawers
  with semantic embeddings and room assignment.
user_invocable: true
metadata:
  author: dimetron
  version: "1.0"
---

## When to use

Use this skill when the user wants to:

- Index/mine project source files into the palace memory
- Mine conversation history (JSONL, text) into the palace
- Rebuild or refresh the palace knowledge base from disk
- Bootstrap memory for a new project

## What it does

Runs `pi memory mine` to walk a directory and ingest files as palace drawers:

1. **Source mining** (default): Walks the directory tree, chunks source files (1500 chars, 200 overlap), assigns rooms via `mempalace.yaml` patterns or directory structure, generates embeddings, deduplicates (cosine >= 0.9), and stores as drawers.

2. **Conversation mining** (`--convos`): Parses JSONL (pi-go or Claude Code format) and plaintext conversations, pairs user+assistant turns, and stores as drawers with importance=4.

## Steps

1. Check if `palace.db` exists (run `pi memory init .` if not)
2. Check if the embedding model is downloaded (run `pi memory model download` if not)
3. Run the mine command:

```bash
# Mine source files from current directory
pi memory mine .

# Mine source files with explicit wing name
pi memory mine . --wing my-project

# Mine conversation files
pi memory mine ./conversations --convos
```

4. Show the results (processed, added, skipped duplicates, errors)
5. Optionally run `pi memory status` to confirm the updated state

## Arguments

The user can pass arguments after `/memory-mine`:

- A directory path (default: `.` current directory)
- `--wing <name>` to override the wing name
- `--convos` to mine conversations instead of source files

Examples:
- `/memory-mine` — mine current directory
- `/memory-mine ./src` — mine the src directory
- `/memory-mine ./chats --convos` — mine conversation files

## Prerequisites

- `pi` binary must be built and available (`go build -o pi ./cmd/pi`)
- Palace DB must be initialized (`pi memory init .`)
- For semantic search, embedding model must be downloaded (`pi memory model download`)

## Notes

- Respects `.gitignore` patterns
- Skips hidden dirs, `node_modules`, `vendor`, `.git`, `build`, `target`
- Skips files > 512 KB
- Uses `mempalace.yaml` for room assignment if present
- Duplicates (cosine similarity >= 0.9) are automatically skipped