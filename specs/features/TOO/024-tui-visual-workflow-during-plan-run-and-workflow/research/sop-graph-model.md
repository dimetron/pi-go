# Research: SOP Compiled Graph Data Model

## Compiled struct — internal/sop/compile.go:32-39

```go
type Compiled struct {
    Definition *Definition
    Edges      []workflow.Edge
    Nodes      map[string]workflow.Node   // indexed by stage id, and "<id>.review" for a stage's review checkpoint
    Order      []string
}
```

- `Definition` — the parsed SOP definition.
- `Edges` — the compiled `[]workflow.Edge` list, built by `wire` (compile.go:160).
- `Nodes` — map keyed by stage ID and by `"<id>.review"` for review checkpoints.
- `Order` — stage IDs in build order, including `"<id>.review"` entries.

`Compile(def *Definition, factory NodeFactory) (*Compiled, error)` (compile.go:44) lints
first, then builds nodes via `buildStage` (63) and edges via `wire` (103).
`Compiled.Workflow()` (219) assembles edges into a `*workflow.Workflow`.

## Stage struct — internal/sop/schema.go:67-108 (graph-relevant fields)

- `ID string` (68), `Description string` (69)
- `Kind string` (74) — "agent" or "function"; empty resolves via `EffectiveKind()` (199):
  agent if `Agent` set or `FanOut != nil`, else function.
- `Agent string` (75)
- `FanOut *FanOut` (84), `Join string` (85)
- `Review *Review` (87)
- `Routes map[string]string` (91) — output value → target stage ID
- `LoopBack string` (92), `MaxCycle int` (93)
- `OnFail string` (96) — "abort", "retry", or a stage ID
- `Timeout Duration` (98), `Retry *Retry` (99)
- `Gate string` (104), `GatesFrom string` (105), `OutputSchema string` (106)
- `Next string` (107)

`Definition` (schema.go:23-31): `SOP`, `Version`, `Description`, `Workspace`, `Defaults`,
`Preflight []Stage`, `Stages []Stage`. `AllStages()` (192) = Preflight + Stages.

## workflow.Edge — google.golang.org/adk/v2/workflow (v2.2.0)

```go
type Edge struct {
    From  Node
    To    Node
    Route Route
}
```

- `Node` interface exposes `Name()`, `Description()`, `Config()`, `Run`, etc.
  `BaseNode.Name()` returns the node's name.
- `Route` interface: `Matches(event *session.Event) bool`. `StringRoute` matches on a
  string value; `Default` matches when no concrete route matches.
- `Start` sentinel node is the entry point.

Edge building (`workflow/edgebuilder.go`): `NewEdgeBuilder()`, `Add(from,to)` (unconditional,
Route nil), `AddRoute(from,to,route)`, `AddRoutes(from, map[string]Node)` (one edge per entry
with `StringRoute`), `AddFanOut`, `AddFanIn`, `Build()`.

In `sop`'s `wire` (compile.go:103-165):
- Entry: `b.Add(workflow.Start, c.Nodes[stages[0].ID])` (110).
- Review path: `b.Add(from, r)` then `from = r` (116-117) — stage → review → successor.
- Routes: `b.AddRoutes(from, routes)` (130).
- LoopBack: `b.AddRoute(from, to, workflow.StringRoute(RecheckSignal))` where
  `RecheckSignal = "RECHECK"` (140, const at compile.go:12).
- Next: `b.Add(from, to)` (146).
- OnFail (non-abort/retry): `b.AddRoute(from, to, workflow.StringRoute("FAIL"))` (156).

## Compiling plan.sop.yaml / run.sop.yaml

- **Loading:** `LoadDefinition(workDir, name string) (*Definition, error)` (load.go:25-36).
  Resolution (`resolveDefinition`, load.go:52-71): project `.pi-go/sops/<name>.sop.yaml` →
  global `~/.pi-go/sops/<name>.sop.yaml` → embedded default (`//go:embed`). Parses with
  `ParseDefinition` (KnownFields(true)) and lints with `LintDefinition`.
- **Factory:** `Compile(def, factory NodeFactory)`. `NodeFactory` interface (compile.go:22-29):
  `AgentNode`, `FunctionNode`, `ReviewNode`.
- **DescribeFactory** (describe.go:22-38) builds nodes that carry identity/config but do no
  work; `Run` returns "description only" error. It compiles **without a provider, a worktree,
  or a running agent** — ideal for the TUI to render the graph structure.
- `Compiled.Describe()` (describe.go:81-116) renders stages and edges as text.

## Exact stage IDs and edges

### plan.sop.yaml
Stages: `clarify`, `research`, `design`, `outline`, `plan`, `prompt`, `manifest`.
Review checkpoints on `clarify`, `design`, `outline`, `plan` — **`clarify`, `design`
and `outline` are `kind: human`** ("Approve to continue to research?", "Approve the
design?", "Approve the outline before expanding it into the plan?"); only
`plan.review` is `kind: agent` (`spec-reviewer`, `verdict_schema: Verdict`).

`research` is a fan-out stage (`over: research_angles`, `agent: explore`,
`max_concurrency: 4`, `isolation: sub_branch`, `join: research_summary`) — the only
plan stage that is not a single `plan`-agent turn.

Edges (verified via `Compiled.Describe()`):
```
START -> clarify
clarify -> clarify.review
clarify.review -> research
research -> design
design -> design.review
design.review -> outline
outline -> outline.review
outline.review -> plan
plan -> plan.review
plan.review -> plan [FAIL]
plan.review -> prompt [PASS]
prompt -> manifest
```

### run.sop.yaml
Stages: preflight `validate_spec`; then `slices`, `gates`, `verify`, `repair`, `merge`, `summary`.
No review checkpoints.

Edges (verified):
```
START -> validate_spec
validate_spec -> slices
slices -> gates
slices -> repair [FAIL]
gates -> verify
gates -> repair [FAIL]
verify -> merge [PASS]
verify -> repair [FAIL]
repair -> verify [RECHECK]
merge -> summary
```

Note: `validate_spec` has `on_fail: abort` (run.sop.yaml:30), which produces **no** edge
(compile.go:151 skips "abort").
