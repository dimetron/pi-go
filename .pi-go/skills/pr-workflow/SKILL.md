---
name: pr-workflow
description: Open, review and merge a pi-go pull request - PR creation, Codex review posted to the PR as a formal GitHub review, resolving review threads, govulncheck, and merge-method rules. Use when asked to create a PR, run or resolve a Codex review, or merge.
---

# Pull request workflow

The always-on rules are in the root `AGENTS.md`. This is the full procedure, in the order the steps run: create the PR, review it on GitHub, scan dependencies, merge.

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

## After the review: run govulncheck and post the result

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

## Never merge with "Rebase and merge"

**Use "Create a merge commit" or "Squash and merge". Never "Rebase and merge".**
Rebase-and-merge is the one merge method that silently produces unsigned
commits on `main`, and it is how 7 consecutive commits landed unsigned on
2026-09-23 (`1134feb`, `63661c5`, `efd5073`, `8f2272f`, `0aa87ae`, `ae30efd`,
`ccce1de`) even though every PR branch head *was* signed.

GitHub's own documentation is explicit:

> When using the Rebase and Merge option on a pull request, the commits in the
> head branch are added to the base branch **without commit signature
> verification**. When you use this option, GitHub creates a modified commit,
> using the data and content of the original commit. This means that GitHub
> didn't truly create this commit, and can't therefore sign it as a generic
> system user. GitHub doesn't have access to the committer's private signing
> keys, so it can't sign the commit on the user's behalf.

The mechanism, measured against this repository's history:

| Merge method | Who authors the landed commit | Signature on `main` |
|---|---|---|
| Create a merge commit | GitHub (`GitHub <noreply@github.com>`) | ✅ signed by GitHub's key |
| Squash and merge | GitHub | ✅ signed by GitHub's key |
| **Rebase and merge** | **you — with a new SHA** | ❌ **unsigned, always** |

Rebase-and-merge always rewrites the committer and creates a new SHA, so it
discards the signature the branch commit carried. `required_signatures` does not
catch it: the rule is checked on the pushed commits, and the rewrite happens
server-side afterwards.

`allowed_merge_methods` in the `main-admin` ruleset is now `["merge",
"squash"]`, so GitHub refuses a rebase-merge on `main`. The instruction remains
because the rule can be changed and because a rebase onto an updated base after
a force-push recreates the same exposure. If history must stay linear, rebase
**locally** and push the result — do not let GitHub rewrite it.

**Verify after merging.** `git verify-commit` on the landing commit, or check
the `Verified` badge, is the only proof. A signed PR branch is not evidence
about what landed: for all 7 commits above the branch head was signed (`%G?` =
`G`) and the landed commit was not.

## Never link an agent session in a PR

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
