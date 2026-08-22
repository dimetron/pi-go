# Test coverage gap review

Reviewed packages: `internal/attest`, `internal/voice`, `internal/eval`,
`internal/memory`, `internal/palace`, and `internal/procs`.

This is a source-and-test review against the supplied fresh coverage snapshot. It
does not treat a higher percentage as the goal by itself: deterministic policy,
serialization, path handling, and process-lifecycle behavior should be covered
first; declarations and network/model wrappers should not acquire brittle tests
solely to move the number.

## Recommended order

1. **Fix and test `eval.sparkline`.** The uncovered function currently indexes a
   UTF-8 string by byte, so even an all-zero series emits an invalid UTF-8 byte
   instead of `▁`. This is a real output defect found by reviewing the gap.
2. **Test Palace's git-ignore wrappers with a temporary git repository.** These
   tests are local, deterministic, and protect a path that avoids unnecessary
   embedding work.
3. **Test `eval.WriteReport` end to end with `t.TempDir()`.** This covers the
   persisted artifact contract, not just rendering.
4. **Add a narrow runner seam to `memory.SubagentCompressor`, then table-test
   stream and failure behavior.** Most of the uncovered lines are orchestration
   glue and become deterministic with one small interface.
5. **Exercise `attest.Verifier.Verify` with an offline SHA-256 fixture.** This is
   the highest security impact, but needs a fixture whose subject and workflow
   identity match the production policy.
6. **Cover local embedding input handling through a pure helper or runner seam.**
   Keep the real-model test as an integration test.
7. **Add the one missing `procs.terminate` timer branch test.** Existing process
   tests already cover the important escalation and stale-PID behavior.

## `internal/eval` — 82.8%

### `WriteReport` (`internal/eval/report.go:15`)

`WriteReport` creates the destination, chooses timestamped filenames, writes
indented JSON, renders Markdown, writes it, and returns both paths plus the exact
Markdown. Existing tests call `RenderMarkdown`, but none assert this persistence
contract.

Add `internal/eval/report_test.go`:

- `TestWriteReport_WritesJSONAndMarkdown`: use a fixed UTC timestamp and a
  representative `RunReport`; call `WriteReport(report, t.TempDir())`; assert
  basename `eval-<spec>-YYYYMMDD-HHMMSS.{json,md}`, both files exist, returned
  Markdown equals the Markdown file, and decoded JSON is structurally equal to
  the input. Also assert both files have permission `0644` on platforms where
  permission bits are meaningful.
- `TestWriteReport_CreatesNestedOutputDir`: pass a not-yet-created nested path
  beneath `t.TempDir()` and assert both artifacts are produced.
- `TestWriteReport_OutputPathIsNotDirectory`: create a regular file and pass its
  path as `outDir`; assert the error contains `create report dir`. This covers a
  stable error branch without relying on root/user permission behavior.
- Decide and pin the filename policy for `Metadata.Spec`. It is interpolated
  directly into a path. A spec containing `/` currently turns into a nested path
  and fails, while `../name` can write outside `outDir`. If spec is not already
  validated at the caller, add `TestWriteReport_RejectsUnsafeSpec` for empty,
  absolute, separator-containing, and `..` values and validate before writing.

The JSON marshal error is not naturally reachable because `RunReport` contains
only JSON-supported field types. Do not add production indirection just to force
that branch.

### `sparkline` (`internal/eval/report.go:223`)

This is the best immediate unit-test target. `levels := "▁▂▃▄▅▆▇█"` is a UTF-8
string, but `levels[idx]` selects one byte and `WriteByte` writes it. `len(levels)`
is 24 bytes, not eight glyphs. The output is therefore invalid UTF-8 for normal
inputs and the scaling has 24 byte positions instead of eight levels.

Add these cases to `internal/eval/report_test.go` and change the implementation
to use `[]rune("▁▂▃▄▅▆▇█")` plus `WriteRune`:

- `TestSparkline_Empty`: nil and empty slices return `""`.
- `TestSparkline_UsesValidUnicodeLevels`: samples with running counts
  `0, 1, 2, 4, 8` produce valid UTF-8, exactly five graph runes before the
  annotation, begin at `▁`, end at `█`, and report `(max 8, 5 samples)`.
