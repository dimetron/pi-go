# PiGo: A Self-Improving Coding Agent That Reviews Its Own Pull Requests

**A deployment case study in human-gated recursive self-modification**

Dmytro Rashko
pi-go project (github.com/dimetron/pi-go)
2026-09-20

---

## Abstract

Recursive self-improvement of coding agents is established as *possible*: the Darwin
Gödel Machine (DGM) raised SWE-bench from 20.0% to 50.0% by rewriting its own Python
source, and the Self-Improving Coding Agent (SICA) reached 53% on SWE-bench Verified.
Both prove the mechanism, and both measure it on curated benchmarks. Neither reports
what happens when the same mechanism is pointed at a system in daily production use,
written in a typed and compiled language, with a human who must be able to reject the
change.

This paper reports such a deployment. **PiGo** is a Go coding agent (313,497 lines,
built on Google ADK Go) whose git identity *is* the agent: repo-local
`user.name = pi`. Over 2026-06-02 to 2026-09-20 it authored **85 commits on
`origin/main`**, **83 of them cryptographically verified `valid`** by GitHub, and
**41 pull requests** contain at least one agent-authored commit (**38 merged**) — 12.5%
of the 303 PRs merged in that window. The agent writes its own specs, finds bugs in its
own subsystems by measurement, adds regression guards, records the resulting invariant
in its own instruction file, and rebuilds itself.

The central finding is architectural, not performance-based. Every existing
self-improvement system relies on an **automatic fitness function**: a benchmark
score. PiGo replaces the benchmark with a **human-gated merge**, and its evidence of
progress is therefore not a score but a *reviewable artifact*. We argue the interesting
variable in self-improving agents is not whether they can rewrite themselves — they can
— but **what stands between a proposed self-modification and its adoption**. We
contribute an anatomy of that gate, four concrete verification mechanisms that caught
real defects during self-modification, and two failure modes we observed: commits that
reached `main` without review, and two on `main` whose signatures are invalid.

**Keywords:** self-improving agents, recursive self-modification, coding agents,
human-gated autonomy, verification, pull-request workflow

---

## 1. Introduction

Schmidhuber's Gödel Machine (2003) required a formal proof that each self-modification
was beneficial. The requirement is unimplementable in general, because the relevant
properties are undecidable. Twenty years later, two lines of work replaced proof with
empirical evidence:

- The **Darwin Gödel Machine** (Zhang et al., arXiv:2505.22954, ICLR 2026) maintains an
  archive of agent variants, mutates a sampled variant, and keeps changes that improve
  coding benchmarks — 20.0% → 50.0% on SWE-bench, 14.2% → 30.7% on Polyglot, across 150
  generations, without human intervention.
- **SICA** (Robeyns, Szummer & Aitchison, arXiv:2504.15228) removes the distinction
  between meta-agent and target agent entirely: one agent edits its own code and improves
  from 17% to 53% on a SWE-bench Verified subset, driven by LLM reflection rather than
  gradients.

Both are evaluated by a *fitness function the system can compute about itself*. That is
what makes them automatable, and it is also what makes them unreportable as production
practice: SWE-bench is a fixed distribution, and an agent optimising it is not the same
as an agent improving its daily driver.

PiGo is the second thing. It is a coding agent that a human uses to write code, and
which uses itself to improve itself. There is no benchmark in the loop. The fitness
signal is a **human reviewing a pull request** — and the artifact under review is the
agent's own source code.

### 1.1 Contributions

1. **A deployment record** (§4) of 41 self-authored PRs and 85 verified commits on
   `main`, with measured signature-verification and PR-attribution statistics — not
   projected numbers but the state of a real repository.
2. **An anatomy of the adoption gate** (§5): the six mechanisms, in order, that a
   self-modification must pass, and why the ordering matters.
3. **Four verification mechanisms that caught real defects** (§6), including one where
   the agent's spec was wrong and the code corrected it.
4. **Two observed failure modes** (§7) — unreviewed commits reaching `main`, and two
   invalid signatures — reported because they bound the safety claim.
