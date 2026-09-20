# AGENTS.md — pi-go

Guidance for coding agents working in this repo. Applies to Claude Code and to
pi-go's own agent; where the two differ, both are described.

## Before starting work

Use an isolated git worktree for every task that edits tracked files. Before
reading or changing code, enumerate the repository's instruction files and read
the applicable ones:

```bash
rg --files -g 'AGENTS.md' -g 'CLAUDE.md' -g '!tmp/**' -g '!.git/**' ..
```

Read the root `AGENTS.md` first, then any instruction file in each parent
directory of the files you will touch. Instructions closer to a file add
constraints to the repository guidance; do not skip them because a task appears
small.

## Work in a git worktree, not the primary checkout

**Do not make uncommitted edits in the primary checkout and leave them there.**
The primary checkout has its branch switched frequently, and `git checkout`
discards uncommitted changes in tracked files without warning. Work has been
lost to this. A worktree gives each task its own working directory and its own
branch, so a switch in one cannot destroy another.

### Check first: are you already in a worktree?

**Do not create a worktree when you are already inside one.** Nesting a
worktree below another puts the new branch's base at the enclosing worktree's
HEAD, and leaves two worktrees whose branches shadow each other on the same
task. If you are already in one, work in the current directory.

The check is a comparison, because `git rev-parse --git-dir` and
`--git-common-dir` differ in exactly the case that matters:

```bash
# Already in a worktree? Then the two paths differ.
if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  echo "already in a worktree: $(git rev-parse --show-toplevel) — work here"
else
  echo "primary checkout — create a worktree for this task"
fi
```

In the primary checkout both report `.git`. Inside a linked worktree `--git-dir`
is `<repo>/.git/worktrees/<name>` while `--git-common-dir` stays `<repo>/.git`.

Create one only when that check says you are in the primary checkout:

```bash
git worktree add -b fix/<topic> .worktrees/fix-<topic> HEAD
cd .worktrees/fix-<topic>
```

Remove it when the branch is merged or abandoned:

```bash
git worktree remove .worktrees/fix-<topic>
git worktree list          # verify; prune stale metadata with `git worktree prune`
```

### Where worktrees live

Three conventions coexist. Match the one that fits who is doing the work.

| Creator | Path | Branch | Notes |
|---|---|---|---|
| Human / Claude Code | `<repo>/.worktrees/<branch-with-dashes>` | `fix/…`, `feat/…` | Inside the repo; `.worktrees/` is gitignored |
| pi-go agent (`/run`, subagents) | `<repo>/.pi-go/tasks/<pathID>` | `pi-agent-<shortID>`, or the sanitized requested name | Created by `internal/subagent/worktree.go`; `.pi-go/` is gitignored |
| `arbor` tool | `~/.arbor/worktrees/pi-go/<name>` | matches dir name | External tool, listed here only so `git worktree list` output is not surprising |

`.pi-go/` and `.worktrees/` are both gitignored, so agent worktrees never show
up as untracked noise in `git status`.

### What pi-go's agent already does, and why it matters

`WorktreeManager.Create` (`internal/subagent/worktree.go:117`) **stashes
uncommitted changes before `git worktree add` and pops them afterwards**, with
a unique stash message so the pop is deterministic
(`stashMessage`, `popStashByMessage`). It does this because `worktree add` from
HEAD fails on a dirty tree.

The consequence worth knowing: if a pi-go subagent runs while you have
uncommitted work in the primary checkout, your changes take a round trip
through the stash. That is safe, but it is one more reason not to keep
long-lived uncommitted work in the primary checkout.

Agents marked `[worktree]` edit an isolated tree. Their edits do **not** land in
the caller's tree — ask for an explicit patch or file list to apply, or use a
non-worktree editing agent (`internal/tools/subagent.go:127-128`).

## Commits: all commits must be signed

**Rule: every commit must be cryptographically signed and carry a
`Signed-off-by` trailer.** There is no exception — not for merge commits, not
for reverts, not for WIP or "just this once". An unsigned commit is a broken
commit; fix it before pushing (see the pre-push hook below).

