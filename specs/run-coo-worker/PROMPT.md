# Coordinator/Worker backpressure

## Objective

Stop `/run` losing 39% of its subagent sessions to provider rate limiting. The
concurrency cap is per-process and the Coordinator/Worker SOP nests processes,
so the budget multiplies with depth instead of dividing. Make one budget knob
compose correctly under nesting.

## Key Requirements

1. **Configurable budget** — `PI_SUBAGENT_CONCURRENCY` sets the per-process
   pool size. It is currently named in the env allowlist but read nowhere.
2. **Composes under nesting** — a spawned agent gets a share of its parent's
   budget, never a copy, so total in-flight agents converge with depth.
3. **Cannot deadlock** — no input produces a zero-sized pool.
4. **Rune-safe titles** — session titles truncate on a rune boundary.

## Acceptance Criteria

- Given `PI_SUBAGENT_CONCURRENCY=2`, when an orchestrator starts, then its pool
  size is 2.
- Given the variable is unset, malformed, negative or `0`, when an orchestrator
  starts, then the pool size is `DefaultPoolSize` and at least 1.
- Given a parent with budget N, when it spawns a child, then the child's
  environment carries `max(1, N/2)` exactly once.
- Given repeated nesting, when the budget is applied at each level, then it
  converges to 1 and never grows.
- Given a title whose byte cut falls inside a multi-byte rune, when it is
  stored, then it contains no U+FFFD and fits `MaxSessionTitle` bytes.

## Implementation Slices

1. **Env budget** — `ConcurrencyFromEnv` + wire into the pool, files:
   `internal/subagent/concurrency.go`, `orchestrator.go`, verify:
   `go test ./internal/subagent/`, parallel-safe: no
2. **Child propagation** — `ChildEnv` rewrites the budget, files:
   `internal/subagent/environ.go`, `spawner.go`, `spawner_codex.go`, verify:
   `go test ./internal/subagent/`, parallel-safe: no
3. **Lower the default** — 5 → 3, files: `internal/subagent/orchestrator.go`,
   verify: `go test ./internal/subagent/`, parallel-safe: yes
4. **Rune-safe titles** — files: `internal/session/store.go`, verify:
   `go test ./internal/session/`, parallel-safe: yes

## Execution Model

Coordinator → Worker → Verifier. Slices 1 and 2 are sequential; 3 and 4 are
independent and may run in parallel with them.

## Done Criteria

- [ ] `PI_SUBAGENT_CONCURRENCY=2` yields a pool of 2 — proven by test
- [ ] No env value produces a pool below 1 — proven by test over the bad inputs
- [ ] The child env carries the halved budget exactly once, not the parent's
- [ ] Repeated nesting converges to 1
- [ ] No stored title contains U+FFFD
- [ ] No slice left as a stub, TODO, or `panic("not implemented")`

## Gates

- **build**: `go build ./...`
- **vet**: `go vet ./...`
- **test**: `go test ./...`
- **lint**: `golangci-lint run ./internal/subagent/... ./internal/session/...`

## Reference

- Findings: `specs/run-coo-worker/recomendations.md`
- Design: `specs/run-coo-worker/design.md`
- Plan: `specs/run-coo-worker/plan.md`
- Deferred (session metadata): `specs/run-coo-worker/deferred.md`

## Constraints

- Do not add an adaptive rate-limit controller; the static budget is the fix.
- Do not extend `retryStream` past its pre-emission boundary — re-sending a
  committed stream duplicates tool calls.