5. **A negative result stated plainly** (§8): we report no benchmark improvement delta,
   and we argue this is the honest shape of evidence for production self-modification.

---

## 2. Related work

| System | Self-modifies | Validation signal | Language | Human gate |
|---|---|---|---|---|
| Gödel Machine (Schmidhuber 2003) | yes | formal proof | — | none (proof suffices) |
| Gödel Agent (Yin et al. 2024) | yes | runtime self-reference | Python | none |
| ADAS (Hu et al. 2024) | meta-agent edits *other* agent | benchmark | Python | none |
| DGM (2025) | yes, archive-based | SWE-bench / Polyglot | Python | none |
| SICA (2025) | yes, self-referential | SWE-bench Verified, cost | Python | none |
| **PiGo (this work)** | **yes** | **human-reviewed PR + gates** | **Go (typed, compiled)** | **required** |

Two dimensions here are, to our knowledge, unreported in the literature.

**Compiled and statically typed self-modification.** Every system above is Python. A
Python agent can adopt a self-modification that would fail to compile in Go only at
runtime; a Go agent that cannot build has produced *no* candidate agent. This shifts the
cheapest verification rung earlier (§5, `make build`) and makes type errors a first-class
part of self-improvement feedback rather than a runtime surprise.

**A human gate as the fitness function.** DGM's archive assumes the benchmark score
ranks candidates. PiGo assumes a human will read the diff, and the agent's obligation is
to make that reading possible: measure the effect, state what was verified, and prove the
guard fails when the bug returns. Where DGM searches a population, PiGo must persuade one
reviewer.

---

## 3. The subject system

PiGo is a Go coding agent built on Google ADK Go, comprising 313,497 lines of Go. Its
relevant subsystems for this paper:

| Subsystem | Location | Role in self-improvement |
|---|---|---|
| Agent runtime | `internal/agent/` | run loop, retries, event stream, grounding |
| Subagents | `internal/subagent/` | worktree isolation, orchestration, merge-back |
| Tools | `internal/tools/` | read/write/edit/bash/grep/git-*/lsp/subagent + compaction |
| Session + compaction | `internal/session/` | persistence, context compaction |
| Memory | `internal/memory/` | SQLite FTS5 observation store |
| Eval | `internal/eval/` | trajectory and tool-efficiency measurement |
| Skills + instructions | `internal/extension/`, `AGENTS.md` | durable self-authored instruction |

Three properties make it a viable self-improvement subject:

1. **Its own toolchain is its own tool.** PiGo edits Go with `edit`, builds with `bash`,
   and reads diagnostics with the LSP tool. No external developer environment is needed.
2. **Session and memory persistence.** Sessions are written to
   `$HOME/.pi-go/sessions/<id>/` (`events.jsonl`, `trajectory.atif.json`), and a
   long-lived observation store — SQLite with FTS5 virtual tables
   (`internal/memory/db.go:73,93`) and an `Observation` record carrying `SourceFiles`,
   `ToolName`, and token cost (`internal/memory/types.go:39`) — is queryable across
   sessions. This is what lets a later session reason about an earlier one.
3. **A durable instruction file.** `AGENTS.md` is machine-read project instruction; a
   self-authored rule written there governs future edits (§6.4).

---

## 4. Evidence of self-modification

All figures below were produced by commands run against the live repository on
2026-09-20; the commands are given so each can be reproduced.

### 4.1 Commit attribution

The repository's *local* git identity is the agent:

```
$ git config user.name && git config user.email
pi
dimetron@me.com
```

Consequently, agent-authored work is attributable rather than anonymous:

```
$ git log --all --author='^pi <' --oneline | wc -l
140
$ git log --all --author='^pi <' --format='%ad' --date=short | sort | sed -n '1p;$p'
2026-06-02
2026-09-20
$ git log origin/main --author='^pi <' --oneline | wc -l
85
```