The signing command:

```bash
git commit -s -S -m "..."     # -s = Signed-off-by trailer, -S = sign
```

`-S` is redundant when config is honoured (`commit.gpgsign` and `tag.gpgsign`
are already `true`), but pass it explicitly so a commit fails loudly rather than
landing unsigned when config is missing or overridden. The `pre-push` hook
hard-fails any push containing an unsigned commit or one missing a matching
`Signed-off-by` trailer, so an unsigned commit cannot reach the remote.

Signing here is SSH-format, not GPG, through 1Password:

```
gpg.format       = ssh
user.signingkey  = ssh-ed25519 AAAAC3Nza...
gpg.ssh.program  = /Applications/1Password.app/Contents/MacOS/op-ssh-sign
```

### Verify signatures with `git verify-commit`, never with the `gpgsig` header

**A `gpgsig` header does not mean the signature is valid.** It only means signing
was *attempted* at the time the commit was created. Rewriting the commit object
afterwards keeps the header and silently invalidates the signature. That is not
hypothetical: appending a trailer, dropping a duplicate one, or re-writing the
message with `git hash-object -t commit -w` all produce a commit that passes a
header check and fails verification.

```bash
# WRONG — passes on invalid signatures
git cat-file commit HEAD | grep -q '^gpgsig' && echo signed || echo UNSIGNED

# RIGHT — actually verifies the signature
git verify-commit HEAD && echo VALID || echo INVALID
```

One-time setup, without which verification cannot run at all
(`gpg.ssh.allowedSignersFile needs to be configured and exist`, and `%G?`
reports `N` for signed and unsigned commits alike):

```bash
echo "dimetron@me.com $(git config user.signingkey)" > ~/.config/git/allowed_signers
git config --global gpg.ssh.allowedSignersFile ~/.config/git/allowed_signers
```

Then check a range — every commit a PR would publish:

```bash
git log --format='%h' origin/main..HEAD | while read c; do
  printf '%s ' "$c"
  git verify-commit "$c" >/dev/null 2>&1 && echo VALID || echo ">>> INVALID <<<"
done
git log --format='%h %(trailers:key=Signed-off-by,valueonly,separator=;) %s' -5
```

**To fix commits that carry an invalid signature**, re-sign them — do not
hand-edit the object:

```bash
git rebase --exec 'git commit --amend --no-edit -S' origin/main
```

`-S` signs. Do **not** pass `-s` as well when the message already ends in a
`Signed-off-by` line: you get a duplicate trailer, and "fixing" that by
rewriting the message with `git hash-object` is what breaks the signature in the
first place. Prefer letting the `commit-msg` hook add the trailer, or run
`git commit -S` alone.

GitHub's own verdict is the ground truth for what reviewers see, and it agrees
with `git verify-commit`:

```bash
gh api repos/dimetron/pi-go/commits/<sha> \
  --jq '.commit.verification | "\(.verified) \(.reason)"'
```

### Two kinds of commit that fail a local check and are not your problem

111 merge commits on `main` fail `git verify-commit` locally. They are
**GitHub-created merge commits**, signed by GitHub's own key — GitHub reports
them `verified=true`, but your personal `allowed_signers` has no public key for
that signature, so a local check cannot confirm it and fails closed. They are
never in a push range (`rev-list <sha> --not --remotes=origin` is empty for
them), so `pre-push` does not reject them. Do not "fix" them.

The non-merge ones are real, and five exist in `main` as of 2026-09-17:

```
60cbdfa  feat(hack/agentgateway): track update-prices.sh
6ce61d9  feat(hack/agentgateway): add update.sh to push repo changes ...
4aa99b8  feat(ollama): cloud pricing and context windows in model metadata
369cfbd  feat: add Nix flake and NixOS module
de6481b  pimodels: add NewFromInfo
```