- `TestSparkline_AllZero`: verifies the scale floor of one and expects only low
  blocks plus `(max 1, N samples)`.
- `TestSparkline_BucketsToWidth`: construct 201 samples with isolated peaks at
  bucket boundaries; assert exactly 100 graph runes, all peaks survive max
  aggregation, and the annotation still reports 201 original samples.
- `TestRenderMarkdown_IncludesConcurrencySparkline`: one integration-level
  assertion that non-empty samples produce the fenced sparkline, while no
  samples omit it.

Negative `Running` values would currently create a negative index and panic.
`ComputeConcurrencyMetrics` produces non-negative counts, so either document
that invariant or clamp at zero and add a negative-input case if `sparkline` is
expected to be defensive.

## `internal/palace` — 87.3%

### `GitIgnoredSet` and `IsGitIgnoredSet` (`internal/palace/miner.go:295`)

The exported functions are untested wrappers over substantial git/path logic.
This is high-impact because a miss causes ignored content to be mined and
embedded, the expensive part of a mining run.

Add `internal/palace/miner_gitignore_test.go`:

- `TestGitIgnoredSet_FromRepositoryRoot`: initialize a temporary repository with
  `git init`, write `.gitignore` entries for an exact file, a directory, a glob,
  and a negated file, then create the files. Assert ignored entries are present,
  the negated file and ordinary tracked/untracked files are absent, and returned
  keys use slash-separated paths.
- `TestGitIgnoredSet_FromNestedDirectory`: call the function on a repository
  subdirectory and assert returned paths are relative to that mining directory.
  This directly protects the `--full-name`/`filepath.Rel` logic.
- `TestGitIgnoredSet_NonRepository`: a plain `t.TempDir()` returns nil.
- `TestIsGitIgnoredSet_ExactAndAncestor`: table-test nil and empty maps, exact
  file match, ignored-directory ancestor match such as `tmp/pi/cache.db` when
  `tmp/pi` is in the set, similar prefixes that must not match (`tmp/piano`), and
  a top-level ordinary file.

Use the real local git binary rather than mocking `exec.Command`; skip only when
`exec.LookPath("git")` fails. Configure repository-local identity only if a
commit becomes necessary (it should not be for `ls-files`).

### Local `Embed` and `Close` (`internal/palace/embedder.go:84`, `:102`)

The uncovered local embedder is coupled to concrete Hugot session and pipeline
types. The normal unit suite cannot construct it successfully without model
weights. `internal/palace/embedder_integration_test.go` exercises the real path
only when a model is available, so it should remain an integration layer rather
than become a mandatory download.

Two achievable improvements:

- Extract the length limiting loop into a pure helper such as
  `limitEmbeddingInputs([]string) []string` and add table tests in
  `internal/palace/embedder_test.go` for empty input, exactly 512 bytes, 513+
  bytes, multiple inputs preserving order, and non-ASCII text at the boundary.
  The current byte slice `texts[i][:maxCharLength]` can split a UTF-8 code point;
  a valid-UTF-8 assertion will force an explicit byte-versus-rune policy. Prefer
  returning a new slice so `Embed` does not silently mutate its caller's input.
- If full unit coverage of `Embed`/`Close` is desired, make the pipeline call and
  session destruction narrow internal interfaces. A fake runner can assert the
  limited inputs, return known vectors, and return a sentinel error to verify the
  `run embedding pipeline` wrapping; a fake destroyer can assert `Close` calls
  destroy once and tolerates its error by contract.

There are already two tests calling `Close` on `&localEmbedder{}` with a nil
session (`internal/palace/embedder_test.go:179` and `:220`). They cover only the
nil branch and are duplicates; merge them. If the fresh profile still labels
the whole function 0%, confirm the profile/build tags before adding another
identical test. The Ollama `Embed` path is already covered extensively by
`embedder_ollama_test.go` and `embedder_ollama_serial_test.go`, including empty
input, batching, ordering, retry, shrinking, serialization, and errors.

## `internal/memory` — 85.6%

### `NewSubagentCompressor`, `CompressObservation`, and `SummarizeSession`
(`internal/memory/compress.go:22`, `:36`, `:161`)

The parsing and prompt helpers have good direct tests. The remaining gap is the
adapter around `*subagent.Orchestrator`, which is concrete and therefore cannot
be faked without launching the subagent machinery.

