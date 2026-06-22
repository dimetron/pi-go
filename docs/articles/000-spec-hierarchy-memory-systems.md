# Aristotle's Memory Palace Meets `specs/`: Why Hierarchical Mnemonics Are the Missing Architecture for AI Agents

**TL;DR** — Ancient memory athletes organized knowledge into spatial hierarchies: wings, rooms, and alphabetical pegs. Modern AI agents need the same thing. Here's how three open-source projects — pi-go's SPEC hierarchy, MemPalace, and Caveman — converge on a 2,400-year-old insight: *structured forgetting is more powerful than total recall*.

---

In *De Memoria*, Aristotle described what mnemonists now call the "nuclear alphabet" — assigning each letter a vivid image, then walking those images through a familiar building bi-directionally. The Art of Memory community still practices this: dedicate a memory palace per letter, and anything you need to recall gets filed by its initial into the right spatial slot. The alphabet becomes an index; the palace becomes a retrieval engine.

If that sounds like a file system with semantic search, it should. Because the exact same problem — *how do you store everything and still find the one thing you need?* — is now the central unsolved challenge for AI coding agents that persist across sessions.

Three recent open-source projects attack this from different angles. Together they sketch what I think is the emerging architecture for agent memory.

## Pi-Go's SPEC Hierarchy: The Method of Loci for Code

Pi-go is a Go implementation of an AI coding agent built on Google ADK Go. Its `specs/` directory implements a 7-phase Plan-Driven Design (PDD) workflow that functions as an externalized memory palace for features:

```
specs/
├── features/
│   ├── MEM/           # Memory system (claude-mem)
│   ├── SOP/           # Standard Operating Procedures (enhance-from-oh-my-pi, plan-command-sop, plan-resume)
│   ├── SUB/           # Subagents (skills-subagents, subagent-execution-modes)
│   ├── TST/           # Testing & audit (atif-support, skill-commands-list-create-load-pull, skills-audit, simple-ollama-test)
│   ├── TUI/           # Terminal UI (better-completion-commands, login-with-openai-codex, nanocoder-tui)
│   ├── WEB/           # Web server (web-serve)
│   └── COD/           # Coding (improve-test-coverage, code-review-codex, research-coding-agents-session-log-optimizations)
├── evaluations/
├── issues/
├── research/
└── tools/
```

Each feature directory follows a strict 7-phase progression:

| Phase | File | Purpose |
|-------|------|---------|
| 1. Idea | `rough-idea.md` | Initial concept, motivation, source |
| 2. Requirements | `requirements.md` | Acceptance criteria and constraints |
| 3. Research | `research/001-*.md` … `00N-*.md` | Codebase exploration, gap analysis, prior art |
| 4. Design | `design.md` | Architecture, data flow, interfaces |
| 5. Plan | `plan.md` | Step-by-step implementation tasks |
| 6. Prompt | `PROMPT.md` | Executable agent instruction |
| 7. Summary | `summary.md` | Artifacts created, outcome, lessons |

This isn't documentation for humans. The `PROMPT.md` file is the executable instruction that gets injected directly into a spawned subagent's context when you run `/run <spec-name>` from the TUI or CLI. Specs that are in-progress at any phase are tracked in the specs index — the system knows which rooms are complete and which still need work.

### Pi-Go's MemPalace Integration

Pi-go's `mempalace.yaml` maps the entire project into a memory palace with 24 rooms, including the 7 feature spec rooms that represent the SPEC hierarchy:

```yaml
# mempalace.yaml (pi-go)
wing: pi-go
rooms:
  - name: cmd          # patterns: "cmd/**"
  - name: agent        # patterns: "internal/agent/**", "internal/subagent/**"
  - name: palace       # patterns: "internal/palace/**" + keywords: palace, miner, drawer, embedding
  - name: memory       # patterns: "internal/memory/**"
  - name: tui          # patterns: "internal/tui/**"
  - name: tools        # patterns: "internal/tools/**"
  - name: provider     # patterns: "internal/provider/**"
  - name: cli          # patterns: "internal/cli/**"
  - name: lsp          # patterns: "internal/lsp/**"
  - name: extension    # patterns: "internal/extension/**"
  - name: webserver    # patterns: "internal/webserver/**"
  - name: infra        # patterns: internal/{atif,audit,auth,config,guardrail,jsonrpc,logger,session,sop,mode}/**
  - name: docs         # patterns: "docs/**"
  - name: skills       # patterns: "skills/**"
  - name: specs        # patterns: "specs/**"
  - name: scripts      # patterns: "scripts/**", "hack/**"
  - name: research     # patterns: "research/**"
  # ── Feature Spec Rooms ────────────────────────────────────────────
  - name: MEM          # patterns: "specs/features/MEM/**"
  - name: SOP          # patterns: "specs/features/SOP/**"
  - name: SUB          # patterns: "specs/features/SUB/**"
  - name: TOO          # patterns: "specs/features/TOO/**"
  - name: TST          # patterns: "specs/features/TST/**"
  - name: TUI          # patterns: "specs/features/TUI/**"
  - name: WEB          # patterns: "specs/features/WEB/**"
  - name: COD          # patterns: "specs/features/COD/**"
```

