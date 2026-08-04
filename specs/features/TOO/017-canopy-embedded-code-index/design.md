# Design — Canopy embedded code index

## Shape

```
internal/codeindex/          NEW — owns the canopy index lifecycle
    index.go                 Index type, lazy build, cache, refresh
    query.go                 Definitions / References / Symbols / CallGraph / Impact / Complexity
    config.go                Config + root resolution + ignore policy

internal/tools/codeindex.go  NEW — the code_* ADK tools
internal/tools/lsp.go        TRIMMED — navigation tools removed, diagnostics/hover/actions kept
internal/lsp/                UNCHANGED — still owns servers, diagnostics, code actions
internal/cli/cli.go          WIRING — setupCodeIndex alongside setupMemory/setupPalace
```

## Why embedded, not MCP

Measured (see `research/canopy-evaluation.md`): 732 ms in-process vs 2.3 s via CLI, 10
transitive packages, no cgo. The MCP route needs a supervised subprocess — the exact cost
we are removing from `internal/lsp` — and canopy's MCP server uses `Content-Length` framing
rather than the newline-delimited JSON the MCP stdio spec specifies, a mismatch that is
unresolved.

## Division of labour with LSP

The split is not arbitrary; it follows what a tree-sitter index can and cannot know.

| Question                       | Answered by | Why                             |
|--------------------------------|-------------|---------------------------------|
| Where is this defined?         | **canopy**  | syntactic — names and positions |
| Who references this?           | **canopy**  | syntactic                       |
| What symbols are here?         | **canopy**  | syntactic                       |
| Who calls this / blast radius? | **canopy**  | call graph over the index       |
| How complex is this?           | **canopy**  | AST metrics                     |
| **Does this compile?**         | **LSP**     | needs a type checker            |
| **What are the quick fixes?**  | **LSP**     | needs a type checker            |
| What type is this expression?  | **LSP**     | needs a type checker            |

Canopy is always available. LSP is best-effort and may be absent — which is already true
today, it is simply no longer catastrophic.

## Index lifecycle

```
        first code_* tool call
                 │
                 ▼
      ┌──────────────────────┐   hit    ┌──────────────┐
      │ load .pi-go/canopy.idx├─────────►│ freshness    │
      └──────────┬───────────┘          │ check        │
                 │ miss                 └──────┬───────┘
                 ▼                       stale │ fresh
        ┌────────────────┐                     │    │
        │ Builder.Build  │◄────────────────────┘    │
        │ Path(root)     │   incremental            ▼
        └───────┬────────┘                    serve from memory
                ▼
         save + hold in memory
```

Rules:

- **Lazy.** Nothing is indexed until a `code_*` tool is called. Agent startup is untouched.
- **Cached** at `.pi-go/canopy.idx`, next to `palace.db`.
- **Incremental.** `pkg/index` exposes `LoadLenient`, `NewPartialIndex` and freshness
  helpers (`ComputeConfigHashes`); use them rather than rebuilding.
- **One build at a time.** A `singleflight`-style guard, so three concurrent tool calls on a
  cold index produce one build, not three.
- **Failure is not fatal.** A build error disables the `code_*` tools with a logged warning,
  matching `setupPalace`.

### Root and ignores

Root is the project directory (same value `setupPalace` uses). Ignores are the union of:

- canopy's workspace matcher (`index.NewBuilderWithWorkspaceIgnores`) — handles
  `.gitignore`, `.canopyignore`, `.graftignore`
- an explicit extra list for vendored trees: `tmp/`, `vendor/`, `node_modules/`, `.git/`,
  `dist/`, `build/`, `target/`

The second list is not optional. Indexing this repo's root did not finish in 120 s because
`tmp/` holds vendored checkouts including canopy itself.

## Package API

