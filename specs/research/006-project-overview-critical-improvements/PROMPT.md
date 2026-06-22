# Execution Prompts: Critical Repository Improvements

Use these prompts to execute `PLAN.md` phase by phase. Each prompt is designed to be handed to a coding agent
independently.

General rules for every phase:

- Read `specs/research/006-project-overview-critical-improvements/OVERVIEW.md` first.
- Read `specs/research/006-project-overview-critical-improvements/PLAN.md` for the target phase.
- Make the smallest correct change for the requested phase only.
- Do not mix phases unless explicitly asked.
- Preserve sandbox safety and existing public behavior unless the phase explicitly changes it.
- Run the verification commands listed for the phase.
- Report changed files, verification results, and any follow-up work.

---

## Phase 0 Prompt — Baseline and Safety Checks

```text
You are working in the pi-go repository.

Goal: establish the current build/test baseline before implementing critical repository improvements.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 0

Tasks:
1. Record current git status.
2. Run:
   - go test ./...
   - go test ./internal/tools/...
   - go test ./internal/subagent/...
   - go test ./internal/cli/...
   - make build
3. If any command fails, do not fix it unless the failure is obviously caused by your own changes. Instead, document it as a baseline failure.
4. Add a “Baseline failures” section to PLAN.md only if failures exist. Include command, package/file, error summary, and whether it appears pre-existing.

Constraints:
- Do not change production code.
- Do not refactor.
- Do not run e2e/integration tests unless asked.

Verification:
- The listed commands were run or skipped with a clear reason.
- PLAN.md contains baseline failures only if failures were observed.

Final response:
- Summarize command results.
- List any changed files.
- State whether it is safe to proceed to Phase 1.
```

---

## Phase 1 Prompt — Documentation and Version Consistency

```text
You are working in the pi-go repository.

Goal: fix contributor-facing documentation inconsistencies identified in the critical improvements plan.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 1
- README.md
- ARCHITECTURE.md
- TODO.md
- go.mod

Tasks:
1. Update README.md Go requirements to match go.mod. If go.mod says `go 1.26.3`, document this as Go 1.26.3+ or Go 1.26+ consistently with project intent.
2. Search for stale `internal/rpc` references in docs and specs:
   rg "internal/rpc|\\brpc/" README.md ARCHITECTURE.md specs docs internal cmd
3. Correct stale package references to `internal/jsonrpc` where that is the actual package name. If any `rpc` reference is conceptual rather than package-specific, leave it but make wording clear.
4. Update TODO.md so it is no longer effectively empty. It should point to active priorities, including this critical improvements plan, ISSUES.md, and ROADMAP.md.
5. Add or update a short “source of truth” note in README.md or ARCHITECTURE.md covering setup, architecture, operational issues, roadmap, and this plan.

Constraints:
- Documentation-only change.
- Do not change code.
- Do not rewrite large sections unnecessarily.

Verification:
- rg "Go 1\\.25" README.md ARCHITECTURE.md docs specs || true
- rg "internal/rpc" README.md ARCHITECTURE.md docs specs || true
- go test ./... if feasible; if not, explain why docs-only validation was used.

Final response:
- List changed files with line references.
- State whether stale Go/package references remain.
- Include verification results.
```

---

## Phase 2 Prompt — Tool Schema Validation Regression Tests

```text
You are working in the pi-go repository.

Goal: add regression coverage for tool schema validation and coercion failures documented in ISSUES.md.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 2
- ISSUES.md
- internal/tools/registry.go
- relevant existing tests under internal/tools/

Tasks:
1. Inspect existing coverage:
   rg "coerc|lenient|schema|additionalProperties|missing properties|alias" internal/tools -g '*_test.go'
2. Add focused tests for known LLM tool-call mistakes:
   - extra unknown properties do not fail before tool execution;
   - optional fields can be omitted without ADK pre-validation failure;
   - integer fields sent as strings are coerced;
   - boolean fields sent as strings are coerced;
   - array/object fields sent as JSON strings are parsed where supported;
   - aliases are remapped where aliases exist.
3. Prefer testing the smallest stable surface: `coercingTool`, schema helpers, or a representative core tool built with `newTool`.
4. If tests reveal a real bug, fix the minimal code path in internal/tools/registry.go or the relevant tool.
5. Refresh ISSUES.md status so historical schema failures are separated from current known behavior. If you cannot query local session logs, say so and update wording without inventing counts.

Constraints:
- Do not weaken model-facing required-field declarations unless necessary.
- Do not make schemas globally permissive in a way that hides useful model guidance.
- Keep changes limited to tool validation/coercion and issue documentation.

Verification:
- go test ./internal/tools/... -run 'Coerc|Schema|Tool' -count=1
- go test ./internal/tools/... -count=1
- go test ./... -count=1

Final response:
- List tests added and what each covers.
- List any code changes.
- Include verification results.
- State remaining schema-validation risks, if any.
```

