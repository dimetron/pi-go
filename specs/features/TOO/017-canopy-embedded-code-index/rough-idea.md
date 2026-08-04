# Rough Idea — Canopy as an embedded code index

## Source

User request: *"i would prefer to use built in canopy instead of external lsp"*, followed by
*"can it be embedded?"*.

[Canopy](https://github.com/odvcencio/canopy) (`m31labs.dev/canopy`) is a structural code
analysis toolkit built on tree-sitter: AST indexing, symbol/reference search, call graphs,
complexity and coupling metrics, and an MCP server, across 206+ languages.

## Motivation

pi-go's code intelligence today is `internal/lsp` — 4,729 lines that spawn and manage external
language servers (gopls, ruff, …) over stdio. Three problems:

1. **It needs servers installed.** No gopls, no Go navigation. The manager degrades to a
   silent no-op, so the agent simply loses the capability without being told.
2. **It is per-language.** Every new language means another server, another config entry,
   another process to supervise.
3. **There is no repo-wide index.** Every question is answered by asking a server about one
   file. There is no artifact describing the codebase as a whole — no call graph, no blast
   radius, no "what calls this".

Canopy inverts all three: one pure-Go dependency, one index, every language, no daemon.

## The catch, up front

Canopy parses; it does not type-check. It cannot produce `lsp_diagnostics` or
`lsp_code_action`. That is not a gap to be closed later — a tree-sitter index has no type
information by construction.

This came up concretely while evaluating it. A concurrent edit in this repo broke with:

```
internal/extension/skills.go:199:16: assignment mismatch: 2 variables
    but parseSkillContent returns 3 values
```

That file is syntactically valid. Canopy indexes it with `errors=0` and reports nothing.
`internal/lsp/hooks.go` populates `lsp_diagnostics` after edits, which is how the agent
notices it broke the build.

So the idea is **not** "replace LSP". It is "make canopy the always-available structural
layer, and keep LSP for the two things only a compiler can do".

## What this unlocks beyond parity

- Call graph, blast radius (`impact`), dead code, reachability
- Complexity / coupling / type metrics as agent-visible tools
- A cached repo index that other subsystems can build on — notably the memory palace, whose
  miner currently chunks source by fixed character windows

## Open questions for requirements

- Embed the library, or run `canopy mcp` as a subprocess?
- Which existing `lsp_*` tools get retired, renamed, or kept?
- Where does the index live, and when is it refreshed?
- Is ~30 MB of extra binary acceptable?
