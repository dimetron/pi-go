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
git worktree add -b fix/<topic> ../pi-go.worktrees/fix-<topic> HEAD
cd ../pi-go.worktrees/fix-<topic>
```

Remove it when the branch is merged or abandoned:

```bash
git worktree remove ../pi-go.worktrees/fix-<topic>
git worktree list          # verify; prune stale metadata with `git worktree prune`
```

### Where worktrees live

Three conventions coexist. Match the one that fits who is doing the work.

| Creator | Path | Branch | Notes |
|---|---|---|---|
| Human / Claude Code | `../pi-go.worktrees/<branch-with-dashes>` | `fix/…`, `feat/…` | Sibling of the repo, so it is outside the checkout |
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

## Repo notes

- `TODO.md` and `MEM_PPROF.md` are gitignored (`~/.gitignore` has `**/TODO.md`).
- `TODO.md` numbering restarts per section, so duplicate item numbers across
  sections are expected and are not a bug to fix.