The first two were produced by hand-rewriting commit objects with
`git hash-object -t commit -w` after signing, which keeps the `gpgsig` header
and invalidates the signature. The rest predate this guidance. All five are
already merged, so removing them means rewriting published history — it needs an
admin force-push and invalidates every clone, so it is a deliberate decision, not
a cleanup. Record them rather than quietly re-pushing.

### Never use `--no-verify`

**Do not pass `--no-verify` to `git commit`, ever.** Not to unblock a failing
hook, not "just this once", not with a note in the commit message. There is no
case in this repo where it is the right answer.

`--no-verify` skips *all* hooks, including the signing path — so a bypassed
commit lands unsigned. That is how `0714568`, `a8b243b` and `bcb6d26` ended up
with no signature. Set up `gpg.ssh.allowedSignersFile` (above) so that
`git verify-commit` catches it immediately rather than leaving it for a reviewer
to notice.

The hook that usually tempts this is `golangci-lint`, which runs against the
**primary checkout** and so can fail on pre-existing issues in files a worktree
branch never touched (currently 10 `SA1019` deprecation errors in
`hack/test/mcp/`). When that happens, stop and report it — the fix is to clear
the unrelated lint failure or to have the user decide, not to bypass the hook.

## Never commit a GIF or a screen recording

**Recordings go on a GitHub release, never into git history.** A GIF of a test
run or a TUI session is large, write-once, and stale within a week — but every
revision of one is downloaded by every clone forever, and a blob cannot be
un-pushed without rewriting history for everyone. GitHub itself warns above
50 MiB and rejects a push above 100 MiB.

Attach one to a PR instead — the `vhs-e2e-gif` skill wraps this:

```bash
CAPTIONS='e2e: tool calls after the fix' \
  .claude/skills/vhs-e2e-gif/scripts/attach-gif-to-pr.sh 246 /tmp/e2e.gif
```

`.githooks/check-large-files` enforces it from both `pre-commit` (staged blobs)
and `pre-push` (blobs the push would upload), with two limits:

| What | Limit | Why |
|---|---|---|
| `.gif .gifv .apng .mp4 .m4v .mov .webm .mkv .avi .ogv .cast` | 1 MiB | Recordings belong on a release |
| Anything else | 10 MiB | Far below GitHub's 100 MiB hard reject; the largest non-media file in the tree is ~70 KiB |

The check runs twice on purpose. `pre-commit` catches it early, when unstaging
is the whole fix; `pre-push` is the last reversible moment for a commit that
reached the branch some other way — a rebase, a cherry-pick, another tool.

One path is grandfathered in the script's `allow_re`: `docs/screen/pi-go.gif`
(5.4 MiB), committed before the hook existed and referenced by nothing in the
tree. It should move to a release asset and take its allowlist entry with it.

If a file genuinely has to live in the tree, shrink it, or as a last resort:

```bash
PI_ALLOW_LARGE_FILES=1 git commit -sS -m "..."
```

**Do not reach for `--no-verify`** — it skips the signing hooks too, and lands
an unsigned commit (see above).

## Review with Codex: open the PR first, review on GitHub

**The PR is the review track.** Open it first, then have Codex post its review
directly to the PR as a formal GitHub review, then resolve findings in-thread.
This keeps every finding, fix, and resolution permanently linked in one place —
a local terminal dump of findings is lost context; PR comments are not.

The flow:

1. **Finish the branch and open the PR** (rules below). All gates — build,
   tests, lint, vet — still run *before* pushing; opening early does not skip
   them.