---

## Phase 3 Prompt — Context and Cancellation Propagation

```text
You are working in the pi-go repository.

Goal: ensure request-scoped long-running operations honor cancellation instead of using unbounded background contexts.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 3
- internal/tools/subagent.go
- internal/tools/grep.go
- internal/tools/bash.go
- internal/acp/client/session.go
- relevant tests under internal/tools and internal/acp

Tasks:
1. Search for context misuse:
   rg "context\\.Background\\(\\)|context\\.TODO\\(\\)" internal cmd
2. Categorize occurrences into:
   - acceptable bootstrap/init use;
   - request-scoped use that should be cancelable.
3. Fix request-scoped long-running paths first:
   - ripgrep/subprocess calls;
   - bash command execution if not already cancelable;
   - subagent execution;
   - ACP session run loops where context is available.
4. Preserve existing timeout behavior. If a nil context is possible, use a bounded fallback and document it in code only if needed.
5. Add cancellation tests where practical:
   - long-running bash command exits when context is canceled;
   - grep/ripgrep exits on context cancel or timeout;
   - subagent cancellation if a stable test harness exists.

Constraints:
- Do not change public tool schemas.
- Do not add broad new abstractions unless necessary.
- Do not remove timeouts.
- Avoid flaky sleep-heavy tests; use short deadlines and deterministic commands.

Verification:
- go test ./internal/tools/... -run 'Cancel|Timeout|Context|Bash|Grep|Subagent' -count=1
- go test ./internal/acp/... -run 'Cancel|Context|Session' -count=1
- go test ./... -count=1

Final response:
- List each context.Background/TODO occurrence changed or intentionally left.
- Explain cancellation behavior now.
- Include verification results.
```

---

## Phase 4 Prompt — Subagent Worktree Path Escaping

```text
You are working in the pi-go repository.

Goal: fix subagent worktree path handling so normal repo files are accessible from worktree agents without using `../../` escape paths, while preserving sandbox security.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 4
- ISSUES.md section on subagent path escaping
- internal/subagent/
- internal/tools/subagent.go
- internal/tools/sandbox.go
- internal/cli/cli.go

Tasks:
1. Reproduce or create a minimal test for the documented failure: worktree subagent attempts to read a repo-root file using a path like `../../go.mod` and gets rejected.
2. Inspect path/root setup:
   rg "PI_SANDBOX_ROOT|PI_WORKTREE_ROOT|NewSandbox|worktree|Root" internal/cli internal/tools internal/subagent
3. Define and implement the path contract:
   - worktree agents should use paths relative to their own worktree root;
   - `../..` escape paths must remain rejected;
   - original repo metadata must be passed explicitly if needed, not by weakening sandbox checks.
4. Fix path construction at the subagent setup, environment, prompt, or sandbox-root boundary, whichever is the true source.
5. Add regression tests for:
   - reading a normal repo file from worktree root succeeds;
   - `../../go.mod` remains rejected;
   - absolute paths inside allowed root are handled consistently if supported.
6. Update ISSUES.md status for subagent path escaping.

Constraints:
- Do not weaken sandbox path escape checks.
- Do not allow arbitrary parent directory reads.
- Keep worktree and non-worktree agent behavior compatible.

Verification:
- go test ./internal/subagent/... -count=1
- go test ./internal/tools/... -run 'Sandbox|Worktree|Path|Escape' -count=1
- go test ./... -count=1

Final response:
- Describe the path contract implemented.
- List tests added.
- Confirm escape rejection remains intact.
- Include verification results.
```

---

## Phase 5 Prompt — CI and Release Workflow Parity

