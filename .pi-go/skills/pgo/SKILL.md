---
name: pgo
description: >
  Profile-guided optimization (PGO) for the pi binary. Use when recording a new
  PGO profile (`make record-pgo`), when a build behaves differently after a
  profile is added or removed, when `default.pgo` is missing or stale, or when
  asked to optimize the pi binary with a CPU profile. Covers the go.dev/doc/pgo
  workflow: collecting a representative CPU profile, merging profiles, and
  building with PGO via `default.pgo` in the main package directory.
---

# PGO — Profile-Guided Optimization

**Source**: [go.dev/doc/pgo](https://go.dev/doc/pgo) (authoritative).

PGO feeds a **CPU pprof profile** from representative runs of the application
back into the compiler for the next build, which uses it to make more informed
optimization decisions (e.g. more aggressive inlining of hot functions). As of
Go 1.22, representative programs improve ~2–14%. The compiler expects a CPU
pprof profile — exactly what `runtime/pprof` and `net/http/pprof` produce.

## The core rule: the profile must be representative

A profile that is not representative of real behavior yields little or no gain.
**Microbenchmarks are bad PGO candidates** — they exercise a small part of the
program. Collect from a representative workload instead. For pi-go that means
**both** of the binary's hot paths, not one:

- **The headless agent/tool loop** — the tool-coverage eval suite: one
  `pi --mode print` scenario per tool family.
- **The TUI render loop** — `RenderMessages`, `collapseBlankLines`, and
  `matchLexer`. A live profile attributed ~24% of CPU to the `ansi.Strip` call
  `blankFast` replaces; bubbletea calls `View()` once per token during
  streaming, so this is a first-class hot path. It is exercised by the render
  benchmarks, not by `--mode print`.

A profile from only one workload optimizes only one half of the program.

## How pi-go records a profile

`make record-pgo` does the whole loop:

1. Builds the binary (`make build`).
2. Runs the eval-tools suite with `PI_EVAL_CPU_PROFILE=tmp/pgo`, so every
   scenario's `pi --mode print` writes a per-scenario CPU profile via the
   `--cpuprofile` flag.
3. Runs the TUI render benchmarks with `-cpuprofile tmp/pgo/render.pprof`.
4. Merges all profiles into `cmd/pi/default.pgo` with
   `go tool pprof -proto`.
5. Cleans up the scratch profiles.

`go build` auto-detects `default.pgo` in the main package directory
(`cmd/pi/`) and enables PGO — no `-pgo` flag needed. So `make build` after a
`record-pgo` is a PGO build.

### Manual collection

For a one-off profile of a specific workload:

```bash
pi --cpuprofile /tmp/pi.pprof --mode print "some representative prompt"
```

The `--cpuprofile` flag writes a runtime CPU profile for the process lifetime
and flushes it on exit. It is a persistent flag, so it works on subcommands too
(`pi memory mine . --cpuprofile /tmp/mine.pprof`).

## Building with PGO

- **Default**: `go build` detects `cmd/pi/default.pgo` and enables PGO
  (`-pgo=auto`).
- **Disable**: `go build -pgo=off`.
- **Explicit path**: `go build -pgo=/tmp/foo.pprof ./cmd/pi`. A path applies to
  all main packages in the invocation, so keep one profile per binary.

## Merging profiles

```bash
go tool pprof -proto a.pprof b.pprof > merged.pprof
```

The merge is a straight sum of samples regardless of wall duration, so when
profiling a long-running server, keep all slices the same duration or longer
slices dominate. For pi-go's per-scenario profiles this is not a concern — each
scenario is a short, bounded run.

## Source and iterative stability

Go PGO is robust to skew between the profiled version and the version being
built:

- **Source stability**: samples are matched by line offsets within functions.
  Adding code outside a hot function, or moving a function to another file in
  the same package, does not break matching. Renaming a function, moving it to
  another package, or editing inside a hot function may lose some optimizations
  (graceful degradation).
- **Iterative stability**: the compiler is conservative, so successive PGO
  builds do not oscillate. No two-stage canary build is required.

**Consequence**: collect a fresh profile regularly. Degradation accumulates
slowly as code drifts from the profile, and new code paths get no PGO benefit
until a new profile reflects them. After a large refactor that renames or moves
many functions, re-record.

## Guidelines

- **Re-record after meaningful code changes** — a stale profile optimizes the
  old code shape. `make record-pgo` is the one-step refresh.
- **Never profile a microbenchmark** for PGO; use the eval suite or a real
  workload.
- **Commit `default.pgo`** — the docs recommend committing the profile so builds
  are reproducible and performant from a fresh clone. It is a build input, not
  scratch.
- **`make clean` removes `cmd/pi/default.pgo`** — re-run `make record-pgo`
  after a clean if you want PGO back.
- **Verify PGO is on**: `go build -x ./cmd/pi 2>&1 | grep -i pgo` shows the
  `-pgo` flag the toolchain passes. Or check the binary size / `go version -m`
  for the profile.
- **A non-representative profile should not make the program slower** than no
  PGO — it just optimizes cold paths. If you see a real regression, file an
  issue at go.dev/issue/new.

## Examples

- `/pgo` — explain the PGO workflow and check whether `default.pgo` is current.
- `make record-pgo` — record a fresh profile from the eval suite.
- `pi --cpuprofile /tmp/pi.pprof --mode print "..."` — one-off manual profile.
- `go build -pgo=off ./cmd/pi` — build without PGO to compare.