The `--all` count is measured over all local refs and is therefore unstable: it moved
between 140 and 141 while this paper was being written, as worktree branches were created
and removed. The `origin/main` figure (85) is the stable one and is used throughout; the
`--all` figure is given only to show how much agent work lives on unmerged local branches
(the remainder being worktrees under `.pi-go/tasks/` and `.claude/worktrees/`).

Of the 85 agent commits on `origin/main`, signature status (`%G?`):

```
$ git log origin/main --author='^pi <' --format='%h %G?' | awk '{print $2}' | sort | uniq -c
   2 B
  83 G
```

**83 of 85 (97.6%) verify `G` (good signature); 2 verify `B` (bad).** Note the identity
split: the *commit* author is `pi` (git identity), while the *pull request* author is
`dimetron` (the `gh` token identity) — 41 of 41 agent-commit PRs were opened under the
human's GitHub account. Attribution and authorization are therefore separate mechanisms,
which §5 treats as a feature.

### 4.2 Pull requests

| Metric | Value |
|---|---|
| Total PRs (all states) | 347 |
| Merged, all time | 324 |
| Merged in 2026-06-02 .. 2026-09-20 | 303 |
| **PRs with ≥1 `pi`-authored commit** | **41** |
| — of those, merged | 38 |
| — of those, open | 3 (#352, #366, #368) |
| Share of merged PRs in window | **38 / 303 = 12.5%** |
| First agent-PR | #319, 2026-09-04 |
| Last agent-PR | #369, 2026-09-20 |
| PRs opened by the human (`dimetron`) | 41 / 41 |

The agent-commit stream is recent and accelerating: it begins at #319 on 2026-09-04 and
runs through #369 on 2026-09-20 — 41 PRs in 17 days.

### 4.3 How the agent acts on git

A finding worth stating plainly, because it constrains the safety story: **no Go code in
PiGo invokes `gh`.** There is no `gh pr create` in `internal/` or `cmd/`. PR creation is
*prompt-level* — the four documented commands (`git add -A`, `git commit -s -S`,
`git push -u origin <branch>`, `gh pr create --fill --web`) are in `AGENTS.md:352-371`
and in the local `self-improve` skill, executed through the generic `bash` tool.

The Go code that does manipulate git is `internal/subagent/worktree.go`:

- `Create` (`:271`) / `addWorktree` (`:252-261`) — `git worktree add -b <branch> <path> HEAD`
- `stashBeforeWorktreeAdd` (`:235`) then `popStashByMessage` (`:148`) — a dirty tree is
  stashed before `worktree add` and restored after
- `CreateBackupBranch` (`:419`) — `git branch -f <backup> <branch>`; the backup outlives
  cleanup
- `MergeBack` (`:534`) — `git merge --no-ff <branch> -m "Merge subagent <shortID>"`
- `CommitAll` (`:365`) — **the only commit in the package**, and deliberately *not*
  `git commit`:

```go
// It is written with plumbing (write-tree/commit-tree) rather than
// `git commit` so that it runs no hooks and no signing: this is a machine
// snapshot taken to avoid losing data, and a failing pre-commit hook on
// half-finished agent work must not be able to turn that into a total loss.
// The merge the caller makes afterwards still follows the user's own config.
```

This is a deliberate two-tier scheme: **snapshots are hook-free and unsigned; only the
final, human-approved change is signed.** `CommitAll` force-adds (`git add -Af`) because
plain `git status --porcelain` silently drops ignored artifacts — `**/specs/` and
`.pi-go/` match the paths agents actually write into, so a status-first gate would have
discarded the work.

Worktree isolation: branches default to `pi-agent-<shortID>`
(`internal/subagent/worktree.go:75-81`), worktrees live at `.pi-go/tasks/<pathID>`, and
`.pi-go/` is gitignored — so agent scratch space never appears as repository noise.
Notably, **0 remote branches and 0 PR head branches match `pi-agent-*`**: those branches
are local and merged back into the invoking branch.

### 4.4 Worked example: PR #368

PR #368 (`feat/compactor-fix-all-tools`, 812 additions / 289 deletions across 8 files,
2 commits, both `verified=true`) is the clearest complete instance of the loop. Its
subject is the agent's own tool-output compaction:

| Property | Value |
|---|---|
| Commits | 2, both authored `pi <dimetron@me.com>` |
| GitHub verification | `true valid` (both) |
| Defect found | 7 of 9 compaction pipelines were **no-ops in production** |
| After fix | 0 of 9 dead |
| Method | measured end-to-end through the real `BuildCompactorCallback` using each tool's actual output struct |
| Documented result | `git-file-diff` 99.6% reduction; `ripgrep` 75.4%; `git-overview` 49.0% |

The agent found that its own pipelines were *green in tests and dead in production* —
three independent silent mismatches: a probe-order guess for the write-target field
(`stdout → content → output → diff → result → data`, where no tool emits `output`), routing
on `git_file_diff` while tools register `git-file-diff`, and pipelines reading fields their
tools never emit. It then recorded the invariants in `AGENTS.md` and measured itself
against an external tool (`rtk`) on identical input bytes rather than reasoning about it.

---

## 5. The adoption gate

The paper's central claim is that the *gate* — not the ability to self-modify — is the
design problem. A self-modification in PiGo must pass six mechanisms, in this order:

| # | Mechanism | Rejects | Cost of failure |
|---|---|---|---|
| 1 | Spec | unfalsifiable or misconceived work | wrong work, done well |
| 2 | Worktree isolation | collateral damage to the primary checkout | lost uncommitted work |
| 3 | Build / type check | non-compiling candidates | none — no candidate produced |
| 4 | Test + lint + vet + vuln gate | behavioural regressions | silent correctness loss |
| 5 | Signature + sign-off | unattributable commits | unverifiable provenance |
| 6 | **Human review on the PR** | **plausible-but-wrong changes** | **the system's fitness signal** |

Ordering carries information. A typed language makes rung 3 nearly free, so bad
candidates die before consuming review attention. Rung 6 is the only one that is not
mechanically checkable, and it is where the *judgment* the agent cannot yet perform
resides — which is precisely why it is retained.

Two design decisions make rung 6 tractable rather than merely obstructive:

- **The agent must measure.** PR #368 does not assert the pipelines were dead; it tabulates
  before/after bytes per tool. Numbers are reviewable; claims are not.
- **The agent must prove its guard can fail.** For a change whose value is regression
  prevention, the agent reintroduced the bug deliberately to confirm the guard went red,
  then restored it. "A guard that cannot fail is worthless" — the agent's own stated rule.

### 5.1 Why the human gate is a fitness signal and not a bottleneck

In DGM the archive ranks candidates automatically. PiGo cannot: there is no score for
"is this good architecture". But the human gate is not merely a safety interlock — it is
where the objective function lives. The agent's *task* is to convert a human's unstated
judgment into explicit, checkable claims: a measurement, a failing-then-passing guard, a
documented invariant. Each merged PR is a datapoint that the conversion succeeded.

That reframing has a testable consequence: **self-modification quality should correlate
with artifact quality, not with diff size.** PR #368 is 289 deletions of mostly-dead code
plus one 160-line guard file and a 58-line `AGENTS.md` section. The deletions are the
fix; the guard and the documentation are the reviewability.

---

## 6. Verification mechanisms that caught real defects

Four mechanisms found defects that would otherwise have shipped, all observed in our own
record.

**6.1 The spec was wrong, and the code corrected it.** PR #368's spec asserted that
`compactGrep` must "read `matches` (a `[]GrepMatch`)". It cannot: ADK round-trips every
tool result through `json.Marshal`/`json.Unmarshal` (adk v2.4.0, `internal/typeutil/convert.go`),
so `matches` arrives as `[]any` of `map[string]any` and a typed assertion fails. Worse,
re-rendering the array to a string would have broken the TUI, which reads those same keys
as `[]any` (`internal/tui/tool_display.go:897-984`). The caps therefore **trim the array
and preserve its type**. A spec is an input to verification, not an authority over it.