The parallel to Aristotle's technique is precise. The top-level categories (MEM, SOP, SUB, TST, TUI, WEB, COD) are **wings** — spatial regions you mentally walk through. Each feature directory is a **room**. The numbered research files are **stations** within that room, ordered for sequential traversal. And the phase progression (idea → requirements → research → design → plan → prompt → summary) is the **bi-directional walk** — you can navigate forward to build, or backward to audit.

The key design decision: specs are *not* in the agent's context window by default. They're retrieved on demand via the `/run` command. This is the same principle as Aristotle's method — you don't hold the entire palace in working memory. You *navigate* to the room you need.

Pi-go's MemPalace layer (`internal/palace/`) then stores observations about the codebase — decisions, bugs, features, discoveries, refactors — into a SQLite-backed drawer system with halls derived from observation types. The SPEC rooms in `mempalace.yaml` ensure that any code generated from a spec can be attributed to its wing and room.

## MemPalace: When You Name It What It Is

The [MemPalace](https://github.com/milla-jovovich/mempalace) project makes the metaphor explicit. It implements an AI memory system using literal memory palace terminology: Wings for top-level containers (people, projects), Rooms for subject divisions (auth, billing, deploy), Halls for standardized memory types (`hall_facts`, `hall_events`, `hall_discoveries`), and Tunnels for cross-wing connections.

The architecture is backed by a tiered retrieval stack:

- **L0 (Identity)**: ~50 tokens, always loaded
- **L1 (Critical Facts)**: ~120 tokens, loaded on wake-up
- **L2 (Room Recall)**: on-demand recent context
- **L3 (Deep Search)**: semantic queries across all stored memories

Their benchmark tells the real story: unfiltered semantic search across 22,000+ memories achieves 60.9% recall. Add wing filtering: +12%. Add wing + room filtering: **94.8%**. The hierarchical structure isn't decorative — it delivers a 34% retrieval improvement by narrowing the search space before the embeddings even fire.

This mirrors what memory athletes have known for millennia. You don't search your entire palace for where you stored "the capital of Mongolia." You walk to the geography wing, enter the Asia room, and it's *right there at station 13*.

## Caveman: Compression as Mnemonic Encoding

[Caveman](https://github.com/JuliusBrussee/caveman) takes a different route to the same destination. Instead of organizing where memories go, it compresses how they're encoded. The project reduces LLM token usage by ~75% through deliberate linguistic compression — dropping articles, particles, and filler while preserving technical elements (code blocks, URLs, paths, commands) verbatim.

A 706-token preferences file becomes 285 tokens. System prompts shrink by 35-60%. And the research backing it suggests brief responses may even *improve* accuracy by 26 percentage points on certain tasks.

This is the AI equivalent of what mnemonists call **encoding efficiency** — the Major System converts numbers to consonant sounds, PAO systems compress three data points into one image. You don't memorize "the forty-third president of the United States was George Walker Bush." You encode a single vivid image at station 43 in your number palace.

MemPalace actually attempted something similar with its AAAK compression dialect — entity codes and structural markers for lossy abbreviation. They were honest about the results: it regressed retrieval from 96.6% to 84.2%. Caveman's approach works better because it compresses *form* without touching *structure*, while AAAK compressed both.

## The Convergent Pattern

What Milla Jovovich's MemPalace, Caveman's compression, and pi-go's SPEC hierarchy have in common is not surface-level — it's architectural:

1. **Hierarchical spatial organization** — Wings/rooms/stations in MemPalace, category/feature/phase in pi-go, intensity levels in Caveman. Every system creates navigable depth.

2. **On-demand retrieval over total context** — None of these systems dump everything into the prompt. They all gate access: pi-go loads PROMPT.md only on `/run`, MemPalace searches within-wing first, Caveman compresses what does get loaded.

3. **Separation of encoding from storage** — Raw memories are preserved (MemPalace's "drawers", pi-go's research files), but what gets injected into working context is compressed and structured (MemPalace halls, pi-go's PROMPT.md). The original is always preserved; only the retrieval form is optimized.

4. **Bi-directional navigation** — Pi-go specs can be walked forward (idea→prompt) to build or backward (summary→idea) to audit. MemPalace supports chronological timelines and point-in-time queries. This is Aristotle's bi-directional walk, implemented in code.

5. **The two-level index** — Both pi-go (mempalace.yaml) and MemPalace use a two-level spatial index (wing→room) as a pre-filter *before* semantic search. This is the key insight: semantic similarity alone is insufficient; categorical hierarchy narrows the search space dramatically.

## What This Means for Your Agent Architecture

If you're building AI agents that persist across sessions, the evidence points toward a clear pattern: don't just vector-embed everything and hope cosine similarity saves you. Build a palace.

Concretely: organize agent knowledge into navigable hierarchies with 2-3 levels of categorical filtering *before* semantic search. Compress what enters the context window but preserve raw originals. And give your agent a map of the hierarchy so it can *navigate* rather than *search*.

The ancient memory athletes didn't have embeddings. They had architecture. Turns out, that's the part we were missing.

---

*References: [pi-go](https://github.com/dimetron/pi-go) | [MemPalace](https://github.com/milla-jovovich/mempalace) | [Caveman](https://github.com/JuliusBrussee/caveman) | [Art of Memory Forum](https://forum.artofmemory.com/t/memory-palaces-using-the-alphabet-letters/43870) | [Aristotle's Nuclear Alphabet](https://youtu.be/_3N2i73LKt0)*
[Aristotle's Nuclear Alphabet](https://youtu.be/_3N2i73LKt0)*