2. **Have Codex review and post to the PR itself.** In the environment tested on
   PR #237, the `codex-review` subagent and restricted sandbox modes could not
   reach `api.github.com` ("credential rejected", browser fallback denied).
   Run this from a clean, dedicated PR worktree because the required
   `danger-full-access` mode removes the filesystem boundary as well as the
   network restriction:

   ```bash
   cd <pr-worktree>
   codex exec --sandbox danger-full-access "Read-only code review of GitHub PR <N> \
   (repo dimetron/pi-go; current dir is the PR worktree, branch <branch>). Scope: \
   'git diff main...HEAD'. Then POST one formal GitHub review yourself using gh: \
   build inline comments for actionable findings, anchored to file+line, as a \
   JSON payload file in /tmp, \
   submit with 'gh api --input' against /repos/dimetron/pi-go/pulls/<N>/reviews \
   with event=COMMENT. Sign the body '— Codex review'. Do not modify tracked files. \
   Do not commit or push." > /tmp/codex-pr-review.log 2>&1
   ```

   Contract: read-only over tracked files (the `/tmp` payload file is fine),
   diff scoped to `main...HEAD`, one formal review signed "— Codex review", with
   inline file:line comments for every actionable finding. A no-findings review
   has no inline comments. Budget ~5 minutes; run the foreground command with a
   generous timeout, then verify `git status --short` is still empty.

   The `gh` credential inside Codex's process may still be rejected even with
   network open. If Codex cannot post, have it emit findings as text (`FILE:` /
   `VERDICT:` / explanation per finding). The caller must convert those findings
   into the same formal review payload and submit it to
   `/repos/dimetron/pi-go/pulls/<N>/reviews`; a top-level `gh pr comment` is not
   a substitute for the review.

3. **Resolve findings in their review threads**: fix accepted ones in normal
   signed commits pushed to the branch, reply to each inline thread with its
   resolving commit, and resolve the thread after verification. For a dismissed
   finding, reply in that thread with the reason before resolving it — never use
   an unrelated top-level comment or silently ignore a finding.

A finding is a claim, not a verdict: verify each against the code before
accepting or dismissing it. The review is an independent gate from tests, lint,
vet, and build; it does not replace any of them.

### Local vs GitHub review — when to use which

- **GitHub review (default)** for anything that becomes a PR: permanent track,
  inline line comments, resolvable threads, visible to humans later.
- **Local `codex exec` output (no posting)** for pre-PR sanity checks on
  uncommitted work-in-progress, or quick second opinions on a spec/design doc
  where there is nothing to anchor comments to yet. Do not let local-only
  reviews substitute for the on-PR review before merge.

### After the review: run govulncheck and post the result

**Once the review is resolved and before the PR merges, run `govulncheck`
against the branch and post the result as a PR comment.** It belongs after the
review rather than before because it is about what the branch *depends on*
rather than what it says, and a dependency added mid-review would otherwise go
unscanned.

```bash
make vulncheck        # govulncheck -format json ./... | go run ./hack/vulngate
```

The gate fails only on findings that name a fixed version — those are the ones
someone can act on. Findings with no released fix are printed and do not fail:
there is nothing to upgrade to, so failing on them would leave every build red
until an upstream maintainer cuts a release, and a permanently red check is one
nobody reads.

Post the scanner and DB versions along with the findings, because the answer is
only true for the database on the day it ran. If a finding does have a fix,
upgrade rather than explain it away — the gate is deliberately narrow so that a
failure always means "there is something to do".

Do not run `make check-cve` for this: it opens with `go mod tidy -v`, which
rewrites tracked files, and a check should not mutate the tree it is checking.

## Creating a pull request

When the user asks to create a PR, open the browser link to the PR after it is
created, and include **all pending changes** in the PR.

```bash
# Push the branch and create the PR with all pending (uncommitted) changes.
# Stage everything, commit, push, then open the PR in the browser.
git add -A
git commit -s -S -m "..."        # sign off and sign, per the rules above
git push -u origin <branch>
gh pr create --fill --web        # --web opens the PR page in the browser
```

- **All pending changes**: stage and commit everything outstanding on the branch
  before creating the PR — do not leave uncommitted work behind.
- **Open the browser link**: after the PR is created, open the PR URL in the
  browser so the user can review it immediately. `gh pr create --web` does this
  automatically; if you create the PR without `--web`, open the returned URL
  yourself.

### Never link an agent session in a PR