**6.2 The agent's own new test caught a bug the agent had just introduced.** `truncated`
carries `omitempty`, so it is absent from a complete result; the agent's conditional write
skipped it, meaning a capped list would have been read as the complete answer. Fixed by
writing named keys unconditionally.

**6.3 Synthetic fixtures hide dead code.** The seven dead pipelines were *green in tests*
because the tests fed maps carrying an `output` key no tool emits. The fix included
rebuilding the tests to marshal the *real* output structs (`buildProdResult`). This is the
single most transferable lesson in our record: a test suite that does not exercise the
production representation of data cannot detect that production is broken.

**6.4 The invariant is written back.** `AGENTS.md` gained a *Tool-output compaction*
section recording: route on the registered name; write only the field you read (via
`CompactWrite{Key,Value}`); keep the value's type; preserve the pre-cap total; decline when
nothing would shrink; test against real output structs — plus the measurements behind the
decision *not* to wire in the external `rtk` binary. This converts one session's discovery
into all future sessions' constraint, including the local `self-improve` skill that now
encodes the loop. **It is the mechanism by which PiGo's history becomes PiGo's policy.**

**6.5 TUI output safety.** Two `log.Printf` calls in the compaction path were removed:
they ran inside an `AfterToolCallback` while the TUI owned the terminal's alternate
screen, where any stdout/stderr write corrupts the display. The panic was recorded as a
metric-bearing technique instead. This class of bug is invisible to tests and obvious to a
user, which is exactly why the human gate earns its place.

---

## 7. Observed failure modes

We report these because a safety claim without its exceptions is not a measurement.

**7.1 Two agent commits reached `main` without a PR.** `cb7c5b3` and `dcf67b4`
(2026-09-06) are `pi`-authored commits on `main` that appear in no PR payload. The
"self-modification always passes review" model has exceptions; the gate is a convention
the agent follows, not an enforced invariant for direct pushes.

**7.2 Two commits on `main` have invalid signatures.** 2 of 85 verify `B`. A `gpgsig`
header check passes on both — the header only records that signing was *attempted*, and
rewriting a commit object (`git hash-object -t commit -w`) keeps the header while
invalidating the signature. The correct verification is `git verify-commit`, not a header
grep. We note this because the naive check is the one an agent would naturally write, and
it silently passes on broken provenance.

**7.3 Signature verification requires out-of-band setup.** Verification reports `N` for
signed and unsigned commits alike unless `gpg.ssh.allowedSignersFile` exists. Without it,
the gate is decorative. (Signing here is SSH-format via 1Password, not GPG.)

**7.4 Cost.** 41 PRs in 17 days is not free. Self-modification consumed a substantial
share of the agent's own operating budget, and much of it went into measurement and guard
construction rather than production code. We regard that ratio as the price of
reviewability, but it should be stated rather than hidden.

---

## 8. Limitations and a negative result

**No benchmark improvement delta is reported.** We ran no SWE-bench or comparable
evaluation; we therefore make **no claim that PiGo's self-modifications made it a better
coding agent in the DGM or SICA sense**. What we report is that self-modifications were
produced, gated, adopted, and retained — a *deployment* claim. Conflating the two is the
most likely misreading of this paper, and we state it to prevent it.

We argue this negative result is the honest shape of evidence for production
self-modification. In a production system the fitness function is not a score the
researcher chooses; it is whether the change helped, judged by someone who must live with
it. That signal is real, valuable, and not a number — and reporting it as a number would
require inventing a proxy. The artifact-verified PR record is our substitute.

Further limitations:

- **n = 1.** One agent, one repository, one author-of-record. The identity split
  (commit author `pi`, PR author `dimetron`) means PR-level attribution is by commit
  payload, which requires per-PR commit fetches to compute.
- **Not peer reviewed.** This is a project report, not a paper with review.
- **The human gate is never removed**, so we demonstrate the *mechanism* of
  self-improvement, not autonomous recursive self-improvement. DGM and SICA make the
  stronger claim; we deliberately do not.