```text
You are working in the pi-go repository.

Goal: align release workflow test prerequisites with CI workflow test prerequisites.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 5
- .github/workflows/ci.yml
- .github/workflows/release.yml

Tasks:
1. Compare the CI test job setup with the release test job setup.
2. Update .github/workflows/release.yml so release tests install the same required external tools as CI tests, especially:
   - uv setup;
   - Rust toolchain fixture setup.
3. Keep action pinning style consistent with the repository.
4. Decide whether release race tests should use the same exclusion as CI for `internal/acp/server`.
   - If yes, update release command and add a concise comment.
   - If no, explain why release should differ and ensure it passes.
5. Avoid unrelated workflow changes.

Constraints:
- YAML-only change unless a reusable composite action is clearly worthwhile.
- Do not unpin existing actions.
- Do not remove existing release gates.

Verification:
- git diff -- .github/workflows/ci.yml .github/workflows/release.yml
- go test ./... -count=1
- If available, validate workflow syntax with an appropriate local tool; otherwise manually inspect YAML.

Final response:
- Summarize workflow differences before/after.
- Include changed files.
- Include verification results.
```

---

## Phase 6 Prompt — OTEL Console Exporter Decision

```text
You are working in the pi-go repository.

Goal: make `OTEL_TRACES_EXPORTER=console` behavior honest: either implement it or clearly mark it unsupported.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 6
- internal/otel/otel.go
- internal/otel/*_test.go if present
- README.md and docs mentioning OTEL

Tasks:
1. Inspect current OTEL exporter selection in internal/otel/otel.go.
2. Choose one approach:
   Option A: implement a real console/stdout/stderr trace exporter.
   Option B: remove/deprecate console support in docs and make configured `console` behavior clearly no-op with a warning.
3. Prefer Option B if console trace output risks corrupting TUI/JSON/RPC/ACP streams and no safe destination exists.
4. Update docs/comments to match behavior.
5. Add tests for exporter selection behavior if testable with current structure.

Constraints:
- Do not allow trace output to corrupt protocol stdout.
- Do not add a new dependency unless it is justified and acceptable for the project.
- Keep initialization quiet by default.

Verification:
- go test ./internal/otel/... -count=1
- go test ./... -count=1

Final response:
- State whether console exporter was implemented or deprecated.
- Explain why.
- List code/docs/tests changed.
- Include verification results.
```

---

## Phase 7 Prompt — Logging and Output Hygiene

```text
You are working in the pi-go repository.

Goal: prevent internal logs/debug output from corrupting TUI, JSONL, RPC, or ACP output streams.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 7
- internal/logger/
- internal/extension/hooks.go
- internal/lsp/hooks.go
- internal/tools/compactor.go
- internal/tui/agent_loop.go
- JSON/RPC/ACP output code under internal/cli, internal/jsonrpc, internal/acp

Tasks:
1. Find direct output/logging:
   rg "log\\.Printf|log\\.Println|fmt\\.Println|fmt\\.Printf|os\\.Stdout|os\\.Stderr" internal cmd
2. Categorize each occurrence as:
   - intentional user output;
   - protocol output;
   - debug/error logging;
   - bootstrap/fatal output.
3. For debug/error logs in production paths, route them through the project logger or explicit event channels instead of direct stdout/stderr/log package output.
4. Ensure JSONL/RPC/ACP stdout remains protocol-clean.
5. Add at least one regression test for output cleanliness where practical, e.g. JSON mode emits parseable JSONL only on stdout.

Constraints:
- Do not remove intentional user-facing output.
- Do not change protocol formats except to remove accidental noise.
- Do not introduce global logger behavior that breaks tests or TUI.

Verification:
- go test ./internal/cli/... -run 'JSON|Output|Log|RPC|ACP' -count=1
- go test ./internal/jsonrpc/... -count=1
- go test ./internal/acp/... -count=1
- go test ./... -count=1

Final response:
- List direct logging/output sites changed and sites intentionally left.
- Explain output-cleanliness guarantees.
- Include verification results.
```

---

## Phase 8 Prompt — Memory Architecture Clarification

