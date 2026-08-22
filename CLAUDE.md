# CLAUDE.md — pi-go

Guidance for coding agents working in this repo. Applies to Claude Code and to
pi-go's own agent; where the two differ, both are described.

## Work in a git worktree, not the primary checkout

**Do not make uncommitted edits in `/Users/dimetron/p6s/pi-dev/pi-go` and leave
them there.** The primary checkout has its branch switched frequently, and
`git checkout` discards uncommitted changes in tracked files without warning.
Work has been lost to this. A worktree gives each task its own working
directory and its own branch, so a switch in one cannot destroy another.

Create one before starting any task that edits tracked files:

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
| Human / Claude Code | `<repo>/.worktrees/<branch-with-dashes>` | `fix/…`, `feat/…` | Inside the repo, so it stays within the sandbox; `.worktrees/` is gitignored |
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

## Commits: always sign off and sign

Every commit must carry both a `Signed-off-by` trailer and a cryptographic
signature:

```bash
git commit -s -S -m "..."     # -s = Signed-off-by trailer, -S = sign
```

`-S` is redundant when config is honoured (`commit.gpgsign` and `tag.gpgsign`
are already `true`), but pass it explicitly so a commit fails loudly rather than
landing unsigned when config is missing or overridden.

Signing here is SSH-format, not GPG, through 1Password:

```
gpg.format       = ssh
user.signingkey  = ssh-ed25519 AAAAC3Nza...
gpg.ssh.program  = /Applications/1Password.app/Contents/MacOS/op-ssh-sign
```

### The sandbox breaks signing — commit with it disabled

`op-ssh-sign` needs to reach the 1Password agent, which is not reachable from
inside the sandbox. A commit attempted there either fails or, worse, succeeds
unsigned. **Run `git commit` with the sandbox disabled.**

This is not hypothetical: commits `0714568`, `a8b243b` and `bcb6d26` carry no
signature at all, and only some commits carry a `Signed-off-by`.

**Do not use `%G?` to check.** `gpg.ssh.allowedSignersFile` is not configured, so
signature *verification* cannot run: `git log --show-signature` errors with
`gpg.ssh.allowedSignersFile needs to be configured and exist`, and `%G?` reports
`N` for correctly signed and unsigned commits alike. It is a verification gap,
not a signing failure, but it makes the obvious check useless.

Inspect the raw object instead — a signed commit has a `gpgsig` header:

```bash
git cat-file commit HEAD | grep -q '^gpgsig' && echo signed || echo UNSIGNED
git log --format='%h %(trailers:key=Signed-off-by,valueonly,separator=;) %s' -5
```

Configuring an allowed-signers file would restore `%G?`, and is worth doing:

```bash
echo "dimetron@me.com $(git config user.signingkey)" > ~/.config/git/allowed_signers
git config --global gpg.ssh.allowedSignersFile ~/.config/git/allowed_signers
```

### Never use `--no-verify`

**Do not pass `--no-verify` to `git commit`, ever.** Not to unblock a failing
hook, not "just this once", not with a note in the commit message. There is no
case in this repo where it is the right answer.

`--no-verify` skips *all* hooks, including the signing path — so a bypassed
commit lands unsigned, and because `gpg.ssh.allowedSignersFile` is unset (see
above) nothing will tell you. That is how `0714568`, `a8b243b` and `bcb6d26`
ended up with no signature.

The hook that usually tempts this is `golangci-lint`, which runs against the
**primary checkout** and so can fail on pre-existing issues in files a worktree
branch never touched (currently 10 `SA1019` deprecation errors in
`hack/test/mcp/`). When that happens, stop and report it — the fix is to clear
the unrelated lint failure or to have the user decide, not to bypass the hook.

## Review with codex before opening a PR

**Always run a `codex` review of the changes before opening a pull request.**
Not after, not instead of the other gates — before, so findings are fixed in the
branch rather than in review comments.

```bash
cd <the worktree holding the branch>
codex review --base main --title "<short description>"
```

`--base <BRANCH>` and a custom `[PROMPT]` are mutually exclusive: passing both
fails with `the argument '--base <BRANCH>' cannot be used with '[PROMPT]'`. Use
`--base` alone for branch review; use `--commit <SHA>` or `--uncommitted` for the
other scopes.

Treat the output as a real reviewer, not a formality:

- Act on findings before pushing. Verify each one against the code yourself
  before accepting or dismissing it — a finding is a claim, not a verdict.
- If a finding is wrong, say why in the PR description rather than silently
  ignoring it.
- Codex prints a long execution trace; the findings are at the end of the output.
- A CLI usage error is not a clean review. `exit 0` with an argument-parsing
  message means the review never ran — re-run it properly.

This has already paid for itself: a codex review of the worktree stash fix found
a time-of-check/time-of-use race that the tests, `-race`, and `golangci-lint` all
passed over — `git stash list` returns positional `stash@{N}` refs, so an
external push or drop between lookup and use renumbers the list and a positional
drop deletes a stranger's work.

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

## Build, test, lint

Go 1.26.5. Use the Makefile rather than raw `go` invocations where a target
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

### Two environment traps

- **Tests that bind a local listener fail under the sandbox.** Anything using
  `httptest.NewServer` panics in `newLocalListener`. `internal/cli` is affected.
  This is not a real failure — re-run outside the sandbox before believing it.
- **Profiling endpoints are on localhost**, so `curl localhost:6060/...` is
  blocked by the sandbox too. See the `go-pprof` skill.

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