- **Selection bias.** We report the PRs the agent proposed. Rejected self-modifications
  are not enumerated here, which would be required to estimate the gate's rejection rate.
- **Provenance statistics depend on local configuration.** `user.name = pi` is repo-local;
  a fresh clone would attribute agent work to whatever identity is configured, and the
  140/85 counts are not reproducible without it.

---

## 9. Reproducing the artifact check

The final loop-closing step — the agent rebuilding and re-running *itself* — is
executable and was run for this paper:

```bash
$ go build ./cmd/pi && go install ./cmd/pi/ && $HOME/go/bin/pi --version
pi version dev
```

`--version` prints `pi version dev` for a local build; that is expected, not a failure.
The installed binary is then smoke-tested as a *running program*, since compiling is not
running:

```bash
$ $HOME/go/bin/pi --mode print "reply with exactly: ok"
✅ ✓ skills: loaded 67
ok                                        # exit 0

$ $HOME/go/bin/pi audit
Scanned 65 file(s): no hidden characters found.
```

The distinction matters for the safety argument: a self-modification that compiles but
cannot start has passed rung 3 and failed rung 6, and only the *running* binary
distinguishes them. This is the step that closes the loop — the next session executes the
modified code.

Reproduce the provenance statistics with:

```bash
git log origin/main --author='^pi <' --oneline | wc -l                    # 85
git log origin/main --author='^pi <' --format='%h %G?' | awk '{print $2}' \
  | sort | uniq -c                                                          # 2 B / 83 G
gh api repos/dimetron/pi-go/commits/<sha> \
  --jq '.commit.verification | "\(.verified) \(.reason)"'                   # true valid
```

---

## 10. Conclusion

DGM and SICA established that a coding agent can rewrite itself and improve on a
benchmark. PiGo shows something adjacent and, we think, more directly relevant to
practice: an agent can rewrite itself *in production*, in a typed and compiled language,
with every change attributable, gated, reviewable, and — once merged — executed by the
next session of the agent that wrote it.

The transferable findings are four:

1. **The gate, not the self-modification, is the design problem.** Capability is
   demonstrated; adoption is where engineering lives.
2. **Measurement is the currency of review.** An agent that tabulates the before/after
   effect of its own change is reviewable; one that asserts it is not.
3. **Test fixtures must use production representations.** Seven of nine pipelines were
   green in tests and dead in production because fixtures fed a field no tool emits.
4. **Write the lesson back into the instruction file.** This is what converts an
   individual self-modification into durable constraint — and it is the smallest,
   cheapest, and most durable form of self-improvement we observed.

A self-improving agent does not need to be trusted to be useful. It needs to be
*auditable*, and the merge request is the audit.

---

## References

1. J. Schmidhuber. *Gödel Machines: Fully Self-Referential Optimal Universal
   Self-Improvers.* 2003.
2. J. Zhang, S. Hu, C. Lu, R. Lange, J. Clune. *Darwin Gödel Machine: Open-Ended
   Evolution of Self-Improving Agents.* arXiv:2505.22954, ICLR 2026.
3. M. Robeyns, M. Szummer, L. Aitchison. *A Self-Improving Coding Agent.*
   arXiv:2504.15228, 2025.
4. X. Yin, X. Wang, L. Pan, X. Wan, W. Wang. *Gödel Agent: A Self-Referential Framework
   for Recursively Self-Improvement.* 2024.
5. S. Hu, C. Lu, J. Clune. *Automated Design of Agentic Systems (ADAS).* 2024.
6. D. Lenat. *EURISKO: A Program That Learns New Heuristics and Domain Concepts.* 1983.
7. *Mendel Gödel Machine: Recursive Self-Improving Coding Agents.* arXiv:2608.07645.
8. Google. *Agent Development Kit for Go (ADK Go).* v2.4.0.
9. pi-go repository. github.com/dimetron/pi-go — PR #368, and `AGENTS.md`, `ARCHITECTURE.md`.
