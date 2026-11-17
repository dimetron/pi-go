# AGENTS.md — pi-go

Rules for coding agents in this repo (Claude Code and pi-go's own agent). This
file is injected into every session, so it holds only the rules that always
apply. The reasons, commands and history behind them live in skills under
`.pi-go/skills/<name>/SKILL.md` — read the named skill before doing that kind of
work, even if your harness does not list it as a skill.

| Before you… | Read |
|---|---|
| hit a failing commit/push hook, verify or re-sign commits, rewrite history | `git-workflow` |
| create a PR, run or resolve a Codex review, merge | `pr-workflow` |
| debug a failure that only shows in Windows CI | `windows-ci` |
| touch `internal/tools/compactor*.go`, `dedup.go`, an after-tool callback, or a tool's output struct | `tool-compaction` |
| report a large saving, speedup or "this fixes it" | `proof-check` |
| record a GIF or screen recording | `vhs-e2e-gif` |

Instruction files closer to the code add constraints: before editing under a
directory, check it and its parents for an `AGENTS.md` (currently `specs/` and
`hack/agentgateway/`).

## Git

- **Work in a worktree, never the primary checkout** — uncommitted edits there
  are lost when its branch is switched. Do not nest: if
  `git rev-parse --git-dir` differs from `git rev-parse --git-common-dir` you
  are already in a worktree, so work there. Otherwise:

  ```bash
  git worktree add -b fix/<topic> .worktrees/fix-<topic> HEAD
  ```

  Remove it (`git worktree remove`) once the branch is merged or abandoned.
- **Every commit is signed and signed-off:** `git commit -s -S -m "..."`. No
  exceptions for merges, reverts or WIP.
- **Check signatures with `git verify-commit`,** never by looking for a `gpgsig`
  header. Fix an invalid one by re-signing
  (`git rebase --exec 'git commit --amend --no-edit -S' origin/main`), never by
  hand-editing commit objects. GitHub-created merge commits fail the local check
  and are fine.
- **Never pass `--no-verify`.** It skips the signing hooks too. If a hook fails
  on something your branch did not touch (usually `golangci-lint`), stop and
  report it.
- **Never commit a GIF or recording** (limit 1 MiB; 10 MiB for any other file).
  Attach recordings to the PR as a release asset with the `vhs-e2e-gif` skill.
- **Agents marked `[worktree]` edit an isolated tree** — their edits do not land
  in yours; ask for a patch or use a non-worktree agent.

## Pull requests

- Commit **all** pending changes, run build, tests, lint and vet, push, create
  the PR, and open its URL in the browser.
- **The PR is the review track:** Codex posts one formal GitHub review to the
  PR; resolve every finding in its own thread, with the fixing commit or the
  reason for dismissing it. Verify each finding against the code first.
- After the review, run `make vulncheck` and post the result on the PR. Do not
  use `make check-cve` for this — it rewrites tracked files.
- **Merge with "Create a merge commit" or "Squash and merge", never "Rebase and
  merge"** (it lands unsigned commits). Run `git verify-commit` on what landed.
- **Never put an agent session link or AI attribution** in a PR body, title,
  commit message or review comment.

## Build, test, lint

Go 1.27.3. Prefer the Makefile over raw `go` commands: `make build`, `install`,
`test` (= `test-unit`), `test-integration`, `test-e2e`, `test-all`,
`test-coverage`, `lint` (golangci-lint v2), `vet`, `vulncheck`.

- **Tooling is Go, not Python.** A checker or harness is a `go run` target under
  `hack/` with a `main_test.go` beside it (see `hack/vulngate`). Shell only
  orchestrates; parsing and verdicts belong in tested Go. If a third-party tool
  ships only Python, ask the user first.
- **Windows CI catches what macOS cannot:** close every `tools.NewSandbox` in
  tests (`t.Cleanup(func() { _ = sb.Close() })`, or the `testSandbox` helper),
  and never hard-code the search tool's name — it registers as `ripgrep` or
  `grep` depending on the host.
- **VS Code extension:** `cd vscode && make install`. Build output goes to
  `$HOME/.vscode-ext/`, never the repo. CI uses `make release`, which leaves the
  versioned VSIX at the repo root for the upload glob — do not move it.

## TUI: never write to stdout or stderr

The TUI owns the alternate screen; any stray write corrupts it. In code that
runs while the TUI is active (agent loop, callbacks, hooks, commands):

- No `fmt.Print*`, `log.Print*`, `stdlog`, `os.Stdout` or `os.Stderr`.
- Diagnostics go through the session logger (`m.cfg.Logger`).
- Text for the user goes through the chat transcript, `SystemNoticeCh`, or the
  TUI's own status rendering.
- Only a panic handler may write to stderr, as a last resort.

When in doubt, grep the package for `Printf|Println|os.Stdout|os.Stderr|stdlog`.

## Tool-output compaction and after-tool callbacks

Read `tool-compaction` before changing this area. The rules that bite most:

- Route a pipeline on the name the tool registers (`git-file-diff`, `ripgrep`).
- A pipeline writes only the field it read, keeps the value's type, and leaves
  the pre-cap `total_*` count intact.
- **ADK runs only the first after-tool callback that returns non-nil.** Always
  pass the chain through `extension.ComposeAfterToolChain`; a non-final stage
  returns `(nil, nil)` to continue.
- Build test payloads by marshalling the tool's real output struct, never a
  hand-written map.

## Proving a win

A large improvement number is a hypothesis. Before reporting one: measure at the
size the producer can really emit, use fixtures built from real structs, prove
it end to end (`make eval-tools`), check the information survived as well as the
size, and see the guard fail once. Say which numbers you did not verify.
`proof-check` has the full list.

## Profiling

`pi --pprof true` serves pprof on `http://localhost:6060/debug/pprof`; scripts
are in `.claude/skills/go-pprof/scripts/`. One sample cannot tell churn from a
leak — establish drift with `pprof-watch.sh`, in the state being complained
about. Findings go in `TODO.md` under `## PPROF` and in `MEM_PPROF.md`.

## Sessions and local notes

- Sessions are in `$HOME/.pi-go/sessions/<session-id>/`, not the repo:
  `meta.json`, `events.jsonl` (the turn stream), `trajectory.atif.json` (tool
  calls), `branches.json`. Runtime logs are in `$HOME/.pi-go/log/`; the
  `session-stats` tool scans sessions for anomalies.
- `TODO.md` and `MEM_PPROF.md` are gitignored local notes — a teammate cannot
  see them. `TODO.md` numbering restarts per section; duplicate numbers are
  expected.
