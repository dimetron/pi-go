---
name: code-review-codex
description: Use the OpenAI Codex CLI in read-only mode to review pi-go diffs, commits, or branches and save the final review to ./specs/issues/002-code-review-codex/PROMPT.md.
metadata:
  author: dimetron
  version: "1.0"
---

# Codex Review

Use the external `codex` CLI as a second-opinion reviewer for `pi-go`.
This skill is for read-only review and findings, not code editing.
The only intended output is `./specs/issues/002-code-review-codex/PROMPT.md`.

The command surface below was verified locally against `codex-cli`.

## CLI constraint: `--uncommitted`/`--base`/`--commit` vs `[PROMPT]`

These flags are **mutually exclusive** with a positional prompt argument.
If you need custom instructions alongside one of these flags, pass `-` as the
prompt and pipe instructions via heredoc stdin:

```bash
codex exec -s read-only review --uncommitted -m gpt-5.4 -o file.md - <<'EOF'
Your instructions here
EOF
```

**Never** combine `--uncommitted "prompt text"` — it will fail with exit code 2.

## Default output path

The canonical saved-review artifact for this skill is:

```text
./specs/issues/002-code-review-codex/PROMPT.md
```

When using this skill, prefer commands that overwrite that file with the latest Codex review.
Do not use this skill to edit source files, run fix-up commands, or write any other artifact.

## When to use

Use this skill when you want Codex CLI to review:

- uncommitted changes in the working tree
- a feature branch against `origin/main`
- a single commit before push
- a change for pi-go-specific risks, not just generic style issues

Do not use this skill to replace repo validation. Codex findings are advisory; the real gates are still:

```bash
make build
make test
make lint
make vet
```

Use targeted tests first when the review points to a specific package, for example:

```bash
go test ./internal/agent/...
go test -race ./...
```

Those validation commands are follow-up work after the review. They are not part of the review invocation described by this skill.

## Repo rules to mention in the prompt

Tell Codex to review against these `pi-go` constraints:

- prefer native ADK interfaces such as `model.LLM`, `tool.Tool`, and `session.Service`
- register tools with `tool.NewFunctionTool` and keep core wiring in `tools.CoreTools()`
- keep sandboxed file access rooted to the working directory via `os.Root`
- wrap errors with context using `fmt.Errorf("context: %w", err)`
- avoid `init()` functions
- keep the repo as a single Go module
- do not bypass sandbox restrictions in tool code
- keep changes focused and minimal
- update docs when behavior or developer workflow changes
- check for missing tests and regressions, not style nits

## Prerequisites

1. Run from the repo root: `/Users/dimetron/p6s/pi-dev/pi-go`
2. Create the output directory if needed: `mkdir -p ./specs/issues/002-code-review-codex`
3. If you are outside the repo, use `codex exec -C /Users/dimetron/p6s/pi-dev/pi-go ...` and an absolute `-o` path
4. Confirm Codex CLI is available: `codex --version`
5. Login if needed: `codex login`
6. For branch review, refresh the base ref first if needed: `git fetch origin main`

The current default remote head in this repo is `origin/main`.

## Review contract

The review command must satisfy all of these:

- run via `codex exec -s read-only review`
- produce findings only, not code edits
- write the final review to `./specs/issues/002-code-review-codex/PROMPT.md`
- avoid extra output artifacts

The only allowed local writes are:

- `mkdir -p ./specs/issues/002-code-review-codex`
- `-o ./specs/issues/002-code-review-codex/PROMPT.md`

## Preferred commands

### 1. Review uncommitted changes and save the result

**Important:** `--uncommitted` and a positional `[PROMPT]` are mutually exclusive in `codex exec review`.
Pass custom instructions via stdin with `-` as the prompt argument.

```bash
mkdir -p ./specs/issues/002-code-review-codex
codex exec -s read-only review \
  -m gpt-5.4 \
  --uncommitted \
  --ephemeral \
  -o ./specs/issues/002-code-review-codex/PROMPT.md \
  - <<'EOF'
Review this pi-go change in read-only mode. Do not propose or apply edits.
Only generate a review report. Focus on bugs, regressions, missing tests,
missing error context, ADK misuse, sandbox escapes, and docs drift.
Report only high-confidence findings with file:line references.
EOF
```

Or without custom instructions (uses Codex defaults):

```bash
mkdir -p ./specs/issues/002-code-review-codex
codex exec -s read-only review \
  -m gpt-5.4 \
  --uncommitted \
  --ephemeral \
  -o ./specs/issues/002-code-review-codex/PROMPT.md
```

Use this before committing local work. This is the default saved-review workflow.

### 2. Review the current branch against `origin/main` and save the result

**Note:** `--base` also conflicts with a positional prompt. Use stdin via `-`.