**Do not put a `claude.ai/code/session_...` link — or any other agent session
link — in a PR body, title, commit message, or review comment.** A session link
hands anyone who can read the PR the entire transcript that produced it,
including whatever unrelated context happened to be in that conversation. That
is a wider audience than the PR, and it is not what a reviewer asked for.

A PR must stand on its own: what changed, why, and how it was verified. The
tooling that produced it is not part of the record.

This overrides the harness default. Claude Code is instructed to append a
session link to PR bodies and to commit messages; in this repo, leave it out —
`gh pr create --fill` inherits whatever is in the commit message, so the link
must not be there either.

## Build, test, lint

Go 1.27.0. Use the Makefile rather than raw `go` invocations where a target
exists:

```bash
make build          # build the binary
make install        # build + install to GOPATH/bin
make test           # == test-unit
make test-unit
make test-integration
make test-e2e       # build-tagged
make test-all       # unit + integration + e2e
make test-coverage
make lint           # golangci-lint v2
make vet
make check-cve
```

### VS Code extension (`vscode/`)

The extension is built and installed from `vscode/` with its own Makefile:

```bash
cd vscode && make install   # bun compile → vsce package → install
```

Local build output goes to `$HOME/.vscode-ext/` (`pi-go-vscode.vsix`), never
into the repo — worktree branches must not accumulate VSIX artifacts, and
`.vscode-ext` output is kept out of `git status`. The `install` target
auto-detects the VS Code CLI: `code` on PATH first, then the binary inside the
Insiders app bundle (`/Applications/Visual Studio Code -
Insiders.app/.../bin/code`, this machine's editor), then stable VS Code. CI
(`release.yml`) uses `make release` only, which keeps the versioned VSIX at the
repo root for the release upload glob — do not move that output.

## TUI output safety: never write to stdout/stderr

The interactive TUI runs on the terminal's alternate screen. **Any write to
stdout or stderr from inside the TUI corrupts the display** — stray `fmt.Print*`,
`log.Print*`, `os.Stdout.Write`, or `os.Stderr.Write` calls render as garbage
over the UI and break the session.

Rules:

- **Never** use `fmt.Print*`, `log.Print*`, `stdlog`, `os.Stdout`, or
  `os.Stderr` to emit diagnostics or output from code that runs while the TUI
  is active (agent loop, callbacks, hooks, commands, model/tool callbacks).
- **Route diagnostics through the session logger** (`m.cfg.Logger` /
  `logger.Logger`) instead — `Info`, `Error`, `Errorf`, etc. These write to the
  session log file, never the terminal.
- **Allowed TUI outputs** are the only sanctioned ways to surface text to the
  user: the chat transcript, `SystemNoticeCh` (short system notices like
  auto-compaction outcomes), and the TUI's own status/error rendering. If a
  message must reach the user, deliver it through one of these, not a raw
  stdout/stderr write.
- A panic handler may write to stderr only as a last resort before the process
  dies; it must never be used for routine logging.

When in doubt, grep for `Printf|Println|os.Stdout|os.Stderr|stdlog` in the
package you are touching and confirm every hit is either outside the TUI path
or routed through the session logger.

## Tool-output compaction: one route per tool, and write what you read

`internal/tools/compactor*.go` shrinks tool results before they reach the model.
Seven of its nine pipelines were no-ops in production for a long time, and every
cause was a silent mismatch rather than a crash. The invariants below exist so
that cannot recur.

- **Route on the registered name.** `compactorPipelines` is keyed by the name a
  tool actually registers — hyphens for the git tools (`git-file-diff`), and
  `ripgrep` for the search tool, which self-names via `grepToolName` and is
  built once by `CoreTools`. The tool registers `grep` only on a host without
  `rg`. A `switch` on underscore spellings is what made three git pipelines
  unreachable.
- **A pipeline may only write the field it read.** `CompactResult` carries
  `CompactWrite{Key, Value}` pairs. Do not add a probe order that guesses a
  target field: the previous `stdout → content → output → diff → result → data`
  order wrote to the wrong key, and no tool emits `output` at all.
- **Keep the value's type.** ADK round-trips every result through
  `json.Marshal`/`json.Unmarshal` (`internal/typeutil/convert.go`), so a
  `[]GrepMatch` arrives as `[]any` of `map[string]any` — never a typed slice.
  Cap the array; do not re-render it to a string. The TUI result summaries read
  the same keys (`internal/tui/tool_display.go`) as `[]any`.
- **Preserve the true total.** A cap sets `truncated` and leaves
  `total_matches`/`total_files`/`total_entries`/`total_hunks` as the pre-cap
  count, so a partial list is never read as the whole answer. `truncated`
  carries `omitempty`, so it is absent from a complete result — `applyCompaction`
  writes named keys unconditionally for exactly this reason.
- **Decline when there is nothing to gain.** Return `nil` when the result would
  not shrink (the rtk `never_worse` guard). Do not cap lists that mislead when
  truncated: `git-overview` caps `recent_commits` but leaves the
  staged/unstaged/untracked lists whole, because hiding a dirty file is a
  correctness problem, not a saving.
- **Test against real structs.** Build test payloads by marshalling the tool's
  actual output struct (`buildProdResult`), never a hand-written map. Synthetic
  maps carrying an `output` key are what kept the dead pipelines green.
  `TestCompactor_EveryToolCompacts` and `TestCompactorRouting_EveryRegisteredTool`
  are the guards; both fail if a route or field drifts.

### rtk is a CLI proxy, not a library — do not delegate to it

`rtk` (third-party, `rtk-ai/rtk`, installed at `/opt/homebrew/bin/rtk`) is an
independent Rust CLI. It is a **reference for design**, not a dependency: pi-go's
compactor is native Go. Measured against pi-go's real shapes, delegating to it
does not work:

- `rtk pipe -f grep` was **byte-identical** on a 400-match list — the same class
  of no-op bug described above. `rtk grep` (command mode) does compact (93.8% on
  the same payload), but that is a different interface: it *runs* the search
  rather than filtering a result pi-go already has.
- `rtk pipe -f go-test` on real `go test` failure output emitted
  `Go test: No tests found` and **exit code 0**, losing both the failure and its
  signal.
- Shelling out per tool result adds a process spawn on the hot path, and rtk's
  filter set is not pi-go's tool set.

Use rtk's *decisions* (caps that preserve totals, the never-worse guard,
head-capping logs while keeping status lists whole); do not wire it in.