```go
package codeindex

// Index is a lazily-built, cached structural index of a project.
// The zero value is unusable; construct with New.
type Index struct{ /* root, cachePath, mu, idx, graph, cfg */ }

func New(cfg Config) *Index

// Ensure builds or loads the index if needed. Safe for concurrent use; concurrent
// callers on a cold index share one build.
func (ix *Index) Ensure(ctx context.Context) error

func (ix *Index) Definitions(ctx context.Context, name string, regex bool) ([]Definition, error)
func (ix *Index) References(ctx context.Context, name string, regex bool) ([]Reference, error)
func (ix *Index) Symbols(ctx context.Context, file string) ([]Symbol, error)
func (ix *Index) CallGraph(ctx context.Context, root string, depth int, reverse bool) (*CallTree, error)
func (ix *Index) Impact(ctx context.Context, changed []string, maxDepth int) (*ImpactReport, error)
func (ix *Index) Complexity(ctx context.Context, minCyclomatic int) (*ComplexityReport, error)

func (ix *Index) Close() error
```

Canopy types (`model.Symbol`, `xref.Definition`, …) are **not** exposed on this surface. The
package returns its own structs. That keeps a canopy upgrade from rippling into
`internal/tools` and lets the backend be swapped without touching tool code.

## Tools

| Tool              | Backed by                               | Replaces                              |
|-------------------|-----------------------------------------|---------------------------------------|
| `code_definition` | `xref.Graph.FindDefinitions`            | `lsp_definition`                      |
| `code_references` | index references                        | `lsp_references`                      |
| `code_symbols`    | `model.Index` file summaries            | `lsp_symbols`, `lsp_workspace_symbol` |
| `code_callgraph`  | `xref` traversal, `reverse` for callers | — new                                 |
| `code_impact`     | `pkg/impact.AnalyzeWithGraph`           | — new                                 |
| `code_complexity` | `pkg/complexity.Analyze`                | — new                                 |

Retained on the LSP path: `lsp_diagnostics`, `lsp_code_action`, `lsp_hover`.

Tool results follow the existing convention in `internal/tools/lsp.go` — a structured map
plus a styled display string compatible with `formatToolResult`.

### Error surface

If `Ensure` fails, tools return an error naming the cause and what to do, in the spirit of
the `pi memory mine` Ollama message. They do **not** return an empty result set: "no
references found" and "the index is broken" must not look identical to the agent. This is
the current failure mode of the LSP manager when a server is missing, and it is the reason
capability loss goes unnoticed today.

## Config

```go
// internal/config/config.go
type CodeIndexConfig struct {
    Enabled   *bool    `json:"enabled,omitempty"`    // default true
    CachePath string   `json:"cache_path,omitempty"` // default .pi-go/canopy.idx
    Exclude   []string `json:"exclude,omitempty"`    // merged with the built-in vendored list
}
```

Wired in `runRoot` next to `setupMemory` / `setupPalace`:

```go
codeIdx, closeCodeIndex := setupCodeIndex(cfg, cwd)
defer closeCodeIndex()
if codeIdx != nil {
    if t, err := tools.CodeIndexTools(codeIdx); err == nil {
        coreTools = append(coreTools, t...)
    }
}
```

## Dependency

```
require m31labs.dev/canopy v0.18.0
```

Pinned. Consequences to state in the PR: ~30 MB binary growth (tree-sitter grammars, 206
languages), 10 transitive packages, MIT, no cgo.

## Risks

| Risk                                       | Mitigation                                                                                    |
|--------------------------------------------|-----------------------------------------------------------------------------------------------|
| Losing type errors by over-trusting canopy | `lsp_diagnostics` explicitly retained; requirements forbid removing it                        |
| Index build blows up on a huge repo        | Lazy build, vendored-tree excludes, `GOMEMLIMIT`/`GTS_MAX_CONCURRENT` are respected by canopy |
| Stale index returns wrong line numbers     | Freshness check on load; incremental refresh on change                                        |
| Canopy API churn (pre-1.0, at v0.18.0)     | Pinned version; canopy types never escape `internal/codeindex`                                |
| Silent degradation                         | Errors are surfaced, never flattened into empty results                                       |

## Deliberately not designed here

- **AST chunking for the palace miner.** Canopy's chunker is in `internal/chunk` and cannot
  be imported. Symbol-boundary chunking is derivable from `model.Symbol` line ranges, but
  that is a memory-quality change needing its own recall measurement against the current
  70% recall@1 baseline. Separate spec.
- **`gts_context` / `pkg/contextbundle`.** The package exports `Greet(name string) string`,
  which suggests it is a stub. Not built on.
- The other 25 canopy analyses.