```text
You are working in the pi-go repository.

Goal: clarify the relationship between `internal/memory` and `internal/palace` in docs and package comments.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 8
- README.md
- ARCHITECTURE.md
- internal/memory/
- internal/palace/
- relevant CLI wiring in internal/cli/

Tasks:
1. Inspect current usage:
   rg "internal/memory|internal/palace|memory\\.|palace\\." internal cmd README.md ARCHITECTURE.md
2. Determine the actual relationship:
   - Which package is active user-facing memory?
   - Which package is legacy, lower-level, transitional, or complementary?
   - Which CLI paths initialize/use each one?
3. Update README.md and ARCHITECTURE.md to describe memory consistently.
4. Add or update package comments/doc.go files for internal/memory and/or internal/palace if they are missing or unclear.
5. Avoid changing runtime behavior unless documentation reveals a small obvious naming bug.

Constraints:
- Documentation/package-comment focused.
- Do not redesign memory.
- Do not migrate data.
- Do not remove either package.

Verification:
- go test ./internal/memory/... ./internal/palace/... -count=1
- go test ./... -count=1

Final response:
- Summarize the documented memory architecture.
- List changed docs/package comments.
- Include verification results.
```

---

## Phase 9 Prompt — Planning Document Hygiene

```text
You are working in the pi-go repository.

Goal: make planning docs easy to navigate and keep current priorities discoverable.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 9
- TODO.md
- ISSUES.md
- ROADMAP.md

Tasks:
1. Add cross-links between:
   - TODO.md
   - ISSUES.md
   - ROADMAP.md
   - specs/research/006-project-overview-critical-improvements/OVERVIEW.md
   - specs/research/006-project-overview-critical-improvements/PLAN.md
2. Update TODO.md to show current top-priority fixes or explicitly point to this plan as the active prioritized list.
3. Add “last validated” dates to operational issue sections in ISSUES.md where counts are based on logs. If exact validation was not performed, say “not revalidated” rather than inventing a date/result.
4. Do not change roadmap commitments beyond linking active work.

Constraints:
- Documentation-only change.
- Do not mark work complete unless implementation and verification are done.
- Do not invent metrics.

Verification:
- rg "006-project-overview-critical-improvements|PLAN.md|OVERVIEW.md" TODO.md ISSUES.md ROADMAP.md specs/research/006-project-overview-critical-improvements

Final response:
- List docs changed.
- Explain where contributors should go for active priorities.
- Include verification results.
```

---

## Phase 10 Prompt — Low-risk Decomposition Specs

```text
You are working in the pi-go repository.

Goal: create follow-up implementation plans for decomposing the largest orchestration files without changing production code now.

Read:
- specs/research/006-project-overview-critical-improvements/OVERVIEW.md
- specs/research/006-project-overview-critical-improvements/PLAN.md, Phase 10
- specs/AGENTS.md for spec conventions

Tasks:
1. Measure largest production Go files:
   find internal cmd -name '*.go' -not -name '*_test.go' -print0 | xargs -0 wc -l | sort -nr | head -30
2. Choose the top decomposition candidates, expected to include some of:
   - internal/cli/cli.go
   - internal/tui/tui.go
   - internal/tui/run.go
   - internal/session/store.go
3. Create dedicated follow-up specs under specs/issues, for example:
   - specs/issues/005-cli-decomposition/PLAN.md
   - specs/issues/006-tui-decomposition/PLAN.md
   - specs/issues/007-session-store-decomposition/PLAN.md
4. Each spec must include:
   - current file responsibilities;
   - proposed extraction seams;
   - behavior-preserving migration steps;
   - test-before-move requirements;
   - verification commands;
   - rollback strategy.
5. Do not modify production code in this phase.

Constraints:
- Plans/specs only.
- No refactor implementation.
- Keep each plan vertical and reviewable.

Verification:
- find specs/issues -maxdepth 2 -name 'PLAN.md' | sort
- git diff -- specs/issues

Final response:
- List created spec files.
- Summarize each planned decomposition.
- Confirm no production code changed.
```

---

## Final Verification Prompt

```text
You are working in the pi-go repository.

Goal: run the final validation gate for the critical repository improvements effort.

Read:
- specs/research/006-project-overview-critical-improvements/PLAN.md, Final Verification Gate and Done Criteria

Tasks:
1. Run:
   - go test ./...
   - go test -race -count=1 $(go list ./... | grep -v '/internal/acp/server$')
   - make build
   - make lint
2. If integration/e2e environments are available, also run:
   - go test -tags integration ./...
   - go test -tags e2e ./...
3. Do not make fixes unless explicitly asked. If failures appear, document them with enough detail for follow-up.
4. Update PLAN.md with a final validation summary if requested by the user.

Constraints:
- Validation-focused.
- Avoid unrelated edits.
- Do not hide failures.

Final response:
- Provide a table of commands, result, duration if available, and notes.
- List any failures with package/file references.
- State whether the plan meets its done criteria.
```