## Never trust a big win without a proof check

**A large improvement number is a claim about the code, and claims need evidence
of the same size as the number.** A 90% saving, a 10× speedup, "this fixes the
slow path" — each is exactly as likely to mean *the thing never ran* as it is to
mean the work succeeded. Treat a big win as a hypothesis to falsify, not a result
to report.

This is not hypothetical. The compaction work above reported savings per tool,
and three separate defects hid behind those numbers:

- **The shape was impossible.** A pipeline was credited with a 49–76% saving on
  payloads of 200 commits. The tool runs `git log --oneline -10`
  (`git_overview.go:72`), so that input can never occur and the pipeline never
  fires. The number was real and meaningless.
- **The input was synthetic.** Pipelines looked healthy against hand-written maps
  carrying an `output` key no tool emits. The tests passed; production did
  nothing.
- **The saving was measured per-tool, not end-to-end.** Nothing checked that the
  bytes actually left the prompt.

The rules that follow from that:

- **Bound the input by what the producer can emit.** Before measuring, find the
  cap the tool itself enforces (a constant like `maxGrepMatches`, a `-10` in the
  command, `truncateOutput`'s byte ceiling) and measure at or below it. A
  payload larger than the tool can produce measures a program that does not
  exist. State the bound next to the number.
- **Build fixtures from the real types.** Marshal the actual output struct and
  unmarshal into `map[string]any`, the way ADK does. Never a hand-written map: a
  fixture you invented encodes the contract you assumed, which is the thing under
  test.
- **Prove the saving end-to-end, not at the seam.** A per-function number shows a
  function works; it does not show the result reached the model. `AvgResultBytes`
  per tool in the eval harness (`internal/eval/metrics.go`) is the end-to-end
  version of this — it is currently *reported* in every eval but asserted only in
  one unit test, and never gated against a baseline. That gap is why seven dead
  pipelines looked healthy.
- **Check information, not just size.** Ask what the consumer needs from the
  output — a file name, a count, a total, whether the list is complete — and
  assert each survives. A cap that drops the one field a decision depends on is a
  regression that a byte count calls a win. `compactor_preservation_test.go`
  exists for this.
- **Verify the guard can fail.** Reintroduce the bug and confirm the test goes
  red, then restore. A test that has never failed proves nothing; several of the
  guards here were only trusted after being seen to break.
- **Prefer the cheap falsification first.** Before a full eval run, ask what
  result would show the win is fake, and run the smallest thing that would show
  it. Measuring at the producer's real bound is usually a few seconds of work and
  kills most false wins outright.
- **Say which numbers you did not verify.** If a figure came from an earlier
  session, a different shape, or reasoning rather than a run, label it. An
  honest gap is cheap; a confident wrong number is expensive.

When a win is large enough to be worth reporting, it is large enough to be worth
an eval. `make eval-tools` runs one headless scenario per tool family and rolls
the results into a coverage matrix (`internal/eval/scenarios/README.md`);
`make eval-run` and `make eval-judge` drive `/run` end-to-end against the pinned
`eval/base` baseline (`internal/eval/eval.md`). Report the before/after the
harness produced, not the before/after you expected.

## Profiling

`pi --pprof true` serves `net/http/pprof` on `http://localhost:6060/debug/pprof`.
Scripts live in `.claude/skills/go-pprof/scripts/` (`pprof-snap.sh`,
`pprof-watch.sh`, `pprof-diff.sh`); all read `PPROF_URL` and take no required
arguments.

A single sample cannot distinguish churn from a leak. Establish drift with
`pprof-watch.sh` before diagnosing, and profile the app in the state being
complained about — an empty session and an aged one are effectively different
programs here.

Findings are tracked in `TODO.md` under `## PPROF`; the live-measurement
write-up is in `MEM_PPROF.md`. Both are gitignored, so they are local notes, not
shared state — do not assume a teammate can see them.

## Session history lookup

When the user asks to check a specific session (e.g. `260809-0249-c53d2-7561f`)
or review session history, search under **`$HOME/.pi-go/`** — that is the
session root, not the repo.

Sessions live in `$HOME/.pi-go/sessions/<session-id>/`, one directory per
session (`sessionsDir()` in `internal/cli/cli.go:766`). Each contains:

- `meta.json` — id, title, model, provider, workDir, timestamps, host info.
- `events.jsonl` — the full turn/event stream (user + assistant messages).
- `trajectory.atif.json` — the ATIF trajectory (agent tool-call trace).
- `branches.json` — session branch state.

Other useful files under `$HOME/.pi-go/`:

- `last-session.json` — metadata of the most recent session start.
- `history.jsonl` / `history` — command history.
- `log/` — runtime logs (check here when init or a run fails).
- `config.json` — default model and settings.
- `memory/` — semantic memory store.

Useful commands:

```bash
ls $HOME/.pi-go/sessions/ | grep <session-id>   # confirm a session exists
cat $HOME/.pi-go/sessions/<session-id>/meta.json
tail -n 50 $HOME/.pi-go/sessions/<session-id>/events.jsonl
```

The `session-stats` tool (`internal/tools/session_stats.go`) scans these
directories for anomalies; it defaults to `$HOME/.pi-go/sessions` and accepts a
`session_dir` override.

## Repo notes

- `TODO.md` and `MEM_PPROF.md` are gitignored (`~/.gitignore` has `**/TODO.md`).
- `TODO.md` numbering restarts per section, so duplicate item numbers across
  sections are expected and are not a bug to fix.