```bash
mkdir -p ./specs/issues/002-code-review-codex
codex exec -s read-only review \
  -m gpt-5.4 \
  --base origin/main \
  --ephemeral \
  -o ./specs/issues/002-code-review-codex/PROMPT.md \
  - <<'EOF'
Review this pi-go branch against origin/main in read-only mode.
Do not propose or apply edits. Only generate a review report.
Prioritize correctness, compatibility, tests, os.Root sandboxing,
tool.NewFunctionTool/tools.CoreTools wiring, %w error wrapping,
and documentation updates.
EOF
```

Use this for a PR-style review.

### 3. Review a single commit and save the result

```bash
mkdir -p ./specs/issues/002-code-review-codex
codex exec -s read-only review \
  -m gpt-5.4 \
  --commit HEAD \
  --ephemeral \
  -o ./specs/issues/002-code-review-codex/PROMPT.md \
  - <<'EOF'
Review this pi-go commit in read-only mode. Do not propose or apply edits.
Only generate a review report. Flag only important issues,
with severity and file:line references.
EOF
```

Replace `HEAD` with any commit SHA when needed.

## Prompt templates

### Short prompt

```text
Review this pi-go change in read-only mode. Do not propose or apply edits. Only generate a review report in ./specs/issues/002-code-review-codex/PROMPT.md. Focus on bugs, regressions, missing tests, ADK misuse, sandbox escapes, missing %w error wrapping, and docs drift. Report only high-confidence findings with file:line references.
```

### Deep prompt

```text
Review this pi-go change against the repo rules in AGENTS.md and ARCHITECTURE.md.
Run in read-only mode. Do not propose or apply edits. Only generate a review report in ./specs/issues/002-code-review-codex/PROMPT.md.

Prioritize:
- correctness and behavior regressions
- missing tests or missing package-level test updates
- ADK-native interfaces (`model.LLM`, `tool.Tool`, `session.Service`)
- tool registration via `tool.NewFunctionTool` in `tools.CoreTools()`
- sandbox safety with `os.Root`
- error wrapping via `fmt.Errorf("context: %w", err)`
- accidental `init()` usage
- single-module assumptions
- documentation drift in README.md, ARCHITECTURE.md, or skill docs

Return only high-confidence findings. Include severity, file:line references, and a concrete fix.
```

## Option reference

- `codex exec -s read-only review`: required command shape for this skill
- `codex exec -s read-only review -m gpt-5.4`: choose the model explicitly
- `codex exec -s read-only review -o ./specs/issues/002-code-review-codex/PROMPT.md`: save the last message to the canonical review-results file
- `codex exec -s read-only review --ephemeral`: avoid persisting session files
- `codex exec -s read-only -C /path review ...`: run against a repo without changing directories

Important:

- Use `-s read-only` on `codex exec` so the model cannot mutate the repo
- Use `-o ./specs/issues/002-code-review-codex/PROMPT.md` so the review result lands in the canonical file
- Do not use the top-level `codex review` form for this skill because the skill requires a saved artifact and explicit read-only sandboxing
- If you need a multiline prompt, pass `-` as the prompt and provide the instructions on stdin

## Review checklist for pi-go

Ask Codex to specifically look for:

- missing or weak tests in changed packages
- regressions in CLI behavior, TUI flows, or JSON-RPC paths
- incorrect provider/model-role wiring
- ADK wrappers that add unnecessary abstraction
- tool registration drift away from `tools.CoreTools()`
- unsafe file access or sandbox escape paths
- missing `%w` error wrapping
- stale docs when CLI flags, behavior, or workflows changed

## Follow-up after the review

After reading the findings:

1. Open `./specs/issues/002-code-review-codex/PROMPT.md` and confirm each finding against the actual code
2. Fix only the real issues
3. Run the appropriate validation commands

Minimum validation for most code changes:

```bash
make build
make test
make lint
make vet
```

Use these when relevant:

```bash
make test-coverage
make test-e2e
go test ./internal/agent/...
go test -race ./...
```

## What this skill found (2026-04-09)

This skill caught two real regressions that tests and linters would not catch:

**P2:** `interactive.go:105` — `pi --continue <id>` ignores the ID argument because `--continue`
is a boolean flag. Codex correctly identified this behavioral mismatch.

**P3:** `tui.go:924-925` — Seeding `inputModel.Text = " "` after init breaks slash-command
cycling, because `HandleKey` only enters that path from an empty buffer. Codex read the
actual `HandleKey` logic and caught this.

This confirms that Codex finds issues that `make vet`/`make lint` miss — especially
behavioral regressions and unintended side effects in the TUI and CLI layers.

## Guidelines

- Prefer `origin/main` as the review base for branch reviews
- Always use `codex exec -s read-only review` when using this skill
- Always write the result to `./specs/issues/002-code-review-codex/PROMPT.md`
- Do not use this skill for ad-hoc review output, JSONL automation output, or code-editing workflows
- Keep prompts specific to pi-go conventions; generic prompts miss important repo rules
- Ask for high-confidence findings only
- Do not use `--dangerously-bypass-approvals-and-sandbox` for review