Introduce an unexported interface held by `SubagentCompressor` with only the two
methods it uses: `LookupAgent(string)` and `Spawn(context.Context,
subagent.SpawnInput)`. Keep `NewSubagentCompressor(*subagent.Orchestrator)` as
the public constructor so callers do not change; tests in package `memory` can
construct the compressor with a fake runner directly.

Extend `internal/memory/compress_test.go`:

- `TestNewSubagentCompressor`: assert the supplied orchestrator is retained.
  This is low-value alone but will be covered naturally by adapter tests.
- `TestSubagentCompressor_CompressObservation`: fake lookup and spawn; capture
  `SpawnInput`; stream JSON over multiple `text_delta` events with an unrelated
  event between them; close the channel; assert the agent name, prompt fields,
  parsed observation, and copied raw metadata (`SessionID`, `Project`,
  `ToolName`, `Timestamp`).
- Table-test lookup failure, synchronous spawn failure, streamed `error` event,
  closed stream with no text, and malformed accumulated JSON. Assert the
  contextual prefixes (`finding`, `spawning`, `memory-compressor error`, and
  `parsing compressor response`) so failures remain diagnosable.
- `TestSubagentCompressor_SummarizeSession`: stream a fenced JSON response in
  multiple deltas; assert the captured prompt includes every observation and
  source file, and the result preserves supplied session/project metadata.
- Give summary the same lookup, spawn, streamed-error, empty-stream, and invalid
  JSON table. Assert `CreatedAt` falls between timestamps captured immediately
  before and after the call rather than comparing it exactly.
- Add a canceled-context case only if the fake runner is defined to return
  `ctx.Err()` from `Spawn`; the compressor itself does not otherwise own context
  cancellation.

This seam tests the package's real responsibilities without invoking a model,
subprocess, pool, worktree, or provider configuration.

## `internal/attest` — 72.8%

### `TUFCacheDir` (`internal/attest/attest.go:184`)

Add to `internal/attest/attest_test.go`:

- `TestTUFCacheDir`: set the platform home environment to a temporary path and
  assert `<home>/.pi-go/sigstore`, cleaned with `filepath.Clean`.
- On Unix, an unset/empty `HOME` exercises the wrapped `locating home directory`
  error from `os.UserHomeDir`. Put this in a Unix build-tagged test if the suite
  must remain portable; Windows uses different environment variables.

Do not run this test in parallel because it changes process environment.

### `NewVerifier` (`internal/attest/attest.go:195`)

The function performs a live Sigstore TUF refresh, so a normal unit test would
be network- and cache-state-dependent. Extract a small internal helper that
accepts a trusted-root fetch function (or an interface) while the exported
wrapper still passes `root.FetchTrustedRootWithOptions`.

Then add:

- `TestNewVerifierWithFetcher_UsesPiCache`: fake the fetch, inspect the received
  `*tuf.Options`, return the pinned trusted root from `testdata`, and assert the
  cache path is `TUFCacheDir()` and the resulting verifier carries the requested
  repository/SAN policy.
- `TestNewVerifierWithFetcher_FetchError`: return a sentinel and assert
  `fetching Sigstore trust root` plus `errors.Is` preservation.
- `TestNewVerifierWithFetcher_InvalidRepo`: return pinned material and assert an
  owner/name validation error is propagated from `NewVerifierWithMaterial`.

An optional explicitly tagged integration test may call `NewVerifier` against
live TUF, but it should not be the only coverage of this wrapper.

### `Verifier.Verify` (`internal/attest/attest.go:262`)

Existing `TestVerify_RealBundle` deliberately bypasses `Verify` with
`verifyStatementOnly` because its fixture has a SHA-512 subject and a branch
SAN. Consequently the production SHA-256 artifact binding is not tested.

Add immediately:

- `TestVerifierVerify_InvalidDigest`: build a verifier from pinned material and
  pass non-hex and odd-length strings; assert the `invalid digest` error. This is
  cheap and covers input rejection before cryptographic verification.
- `TestVerifierVerify_RejectsDigestMismatch`: call `Verify` on a valid bundle
  fixture with a syntactically valid but incorrect 32-byte digest and assert
  verification fails. This proves the production entry point applies an
  artifact policy, although the current SHA-512 fixture may fail for multiple
  policy reasons and therefore is not sufficient by itself.

