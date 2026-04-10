---
name: memory-status
description: >
  Show MemPalace memory system status — drawer counts, wings, rooms,
  knowledge graph stats, and embedding model state.
user_invocable: true
metadata:
  author: dimetron
  version: "1.0"
---

## When to use

Use this skill when the user wants to:

- Check the current state of the palace memory system
- See how many drawers, wings, and rooms exist
- Verify the embedding model is loaded
- View knowledge graph statistics (entities, triples, predicates)
- Diagnose palace issues or verify mining results

## What it does

Runs `pi memory status` to display a comprehensive overview of the palace:

- **Drawer count**: Total knowledge units stored
- **Wing count**: Number of project namespaces
- **Room count**: Number of topical areas
- **Model status**: Whether the embedding model is loaded
- **Knowledge Graph**: Entity count, triple count (active vs total), predicates in use
- **Wing details**: Per-wing breakdown of drawers and rooms

## Steps

1. Run the status command:

```bash
# Default: looks for .pi-go/palace.db in current directory
pi memory status

# Specify a custom database path
pi memory status --db ~/.pi-go/palace.db
```

2. If no palace database is found, suggest running `pi memory init .`
3. Present the output to the user in a clear format

## Arguments

The user can pass arguments after `/memory-status`:

- `--db <path>` to specify a custom palace database path

Examples:
- `/memory-status` — show status for current project
- `/memory-status --db ~/.pi-go/palace.db` — show status for global palace

## Additional diagnostics

If the user needs deeper insight, chain with other commands:

```bash
# Search for specific knowledge
pi memory search "auth middleware"

# Check recent observations
pi memory recent

# View knowledge graph for an entity
pi memory kg query "pi-go"

# Show L0+L1 context that would be injected
pi memory wake-up
```

## Notes

- If the palace DB doesn't exist, the command exits gracefully with instructions
- Model "not loaded" means semantic search falls back to FTS5 keyword matching
- Run `pi memory model download` to enable semantic embeddings