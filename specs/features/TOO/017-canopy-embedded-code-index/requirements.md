# Requirements

## Task Scope

Embed [canopy](https://github.com/odvcencio/canopy) (`m31labs.dev/canopy`) as an in-process
structural code index in pi-go, replacing the parts of `internal/lsp` that do not require a
type checker, and exposing new structural tools (call graph, blast radius, complexity) that
LSP cannot provide.

Not in scope: retiring `internal/lsp` entirely, MCP-subprocess integration, or changing the
memory palace's chunking (tracked as follow-ups below).

## Questions & Answers

**Q1 — Embed the library, or run `canopy mcp` as a subprocess?**
A: **Embed the library.** Measured: 732 ms for a full in-process index of `internal/`
(vs 2.3 s via CLI), only 10 transitive packages, `CGO_ENABLED=0`, typed Go structs
instead of JSON round-trips. The MCP route additionally has an unresolved framing
mismatch (canopy uses `Content-Length`, MCP stdio spec uses newline-delimited JSON) and
a subprocess to supervise — which is the thing we are trying to get away from.

**Q2 — Does this replace `internal/lsp`?**
A: **No.** Canopy cannot type-check. `lsp_diagnostics` and `lsp_code_action` must keep
working through language servers. Canopy becomes the always-available layer; LSP becomes
an optional enhancement for the two compiler-dependent tools.

**Q3 — What happens to the existing `lsp_*` navigation tools?**
A: `lsp_definition`, `lsp_references`, `lsp_symbols`, `lsp_workspace_symbol` are replaced by
`code_*` equivalents backed by canopy. They must work with no language server installed —
that is the point. `lsp_hover` is retained on the LSP path because canopy has no type
information; a canopy-backed signature lookup is a degraded substitute, not a replacement.

**Q4 — Which new capabilities are exposed as tools?**
A: Three, chosen because they answer questions the agent actually asks and LSP cannot:
`code_callgraph` (who calls this / what does this call), `code_impact` (blast radius of a
change), `code_complexity` (which functions are risky). Everything else canopy offers
(coupling, smells, capa, hotspot, …) is deferred until there is demand.

**Q5 — Where does the index live and when is it built?**
A: Cached at `.pi-go/canopy.idx`, built lazily on first tool use, refreshed incrementally.
It must never be built eagerly at agent startup — indexing this repo's root did not
complete in 120 s because it walks `tmp/`.

**Q6 — What is indexed?**
A: The project root, honouring `.gitignore` plus canopy's `.canopyignore`/`.graftignore`.
`tmp/`, `vendor/`, and other vendored trees must be excluded by default. The miner's
existing skip list (`internal/cli/memory_mine.go:skipDirNames`) is the reference for what
"vendored" means here.

**Q7 — Is ~30 MB of binary growth acceptable?**
A: Yes. pi-go's binary is already ~102 MB; the tree-sitter grammars take it to ~132 MB. The
alternative is per-language server installs, which is a worse user experience. This must
be stated in the PR description, not discovered.

**Q8 — Can this be turned off?**
A: Yes. Config `code_index.enabled` (default true). When disabled, the `code_*` tools are not
registered and nothing is indexed.

## Acceptance Criteria

### Functional

- [ ] `code_definition`, `code_references`, `code_symbols` return correct results on this repo
  **with no language server installed**
- [ ] `code_callgraph` traverses both directions (callers and callees)
- [ ] `code_impact` reports blast radius for a changed symbol
- [ ] `code_complexity` reports per-function cyclomatic/cognitive metrics
- [ ] `lsp_diagnostics` and `lsp_code_action` continue to work unchanged
- [ ] With `code_index.enabled=false`, no `code_*` tool is registered and no index is built

### Non-functional

- [ ] `go build` succeeds with `CGO_ENABLED=0` — the `go install` property is preserved
- [ ] First index of `internal/` completes in under 5 s
- [ ] Cached index is reused across invocations; unchanged files are not reparsed
- [ ] Indexing never walks `tmp/`, `vendor/`, `node_modules/`, or `.git/`
- [ ] Index build failure degrades to "tools unavailable" with a warning — never a fatal error,
  matching how `setupMemory`/`setupPalace` already behave

### Testing

- [ ] Unit tests for the index wrapper with a fixture tree (not this repo)
- [ ] Each `code_*` tool has a test asserting shape and error handling
- [ ] A test asserts the index is not rebuilt when nothing changed
- [ ] Tests must not depend on network or on a language server being installed

## Constraints

- **No cgo.** Any change that forces `CGO_ENABLED=1` is rejected.
- **No eager indexing at startup.** Agent start latency must not regress.
- **No silent capability loss.** If the index cannot be built, say so; do not return empty
  results that look like "no matches" (this is the existing failure mode of the LSP manager
  when a server is missing, and it should not be reproduced).
- Canopy is pinned to an explicit version; `@latest` is not used.

## Follow-ups (explicitly out of scope)

1. **AST-boundary chunking for the memory miner.** `internal/palace/miner.go` chunks by fixed
   character windows, cutting through function bodies. `model.Symbol` carries
   `File`/`StartLine`/`EndLine`, so symbol-boundary chunking is derivable from the index —
   but canopy's own chunker is in `internal/chunk` and not importable. Needs its own spec and
   a recall measurement against the current 70% recall@1 baseline.
2. **Retiring `internal/lsp` hover.** Depends on whether canopy signature lookup proves good
   enough in practice.
3. **The remaining 25 canopy analyses** (coupling, smells, capa, hotspot, testmap, …) as
   tools or as a `pi analyze` command.
