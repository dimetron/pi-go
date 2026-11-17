---
name: git-workflow
description: Full pi-go git rules - worktree layout, SSH commit signing, verifying and re-signing commits, the known unverifiable commits, and the large-file and GIF hook. Use when a commit or push hook fails, a signature reads invalid, or before any history rewrite.
---

# Git workflow: worktrees, signed commits, large files

The always-on rules are in the root `AGENTS.md`. This is the full reference: the commands, the reasons, and the history behind each rule.

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

A signed commit is only half the guarantee: **the merge method can replace it
with an unsigned one after review.** "Rebase and merge" rewrites the commit
server-side and drops the signature; see "Never merge with 'Rebase and merge'"
under *Creating a pull request*.

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
  .pi-go/skills/vhs-e2e-gif/scripts/attach-gif-to-pr.sh 246 /tmp/e2e.gif
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
