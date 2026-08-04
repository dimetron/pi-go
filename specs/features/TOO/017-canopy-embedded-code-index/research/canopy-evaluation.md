# Research — Canopy evaluation

All figures measured on an M2 Max (12 cores) against this repository, 2026-08-04.
Canopy `v0.18.0`, `tmp/canopy` checkout at the same version.

## 1. Is it embeddable?

**Yes.** `pkg/index` and `pkg/xref` do not import `canopy/internal/`, so they are reachable
from an external module. License is MIT. Published on the Go proxy up to `v0.18.0`.

Verified in an isolated scratch module:

```go
idx, _ := index.NewBuilder().BuildPath("<repo>/internal")
g, _ := xref.Build(idx)
defs, _ := g.FindDefinitions("AddDrawer", false)
```

```
in-process index: files=478 symbols=7976 elapsed=732ms
FindDefinitions(AddDrawer) -> 2
  palace/drawer_service.go:39 AddDrawer
  palace/palace.go:122 AddDrawer
```

Both definitions resolved correctly (the `DrawerService` method and the `Palace` facade
method).

### Cost of embedding

| Property                                            | Value                                           |
|-----------------------------------------------------|-------------------------------------------------|
| Transitive packages via `pkg/index` + `pkg/xref`    | **10**                                          |
| cgo required                                        | **No** — builds with `CGO_ENABLED=0`            |
| Binary growth                                       | ~30 MB (tree-sitter grammars for 206 languages) |
| Heavy CLI deps (7zip, pdf, brotli, archives, cobra) | **Not** reachable from the library packages     |

The `CGO_ENABLED=0` result is the important one: it preserves pi-go's property that
`go install` works with nothing but a Go toolchain.

### Speed

| Mode                                        | Result                                           |
|---------------------------------------------|--------------------------------------------------|
| In-process `BuildPath("./internal")`        | 478 files, 7,976 symbols, **732 ms**             |
| CLI `canopy index build ./internal --out …` | 477 files, 6,935 symbols, **2.3 s**, 13 MB cache |

In-process is faster because nothing is serialized to disk. (The symbol-count difference is
the CLI's default ignore handling; not investigated further.)

Indexing the **repo root** did not finish within 120 s — it walks `tmp/`, which contains
vendored checkouts. Scoping matters; see design.

## 2. Capability mapping vs `internal/lsp`

pi-go exposes 7 LSP tools (`internal/tools/lsp.go:212`).

| Current tool           | Canopy                             | Notes                                                                                  |
|------------------------|------------------------------------|----------------------------------------------------------------------------------------|
| `lsp_references`       | ✅ `xref` / `gts_refs`              | Verified: found all `AddDrawer` call sites with file:line:col                          |
| `lsp_symbols`          | ✅ `model.Index` file summaries     |                                                                                        |
| `lsp_workspace_symbol` | ✅ index-wide symbol search         |                                                                                        |
| `lsp_definition`       | ✅ `xref.Build` graph, import-aware |                                                                                        |
| `lsp_hover`            | ⚠️ partial                         | `model.Symbol` has `Signature`/`Receiver`; no inferred types, no resolved doc comments |
| `lsp_diagnostics`      | ❌ **none**                         | tree-sitter reports *parse* errors only                                                |
| `lsp_code_action`      | ❌ **none**                         | no quick fixes                                                                         |

### Why diagnostics cannot be recovered

`canopy analyze` looks like it might cover this, but every subcommand is structural —
`complexity`, `coupling`, `duplication`, `smells`, `boundaries`, `capa`, `lint`, `check`.
These are lint-style rules over an AST index. There is no type checker in canopy.

Concrete failure observed during evaluation: `internal/extension/skills.go` had
`assignment mismatch: 2 variables but parseSkillContent returns 3 values`. Syntactically
valid, so canopy's index reports `errors=0`. Only a compiler catches it.

## 3. What canopy adds that LSP cannot

`canopy mcp` exposes 34 tools. Grouped:

- **Navigation**: `gts_refs`, `gts_scope`, `gts_grep`, `gts_query`, `gts_files`, `gts_map`, `gts_stats`
- **Graph**: `gts_callgraph`, `gts_deps`, `gts_bridge`, `gts_impact`, `gts_dead`, `gts_reachability`
- **Metrics**: `gts_complexity`, `gts_coupling`, `gts_types`, `gts_smells`, `gts_risk`, `gts_hotspot`, `gts_similarity`
- **Gates**: `gts_check`, `gts_lint`, `gts_boundaries`, `gts_guardrails`
- **Change**: `gts_diff`, `gts_drift`, `gts_review`
- **Security**: `gts_capa` (MITRE ATT&CK mapping), `gts_reachability`
- **Testing**: `gts_testmap`
- **Retrieval**: `gts_chunk`, `gts_context`
- **Refactor**: `gts_refactor` (dry-run by default; needs `--allow-writes`)
- **Multi-repo**: `gts_services`

## 4. Library vs MCP subprocess

Both work. Verified `go run m31labs.dev/canopy/cmd/canopy@v0.18.0 mcp` completes an
`initialize` handshake and returns 34 tools.

**Framing gotcha:** canopy's MCP server uses **`Content-Length` framing** (LSP-style,
`internal/mcp/server.go:236`), not the newline-delimited JSON that the MCP stdio spec uses.
Two newline-delimited handshake attempts returned nothing before this was found. Any MCP
integration must confirm pi-go's client speaks the same framing — this was **not** verified.

`go run` also pays a cold compile (~2-4 min) on first use, so a real config would need an
installed binary.

## 5. Which capabilities are importable

Available under `pkg/` (importable):

`index`, `xref`, `scope`, `impact`, `complexity`, `coupling`, `typemetrics`, `smells`,
`testmap`, `similarity`, `boundaries`, `capa`, `risk`, `hotspot`, `query`, `refactor`,
`sarif`, `structdiff`, `contextbundle`, `lang`, `model`, `generated`, `ignore`, `roots`

Sample signatures:

```go
func index.NewBuilder() *Builder
func (b *Builder) BuildPath(path string) (*model.Index, error)
func xref.Build(idx *model.Index) (Graph, error)
func (g *Graph) FindDefinitions(pattern string, regexMode bool) ([]Definition, error)
func impact.AnalyzeWithGraph(idx *model.Index, graph xref.Graph, opts Options) (*Result, error)
func complexity.Analyze(idx *model.Index, root string, opts Options) (*Report, error)
func scope.BuildFileScope(...)
```

### Not importable

**`chunk` lives in `canopy/internal/chunk`.** The `gts_chunk` MCP tool is backed by
`internal/mcp/call_chunk.go`. So AST-boundary chunking is reachable only over MCP, not as a
library.

This matters: `internal/palace/miner.go` chunks source with `chunkText(text, size, overlap)`
— fixed character windows that cut through function bodies. AST-aligned chunks would likely
improve retrieval, but the chunker cannot simply be imported.

Mitigation: `model.Symbol` carries `File`, `StartLine`, `EndLine`, so symbol-boundary
chunking can be derived from the index directly without canopy's internal chunker. Untested.

Also note `pkg/contextbundle` exports `Greet(name string) string`, which suggests the package
is still a stub. Treat `gts_context` as MCP-only for now.

## 6. Version constraints

|                 | Version     |
|-----------------|-------------|
| canopy `go.mod` | `go 1.25.7` |
| pi-go `go.mod`  | `go 1.26.5` |

Compatible. Canopy depends on `github.com/odvcencio/gotreesitter v0.47.1`.
