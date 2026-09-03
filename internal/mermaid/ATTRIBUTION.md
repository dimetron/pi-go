# Attribution — internal/mermaid

The rendering engine in this package is adapted from
**[aaronsb/mmaid-go](https://github.com/aaronsb/mmaid-go)** (MIT), which is
itself a Go reimplementation of
**[fasouto/termaid](https://github.com/fasouto/termaid)** (MIT, Python).

The upstream MIT licence is kept verbatim in `LICENSE.upstream`.

| | |
|---|---|
| Upstream | `github.com/aaronsb/mmaid-go` |
| Commit | `2d6873d4bfc522c737c191aeeb69dfaa2911b958` |
| Imported | 2026-08-28 |

## Why it is copied rather than imported

The upstream engine lives entirely under its own `internal/`, so a dependency
can call exactly one function — `Render(source, opts...)`. Everything this
package needs beyond that is unreachable from outside the module: the width
override, the styled-cell accessor the TUI colors with its own palette, and the
fixes listed below. Copying is what makes those possible; it is not a
convenience.

Upstream was last pushed 2026-08-16 and has a single star, so there is no
maintenance stream to track. Re-syncing means diffing against the commit above
by hand.

## Changes made here

Structure:

- `cmd/` and the JSON `ingest/` package dropped — both are CLI-only surface.
- Import paths rewritten to `github.com/dimetron/pi-go/internal/mermaid/...`,
  root package renamed `mmaid` → `mermaid`.

Additions:

- **`WithWidth(cols)`** — upstream exposed the width override only through its
  CLI, leaving a library caller with the terminal's own width. Wrong for any
  pane narrower than the terminal.
- **`diagram.WithWidthScope`** — the width is package-level state that fourteen
  renderers read directly. The scope pins it under a mutex so `WithWidth`
  reaches all of them and concurrent renders cannot see each other's value.
  Threading the width through each renderer as a parameter is the real fix and
  remains undone.
- **`RenderCells`** — returns the diagram as styled cells so a caller can apply
  its own palette instead of the package's baked-in ANSI themes.

Bug fixes, each with a regression test in the corpus suite:

- **Non-determinism in column-slack distribution** (`layout/grid.go`).
  `ColWidths` is a map; the leftover columns went to whichever entries map
  iteration reached first, so the same diagram rendered differently on
  successive calls. In a TUI that re-renders per frame this reads as flicker.
- **Non-determinism in subgraph edge anchoring** (`routing/router.go`).
  Members equidistant from a subgraph's center tied, and the tie was broken by
  map order — an arrow left `A1` on one render and `A2` on the next.
- **Panic on nested `block`** (`diagram/blockdiagram.go`). A nested
  `block ... end` produced grid entries the column pass never sized, and the
  position pass indexed past the end of `colX`.
- **ASCII mode leaked Unicode** (`renderer/shapes.go`, `renderer/charset.go`,
  `diagram/quadrant.go`, `diagram/gantt.go`). Shape indicators and dashed
  lines were hardcoded to their Unicode glyphs, so `--ascii` emitted
  `◦ ⊂ ‖ ◇ ⬡ ○ ◎ ┄ ┆ ─` regardless. Indicators now come from the charset.

## Test corpus

`testdata/corpus/` holds 437 deduplicated inputs, and `testdata/golden/` the
approved output for each at width 100:

- **358** `mermaid-example` fences scraped from the 17 upstream mermaid syntax
  docs that correspond to a supported diagram type
  (`mermaid-js/mermaid@develop:docs/syntax`).
- **80** `.mmd` fixtures from `fasouto/termaid`'s test suite.

Only the inputs were taken. Upstream's expected outputs are the Python
renderer's and do not match this engine byte-for-byte, so every golden here was
generated and reviewed locally.

Regenerate with `go test ./internal/mermaid -update`, then read `git diff`
before committing: a golden is an approval, not a snapshot taken on trust.
