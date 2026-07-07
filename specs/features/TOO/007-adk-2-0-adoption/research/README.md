# Research

> Objective fact-finding for the `google.golang.org/adk v1.4.0 → v2.0.0` migration in pi-go.
> No design opinions, no migration plan — see `design.md` and `plan.md` for those.

## Files

| File                                       | Contents                                                                                                                                                            |
|--------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`v1-usage-surface.md`](v1-usage-surface.md) | How pi-go's v1.4.0 code uses the ADK today. Import inventory, sub-package usage, type references, mock context fakes, and existing build/test/lint infrastructure. |
| [`v2-api-delta.md`](v2-api-delta.md)         | The v1.4.0 → v2.0.0 API delta. Module path change, type changes (`agent.Context` unification, callback signatures, `functiontool.Func`, `session.NewEvent`), transitive dep changes, and the `agent.StrictContextMock` story. |
| [`call-sites.md`](call-sites.md)             | Per-file/per-line inventory of every source-level change required. Import-path-only edits, callback-parameter-type edits, mock method additions, and the "things that don't change" list. |
| [`adk-docs-addendum.md`](adk-docs-addendum.md) | Additional findings from the official ADK 2.0 documentation site (`adk.dev`). Surfaces 5 new `Event` fields, `llmagent.Config` field-by-field verification, and the 5 official Go breaking changes (with their zero impact on pi-go). |

## Headline findings (TL;DR)

1. **Module path change.** Every `google.golang.org/adk/...` import becomes `google.golang.org/adk/v2/...`. ~85 files touched by this change alone.
2. **No new v2 features used.** Per A1, this is a pure migration. We do not adopt the graph workflow engine, collaboration-agent modes, or `StrictContextMock`.
3. **One real API surface change affects our code:** the callback and `functiontool.Func` first-argument type widens from `agent.CallbackContext` / `agent.ToolContext` / `tool.Context` to the new unified `agent.Context`. ~60 function literals need a one-token retype.
4. **One real API change does NOT affect our code:** `session.NewEvent` now takes a `context.Context` as its first argument — but pi-go never calls `session.NewEvent` directly (0 hits), so this is a no-op for us.
5. **`tool.Context` alias is removed in v2.0.0.** We don't use it as a type name anywhere, so this is also a no-op.
6. **Three hand-rolled mocks need method additions** (`mockToolCtx` in three test files). Per A5, we add the full v2.0.0 `agent.Context` method set so the mocks satisfy `var _ agent.Context = (*Y)(nil)`.
7. **Event struct gained 5 new fields** (`IsolationScope`, `Routes`, `RequestedInput`, `Output`, `NodeInfo`) per the official 2.0 docs. Our `internal/session/store.go` uses JSON-blob storage which handles the new fields automatically. **No code change needed** — verified.
8. **All 9 `llmagent.Config` fields we set are preserved in v2.0.0.** No field renames or removals — verified field-by-field.
9. **No custom `agent.Agent` impls in pi-go.** We only use `llmagent.New(llmagent.Config{…})`. The v2.0.0 "Workflow Graph engine bypasses custom `Run` overrides" change is a no-op for us.
10. **All 3 of our `recover()` callsites are at outer boundaries** (TUI agent loop, ACP prompt handler, compactor stage) — **not** inside tool bodies. The v2.0.0 "don't blanket-recover in tool bodies" guidance is satisfied.
11. **No direct `context.session.events.append` or `Events().Append` calls in production code.** The v2.0.0 "don't bypass framework to manually append events" change is a no-op for us.
12. **Transitive dependencies:** the v2.0.0 ADK adds `gorm.io/gorm`, `glebarez/sqlite`, `mitchellh/mapstructure` as new indirect deps. None conflict with our existing pins. `go mod tidy` will absorb them.
13. **Go version:** v2.0.0 requires Go 1.25+; our `go.mod` is 1.26.4, which is above the floor. No change required.
14. **Known baseline test failure:** `internal/tui.TestCommitCommand_ConfirmCommits` fails on trunk due to a missing 1Password CLI agent. Unrelated to ADK, but A8(a)=iii requires this migration PR to fix or reliably isolate it so the final `go test ./...` gate is green.

## Verification

All findings in these research files are sourced from:

- The v1.4.0 source in the local module cache: `/Users/dimetron/go/pkg/mod/google.golang.org/adk@v1.4.0/...`
- The v2.0.0 source downloaded from `https://proxy.golang.org/google.golang.org/adk/v2/@v/v2.0.0.zip`
- The v2.0.0 release notes from `https://api.github.com/repos/google/adk-go/releases/tags/v2.0.0`
- The v2.0.0 migration guide at `https://raw.githubusercontent.com/google/adk-go/v2.0.0/README-v2.md`
- The official ADK 2.0 documentation site: `https://adk.dev/llms.txt` and the linked pages
  (2.0 overview, function tools, context, events, sessions, callbacks, etc.)
- `ripgrep` queries run against `/Users/dimetron/p6s/pi-dev/pi-go` (the current repo state)
- The local `Makefile`, `go.mod`, and the `go test ./internal/...` baseline run