Highest-value follow-up: add a small, immutable offline Sigstore bundle whose
in-toto subject uses SHA-256 and whose certificate SAN is
`https://github.com/<fixture-repo>/.github/workflows/release.yml@refs/tags/<tag>`.
Store the bundle and its public-good trusted root under
`internal/attest/testdata/`, record the expected subject digest in the test, and
add:

- `TestVerifierVerify_ValidSHA256Bundle`: expect predicate type, signer SAN,
  timestamp, and parsed provenance/SBOM fields.
- Subtests changing one factor at a time: one digest nibble, repository name,
  workflow path, branch instead of tag, and malformed predicate. Each must fail
  for the intended production policy.

That fixture closes the meaningful security gap. A mocked `verify.Verifier`
would raise line coverage but would not demonstrate certificate identity,
transparency log, timestamp, and artifact-digest policy composition.

## `internal/procs` — 86.5%; `terminate` 66.7%

`internal/procs/procs_fallback_test.go` and `selfkill_test.go` already cover the
important behavior: TERM-to-KILL escalation, no delayed signal after reap,
direct-child fallback, own-process-group protection, nil/unstarted
`signalGroup`, and caller timing wiring.

Add one focused Unix test to `internal/procs/procs_fallback_test.go`:

- `TestTerminate_UnstartedCommandDoesNotEscalate`: create but do not start an
  `exec.Cmd`, call `terminate` with a very short grace, wait past the timer, and
  assert the test remains alive. This covers the timer callback's
  `cmd.Process == nil` return at `procs_unix.go:52`.

There is a related defensive inconsistency: `signalGroup(nil, ...)` is a no-op,
but `terminate(nil, grace)` schedules a closure that dereferences `cmd` and will
panic asynchronously. Production cancellation always supplies a command, so
this is lower priority. If nil is intended to share `signalGroup`'s contract,
guard it before scheduling and add `TestTerminate_NilIsNoOp`.

The early `return err` branch depends on an actual OS signaling error other than
already-gone (`ESRCH`) and is difficult to provoke safely and portably. Do not
introduce syscall indirection solely for that final branch unless process
signaling is being refactored for another reason.

## `internal/voice` — no tests

`internal/voice/voice.go` contains only the `SessionCreator` interface and the
`Session` data type. It has no executable statements, so package-local tests
cannot improve statement coverage meaningfully. The behavior belongs to its
implementer and consumer, and is already exercised in
`internal/voicegemini/voicegemini_test.go` and
`internal/webserver/voice_test.go` (creation, expiry presence, realtime relay
descriptor, storage, and credential non-leakage).

Recommended contract hardening, not percentage chasing:

- Add `var _ voice.SessionCreator = (*Creator)(nil)` in
  `internal/voicegemini/voicegemini_test.go` (or beside `Creator`) to make
  interface drift a compile-time failure.
- Keep the behavioral cases in `voicegemini`: missing API key, expiry close to
  `now + SessionTTL`, and a realtime map containing transport/model/rates but no
  API key. Missing-key and credential/descriptor coverage already exists;
  strengthen the current non-zero expiry assertion to bound the TTL, and add
  explicit model/rate assertions if they are not pinned elsewhere.
- If `Session` later gains validation, cloning, or JSON behavior, create
  `internal/voice/voice_test.go` then. A test that merely constructs a struct or
  calls a fake through the interface adds maintenance without testing package
  logic.

## Suggested implementation slices

### Slice A — no production seams

- Fix/test `sparkline`.
- Add `WriteReport` happy path and stable filesystem error tests.
- Add Palace git-ignore tests.
- Add `Verify` invalid-digest test.
- Add the unstarted-command `terminate` test.
- Add the `voicegemini.Creator` compile-time assertion.

### Slice B — small internal seams

- Introduce the memory runner interface and cover both event-stream adapters.
- Extract Palace input limiting; decide and test UTF-8 and input-mutation
  semantics.
- Inject trusted-root fetching into an internal `NewVerifier` helper.

### Slice C — offline integration fixtures

- Add a release-tag/SHA-256 Sigstore fixture for the complete production
  `Verifier.Verify` path.
- Keep the local embedding model test opt-in, but run it in the model-bearing CI
  job for both default and `ORT` builds if those artifacts are supported.
