// Package agent sets up the ADK Go agent loop with tools, system prompt,
// and runner for the pi-go coding agent.
package agent

import (
	"context"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/util/instructionutil"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// re-export callback types for use by CLI without importing llmagent directly.
type (
	BeforeToolCallback  = llmagent.BeforeToolCallback
	AfterToolCallback   = llmagent.AfterToolCallback
	BeforeModelCallback = llmagent.BeforeModelCallback
	AfterModelCallback  = llmagent.AfterModelCallback
)

const (
	// AppName is the ADK application name used for session management.
	AppName = "pi-go"

	// DefaultUserID is the default user ID for local single-user sessions.
	DefaultUserID = "local"
)

// SystemInstruction is the default system prompt for the coding agent.
const SystemInstruction = `You are pi-go, a coding agent that helps users with software engineering tasks.

You have access to tools for reading, writing, and editing files, running shell commands,
and searching codebases. Use these tools to assist the user effectively.

# Codebase exploration

When you need to understand code before acting, follow this strategy — work top-down, stop as soon as you have enough context:

1. Orient: run tree (depth 2-3) or ls to see the project layout. Check for README, go.mod, package.json, or similar to understand the stack.
2. Narrow: use grep to find the exact symbols, types, or strings relevant to the task. Search by function name, type name, error message, or constant.
3. Read targeted sections: use offset/limit to read only the relevant part of a file — never cat entire large files.
4. Trace connections: if you need to understand a call chain, grep for the function name to find all callers/callees. Follow import chains to build the full picture.

Rules for efficient exploration:
- grep before read — always search for the symbol first, then read the specific file and line range.
- Try alternative names if the first search misses: different casing, abbreviations, interface vs implementation.
- For large codebases, use the subagent tool with {agent: "explore", task: "..."} to parallelize searches.
- Include file:line references in your explanations so the user can navigate directly.
- When multiple files are involved, briefly explain how they connect before diving into details.

# Environment management

Prefer modern, fast package managers:

- **Python**: Use uv instead of pip. Run scripts with "uv run", manage dependencies with "uv add". Example: "uv run pytest", "uv run python script.py", "uv add requests".
- **Node.js**: Use bun instead of npm/yarn/pnpm. Faster installs, built-in TypeScript, works as package manager and runtime. Example: "bun install", "bun run dev", "bun test".

# Coding tasks

Before starting a task, first check for repository-specific instructions and reusable skills:
- find AGENTS.md if it exists in current folder .pi-go .cursor .claude
- Read AGENTS.md if it exists and follow it as project-specific rules.
- find SKILL.md files .pi-go .cursor .claude and load any skills relevant to the user's request before planning or implementing.

Follow this workflow for every coding task — move fast, verify, deliver:

1. Understand: read the specific code you will change. grep for the function/type/symbol, then read the relevant section. Do not read unrelated files.
2. Plan briefly: state what you will change and why in 1-3 sentences. For non-trivial changes, list the files and the change for each.
3. Implement: make the smallest correct change. Edit existing files — do not create new files unless the task requires it.
4. Verify: build/compile to catch errors. Run existing tests if available. Fix any issues before declaring done.
5. Report: show what changed (file:line) and confirm it builds/passes.

Coding principles:
- One thing at a time — finish one change fully before starting the next.
- Match existing patterns — use the same style, naming, error handling, and structure as the surrounding code.
- Edit surgically — change only what is needed. Do not refactor, reformat, add comments, or "improve" code you were not asked to touch.
- Verify after every edit — run the build or relevant test immediately. Do not batch multiple edits before checking.
- When a build/test fails, read the error, fix the root cause, and rebuild. Do not retry the same thing.
- Prefer edit over write — use the edit tool for targeted changes, write tool only for new files.
- Keep it simple — three similar lines are better than a premature abstraction. No feature flags, no backwards-compat shims, no speculative helpers.
- Avoid introducing vulnerabilities — validate at system boundaries, use parameterized queries, escape user input.
- Never delete code you did not create. If a file or function is untracked or was created by another session, do not remove it to "clean up" — it may be part of an in-progress feature. If linters flag it as unused, wire it in or leave it; do not delete it. When in doubt, ask the user.
- Never use rm or mv to remove source files to debug a build issue. Comment out the problematic import or use git stash instead. A slow build is not a reason to delete a file.
- Before deleting any file, verify it is not referenced elsewhere (grep for imports, function names, types). Untracked files are not necessarily disposable — they may be new work not yet committed.

Anti-hallucination rules (critical — violating these makes your output worthless):
- Never claim a build passes without running the actual build command. Paste the output. If you did not run it, say "build not verified".
- Never claim tests pass without running them. Paste the output. If you did not run them, say "tests not verified".
- Never claim a file was created or edited without verifying with ` + "`" + `ls` + "`" + `, ` + "`" + `git status` + "`" + `, or ` + "`" + `git diff --name-only` + "`" + `. If the diff is empty, you delivered nothing — say so honestly.
- Do not fabricate tool output. If a command failed, report the failure. Do not pretend it succeeded.
- When reporting completion, include the actual ` + "`" + `git diff --name-only` + "`" + ` output as proof of work.

# Presenting choices

When you ask the user to choose between several options (A, B, C, D), always recommend one:
- Label your preferred option clearly, e.g. "(recommended)", and put it first in the list.
- Give a one-line reason why it is the best default for this situation (simplest, safest, matches existing patterns, least risk).
- Keep options mutually exclusive and concise — describe the trade-off of each, not just its name.
- Never present a flat list with no guidance. The user can always pick another option, but they should never have to guess which one you would choose.

# Clarifying questions

Default: act. Only ask when the task is genuinely ambiguous in a way that would change the work. A clear request deserves a clear answer — do not stall it with questions. Pinging the user on every detail breaks flow and signals you can't think for yourself.

When the user's request is ambiguous, incomplete, or has multiple reasonable interpretations, ask before acting. A short clarification now is far cheaper than rewriting code (or worse, building the wrong thing) later. The user's recent style in this project is short, terse instructions with implicit context — read it generously, but ask when the ambiguity is real.

Ask when:
- The request has multiple valid interpretations and the wrong one means rewriting code. Examples: "remove X and replace with Y" (replace with what?), "use the same colors" (which colors?), "make it look better" (in what way?), "move X next to Y" (above, below, or inline?).
- A key piece of information is missing that you cannot infer from the codebase (target audience, file/function name, specific value, exact desired behavior).
- The task touches something destructive or hard to reverse (deleting files, force-push, dropping data) and the scope is unclear.
- Two stated goals conflict (e.g. "keep it short" + "add extensive logging") and the user hasn't said which wins.
- The user references something by shorthand ("the sidebar", "the old method", "like before") and you don't know which thing they mean.

How to ask:
- Ask BEFORE exploring deeply or running tools for an ambiguous task. State what you understood, then ask the one or two questions that matter. Don't preface with "Let me first check..." then disappear into the codebase — that wastes a turn.
- Prefer multiple-choice (A/B/C, with a recommended option) over open-ended questions. See the "Presenting choices" section above.
- Be specific. "Should the OTEL indicator go immediately left of the model name, or grouped with other status icons on the right?" is better than "where do you want it?".
- Bundle related questions into one message — don't drip-feed them one at a time.
- If you can make a reasonable default assumption, state it explicitly ("I'll go with X unless you'd prefer Y") so the user can correct you in one word.

Do NOT ask when:
- The request is clear. If the task has a single obvious reading, just do it. "Add a footer to the login page" needs no question.
- The only ambiguity is a small implementation detail (variable name, exact log format, ordering inside a struct) — pick a sensible default and mention it in the plan.
- The answer is obvious from the codebase (existing pattern, prior commit, AGENTS.md rule, naming convention).
- You're mid-execution on a clear plan and hit a small surprise — note it in the report, don't interrupt.
- The question is rhetorical or a "sanity check" before doing exactly what was asked. Trust the request.

# Git safety

Treat the user's git history as precious. Default to non-destructive operations and make every large change recoverable.

Before large or risky changes:
- Check state first — run "git status" and "git rev-parse --abbrev-ref HEAD" before any git operation so you know the branch and what is staged/dirty.
- Never work directly on main/master — if you are on the default branch, create a feature branch before editing (e.g. "git switch -c feat/<short-topic>").
- Create a backup branch before any large, multi-file, or history-altering change: "git branch backup/<topic>-<context>" (a branch is a free, instant snapshot you can return to). Tell the user the backup branch name so they can recover with "git switch" or "git reset --hard <backup>".
- For experimental edits to tracked files, "git stash" or commit a checkpoint first so the working tree can be restored.

Make focused, isolated changes:
- Work in small, self-contained batches — one logical change per commit. Do not bundle unrelated edits.
- Stage selectively (specific paths, or "git add -p") rather than "git add -A" so each commit contains only the intended change.
- Commit checkpoints frequently during large work so progress is never lost and individual steps can be reverted in isolation.
- Write clear, scoped commit messages describing the single change.

Integrate carefully:
- Prefer rebasing your focused batches onto the latest base ("git rebase <base>") to keep a linear, reviewable history — but only after the work is committed and a backup branch exists.
- If a rebase or merge goes wrong, "git rebase --abort" / "git merge --abort" returns to safety; the backup branch is the fallback.

Destructive operations — confirm and protect first:
- Operations that can lose work or rewrite shared history — "git reset --hard", "git checkout -- <file>", "git clean -fd", "git push --force", "git rebase", branch/tag deletion — require an existing backup branch (or stash) and, for anything touching pushed/shared history, explicit user confirmation.
- Prefer "git push --force-with-lease" over "git push --force" so you never clobber commits you have not seen.
- Never run "git reset --hard", "git clean", or force-push without first ensuring the work is recoverable and stating what will be discarded.

# Context management

Be aware of context window pressure. Follow these rules to keep output quality high:
- When a tool returns a very large result (>200 lines), summarize the key findings and note where the full output can be found. Do not paste large outputs verbatim into your response.
- Prefer targeted reads (offset/limit) over full-file reads. Only read the lines you actually need.
- If you notice your responses becoming repetitive or losing track of earlier details, proactively suggest compaction or summarize your current understanding before continuing.
- Keep your working context focused: when switching between unrelated topics, briefly restate the current goal.

# Multi-step tasks

For non-trivial tasks involving multiple files or phases, plan vertically, not horizontally:
- Vertical (preferred): implement one complete slice end-to-end (e.g., type + handler + test), verify it works, then move to the next slice.
- Horizontal (avoid): implementing all types first, then all handlers, then all tests — this delays verification and compounds errors.
- After each vertical slice, run the build and tests to confirm correctness before proceeding.

# Parallel execution

You can call multiple tools in a single response when they are independent. For example:
- Read multiple files simultaneously
- Run grep searches in parallel
- Spawn multiple subagents at once
The TUI tracks all active tools and shows them in the status bar. Only parallelize when operations are truly independent — do not parallelize edits to the same file or dependent operations.

# Diagrams

When a response benefits from a visual diagram, use these three styles as appropriate:

- **Mermaid ` + "`" + `mindmap` + "`" + `** — for showing hierarchy, grouping, or "what belongs to what" (e.g. a toolbelt, a category tree). No directional flow; branches radiate from a center node.
- **Mermaid ` + "`" + `flowchart` + "`" + `** (` + "`" + `TD` + "`" + ` or ` + "`" + `LR` + "`" + `) — for showing a process, pipeline, or decision flow with arrows. Use ` + "`" + `TD` + "`" + ` (top-down) for sequential steps, ` + "`" + `LR` + "`" + ` (left-right) for pipelines with feedback loops.
- **ASCII tree** — for showing file/directory structure, nested config, or anything naturally tree-shaped. Use box-drawing characters (` + "`" + `├──` + "`" + `, ` + "`" + `└──` + "`" + `, ` + "`" + `│` + "`" + `) with optional emoji annotations.

Prefer the simplest style that communicates the idea. Do not mix styles within a single diagram. Use markdown tables to compare or summarize when a diagram is not needed.

# Internal tools

- restart — Restarts the pi process (re-exec with same binary and args). Call this tool after successfully rebuilding the pi binary to apply changes. The process will restart with the updated binary.

# JSON String Escaping

When sending tool parameters that contain file paths or strings with special characters:
- Always escape backslashes in JSON: use ` + "`" + `\\` + "`" + ` not ` + "`" + `\` + "`" + `
- For Windows paths like C:\Users\test, send as "C:\\Users\\test" in JSON
- Verify paths are properly escaped before calling tools that require file_path

Example INCORRECT (will cause tool errors):
{"file_path": "C:\Users\test\file.go"}

Example CORRECT:
{"file_path": "C:\\Users\\test\\file.go"}

# Subagents

You can spawn subagents using the subagent tool to parallelize work. The sidebar shows running agent names, status, and total count.

## When to use agents

Use agents for any task that benefits from parallel or independent work:

- **Research & exploration**: spawn explore agents to search multiple code areas simultaneously. For example, to understand a feature, spawn parallel explores for "find all callers of FooService" and "find the config and initialization for FooService".
- **Repository analysis**: for broad questions ("how does auth work?", "what changed recently?"), spawn 2-3 explore agents targeting different aspects in parallel rather than searching sequentially yourself.
- **Implementation**: use task/designer agents for isolated coding in worktrees, or worker/quick-task agents for edits in the main tree.
- **Review**: use code-reviewer for diff review, spec-reviewer for design document review.
- **Planning**: use the plan agent to produce vertically-sliced implementation plans from codebase research.

## Worktree agents

- "task" and "designer" run in isolated git worktrees. A normal subagent call returns the agent's output; its worktree edits are not automatically applied to the current tree unless a separate workflow keeps and merges that worktree.
- For user-requested changes that must land in this session, either edit the current tree yourself, use "worker"/"quick-task" for main-tree edits, or ask a worktree agent to return an exact patch/file list that you can review and apply.
- When delegating worktree edits, give the agent clear ownership of specific files or directories, expected verification commands, and the final handoff format. Do not send multiple worktree agents to edit the same files.

## Rules

- Maximum 8 concurrent subagents. Do not spawn more than needed.
- Each subagent runs in its own process with its own context and tools.
- Give each subagent a specific, focused task description — not the full ticket. The clearer the input, the better the output.
- **Prefer parallel over sequential**: when researching a topic, spawn 2-4 explore agents with different search angles rather than one agent doing everything.
- **Prefer agents over manual multi-step search**: if finding the answer requires reading 3+ files across different packages, delegate to an explore agent instead of doing it yourself.
- Chain mode passes results between agents: use it when step 2 depends on step 1's output (e.g., explore → plan → task).
`

// Config holds configuration for creating a new Agent.
type Config struct {
	// Model is the LLM provider to use (implements model.LLM).
	Model model.LLM

	// Tools are the tools available to the agent.
	Tools []tool.Tool

	// Toolsets are additional tool providers (e.g. MCP toolsets).
	Toolsets []tool.Toolset

	// Instruction overrides the default system instruction.
	// If empty, SystemInstruction is used.
	Instruction string

	// SessionService overrides the default in-memory session service.
	// If nil, an in-memory service is created.
	SessionService session.Service

	// BeforeToolCallbacks run before each tool execution.
	BeforeToolCallbacks []BeforeToolCallback

	// AfterToolCallbacks run after each tool execution.
	AfterToolCallbacks []AfterToolCallback

	// BeforeModelCallbacks run before each LLM invocation.
	BeforeModelCallbacks []BeforeModelCallback

	// AfterModelCallbacks run after each LLM invocation.
	AfterModelCallbacks []AfterModelCallback

	// WorkingDir, when non-empty, is the directory reported to the model as
	// the current working directory. Empty means the process's, which is
	// right for the CLI but wrong for an embedder driving a directory it was
	// not started in.
	WorkingDir string

	// Logger, if non-nil, receives non-fatal diagnostics such as unresolved
	// instruction placeholders. These are written to the session log rather
	// than stderr: stderr would paint over the TUI alt-screen. Its methods
	// are nil-safe, so a nil Logger silently discards.
	Logger *logger.Logger
}

// workingDir returns the directory to report to the model: the configured one
// when set, otherwise the process's. An unreadable process cwd yields "", and
// the caller then omits the line rather than reporting a directory it guessed.
func (c Config) workingDir() string {
	if c.WorkingDir != "" {
		return c.WorkingDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// Agent wraps an ADK Runner and session management for the coding agent.
type Agent struct {
	runner         *runner.Runner
	sessionService session.Service
	config         Config // stored for RebuildWithInstruction

	// preTurn runs before each turn is dispatched. Auto-compaction uses it to
	// reclaim context while the conversation is between turns, which is the
	// only safe moment: rewriting history mid-turn would orphan a tool call
	// from its result.
	preTurn PreTurnHook
}

// PreTurnHook runs before a turn is dispatched to the runner. Returning an
// error aborts the turn.
type PreTurnHook func(ctx context.Context, sessionID string) error

// SetPreTurnHook installs a hook that runs before every turn. Passing nil
// removes it.
func (a *Agent) SetPreTurnHook(h PreTurnHook) {
	a.preTurn = h
}

// runPreTurn invokes the hook if one is installed.
func (a *Agent) runPreTurn(ctx context.Context, sessionID string) error {
	if a.preTurn == nil {
		return nil
	}
	return a.preTurn(ctx, sessionID)
}

const maxInstructionFileSize = 128 * 1024

// placeholderRegex matches `{...}` runs in the instruction template. Kept
// in sync with the regex used by ADK's internal instruction processor
// (google.golang.org/adk/v2/internal/llminternal/instruction_processor.go)
// so that we slice the template on the same boundaries.
var placeholderRegex = regexp.MustCompile(`\{+[^{}]*\}+`)

// safeInstructionProvider returns an ADK InstructionProvider that
// substitutes {state_var} placeholders in template via the session state,
// but fails open per placeholder: if a particular placeholder cannot be
// resolved (missing key, artifact not found, etc.) the original literal
// substring is left in place rather than aborting the whole turn.
//
// Why: the instruction may contain text loaded from project context files
// (AGENTS.md / CLAUDE.md / AGENT.md / .pi-go/AGENTS.md) and skill
// descriptions. Those files are user-authored prose and can legitimately
// contain {identifier}-shaped substrings (e.g. documentation about
// `{AGT_X}` placeholder tokens in a keymap system) that are NOT session
// state references. With the default Instruction: <string> wiring, ADK
// treats every such substring as a state key and aborts the turn with
// "state key does not exist". By owning the provider we keep substitution
// for legitimate keys while leaving stray prose alone, and we do it on a
// per-placeholder basis so a single missing key never cancels every other
// legitimate substitution in the same template.
func safeInstructionProvider(template string, log *logger.Logger) llmagent.InstructionProvider {
	return func(ctx adkagent.ReadonlyContext) (string, error) {
		return injectWithFailOpen(ctx, template, log), nil
	}
}

// injectWithFailOpen walks template, resolving each {placeholder} via
// instructionutil.InjectSessionState and keeping the original literal
// when resolution fails. Literal segments (between matches) are copied
// unchanged.
func injectWithFailOpen(ctx adkagent.ReadonlyContext, template string, log *logger.Logger) string {
	var b strings.Builder
	last := 0
	for _, loc := range placeholderRegex.FindAllStringIndex(template, -1) {
		b.WriteString(template[last:loc[0]])
		match := template[loc[0]:loc[1]]
		resolved, err := instructionutil.InjectSessionState(ctx, match)
		if err != nil {
			// Record to the session log (not stderr — that corrupts the TUI)
			// so a developer who intended {app:foo} to resolve but typoed the
			// name still has a trace. User-authored prose containing literal
			// {tokens} produces one entry per unmatched placeholder, which is
			// acceptable signal-to-noise in an on-disk log.
			log.Info(fmt.Sprintf("instruction placeholder %q not resolved; left as literal: %v", match, err))
			b.WriteString(match)
		} else {
			b.WriteString(resolved)
		}
		last = loc[1]
	}
	b.WriteString(template[last:])
	return b.String()
}

func buildRunner(cfg Config, instruction string, sessionSvc session.Service) (*runner.Runner, error) {
	llmAgent, err := llmagent.New(llmagent.Config{
		Name:                 "pi",
		Description:          "A coding agent that helps with software engineering tasks.",
		Model:                cfg.Model,
		InstructionProvider:  safeInstructionProvider(instruction, cfg.Logger),
		Tools:                cfg.Tools,
		Toolsets:             dedupeToolsets(cfg.Tools, cfg.Toolsets),
		BeforeToolCallbacks:  cfg.BeforeToolCallbacks,
		AfterToolCallbacks:   cfg.AfterToolCallbacks,
		BeforeModelCallbacks: cfg.BeforeModelCallbacks,
		AfterModelCallbacks:  cfg.AfterModelCallbacks,
	})
	if err != nil {
		return nil, fmt.Errorf("creating LLM agent: %w", err)
	}

	r, err := runner.New(runner.Config{
		AppName:        AppName,
		Agent:          llmAgent,
		SessionService: sessionSvc,
	})
	if err != nil {
		return nil, fmt.Errorf("creating runner: %w", err)
	}
	return r, nil
}

// New creates a new Agent with the given configuration.
func New(cfg Config) (*Agent, error) {
	instruction := cfg.Instruction
	if instruction == "" {
		instruction = SystemInstruction
	}

	// Add working directory context to the instruction.
	if cwd := cfg.workingDir(); cwd != "" {
		instruction += fmt.Sprintf("\nCurrent working directory: %s\n", cwd)
	}

	// Set up session service.
	sessionSvc := cfg.SessionService
	if sessionSvc == nil {
		sessionSvc = session.InMemoryService()
	}

	// Create the runner.
	r, err := buildRunner(cfg, instruction, sessionSvc)
	if err != nil {
		return nil, err
	}

	return &Agent{
		runner:         r,
		sessionService: sessionSvc,
		config:         cfg,
	}, nil
}

// RebuildWithInstruction recreates the agent's internal runner with a new
// system instruction while preserving all other configuration (tools, callbacks, etc.).
// The session service is reused so existing sessions remain accessible.
func (a *Agent) RebuildWithInstruction(instruction string) error {
	cfg := a.config
	cfg.Instruction = instruction
	// Force the provided instruction (skip default fallback).
	if instruction == "" {
		return fmt.Errorf("instruction must not be empty")
	}

	r, err := buildRunner(cfg, instruction, a.sessionService)
	if err != nil {
		return fmt.Errorf("rebuilding runner: %w", err)
	}

	a.runner = r
	a.config = cfg
	return nil
}

// RebuildWithModel recreates the agent's internal runner with a new LLM while
// preserving all other configuration (tools, callbacks, instruction, session).
// The session service is reused so existing sessions remain accessible.
func (a *Agent) RebuildWithModel(llm model.LLM) error {
	cfg := a.config
	cfg.Model = llm

	// Reconstruct the instruction the same way New() does.
	instruction := cfg.Instruction
	if instruction == "" {
		instruction = SystemInstruction
	}
	if cwd := cfg.workingDir(); cwd != "" {
		instruction += fmt.Sprintf("\nCurrent working directory: %s\n", cwd)
	}

	r, err := buildRunner(cfg, instruction, a.sessionService)
	if err != nil {
		return fmt.Errorf("rebuilding runner: %w", err)
	}

	a.runner = r
	a.config = cfg
	return nil
}

// modelNamer is satisfied by session services that can record the model name
// for an existing session (notably *session.FileService). Sessions backed by
// services without this capability still get the default "unknown" placeholder
// written by the file backend.
type modelNamer interface {
	SetSessionModel(sessionID, modelName string) error
}

// titleNamer is satisfied by session services that can record a human-readable
// title for an existing session. *session.FileService implements it; in-memory
// services without the capability silently no-op so callers don't have to
// branch on the service type.
type titleNamer interface {
	SetSessionTitle(sessionID, title string) error
}

// agentContextRecorder is implemented by session services that can persist a
// session's place in an agent tree. Declared here for the same reason as
// titleNamer: the agent takes the capability, not the concrete service.
type agentContextRecorder interface {
	UpdateAgentContext(sessionID string, ctx *pisession.AgentContext) error
}

// CreateSession creates a new session and returns its ID together with the
// default title that was applied (git repo name, or CWD basename) — or "" if
// no title was set. The title is metadata only; the TUI seeds its terminal
// window/tab title from it so users see a sensible label before the first user
// prompt arrives.
func (a *Agent) CreateSession(ctx context.Context) (sessionID, defaultTitle string, err error) {
	resp, err := a.sessionService.Create(ctx, &session.CreateRequest{
		AppName: AppName,
		UserID:  DefaultUserID,
	})
	if err != nil {
		return "", "", fmt.Errorf("creating session: %w", err)
	}
	sid := resp.Session.ID()
	// Record this process's place in an agent tree, if it has one. A spawned
	// worker knows its coordinator, spec, slice and cycle only through the
	// environment; writing them here is what makes a run tree a field lookup
	// instead of an inference from workDir and title prefixes.
	if ac, ok := a.sessionService.(agentContextRecorder); ok {
		if actx := pisession.AgentContextFromEnv(); actx != nil {
			_ = ac.UpdateAgentContext(sid, actx) // best-effort metadata
		}
	}
	if mn, ok := a.sessionService.(modelNamer); ok {
		modelName := ""
		if a.config.Model != nil {
			modelName = a.config.Model.Name()
		}
		_ = mn.SetSessionModel(sid, modelName) // best-effort; meta defaults to "unknown"
	}
	// Default title: the git repo name (or CWD basename) so brand-new sessions
	// in /sessions listings have a sensible label before the first user prompt
	// overwrites it via the TUI's applySessionTitle / runPrint's derivePrintTitle.
	if tn, ok := a.sessionService.(titleNamer); ok {
		if title := defaultSessionTitle(a.config.workingDir()); title != "" {
			_ = tn.SetSessionTitle(sid, title) // best-effort metadata
			return sid, title, nil
		}
	}
	return sid, "", nil
}

// gitToplevelTimeout caps the `git rev-parse --show-toplevel` subprocess so a
// stuck git invocation never delays session creation.
const gitToplevelTimeout = 2 * time.Second

// gitToplevelFn returns the absolute path of the git toplevel for dir, or "" if
// dir is not inside a git repository (or git is unavailable). It's a var so
// tests can stub the subprocess call without touching the real binary.
var gitToplevelFn = func(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitToplevelTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitCurrentBranch returns the current git branch name for dir, or "" if dir
// is not inside a git repository, git is unavailable, or the repo has no
// commits yet (in which case `git rev-parse --abbrev-ref HEAD` prints "HEAD"
// — meaningless as a label, so we treat it as no branch). It's a var so
// tests can stub the subprocess call without touching the real binary.
var gitCurrentBranch = func(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitToplevelTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return ""
	}
	return branch
}

// defaultSessionTitle returns a short, human-readable label for a brand-new
// session, composed from the cwd's git context when available:
//   - inside a git repo:        "<repo> <branch> <folder>"
//   - detached / no branch:    "<repo> <folder>"
//   - not a git repo:          "<folder>"      (no separator; nothing to anchor)
//   - empty cwd:               ""              (caller treats as "no title")
//
// Components are joined with a single space so the title stays clean inside
// the terminal tab title (used as the OSC 0 payload) and doesn't collide
// with the prompt-derived title that /title and the first-line derive path
// will later replace. The branch is omitted (not rendered as "(no branch)")
// when it can't be determined, to keep the title clean.
func defaultSessionTitle(cwd string) string {
	if cwd == "" {
		return ""
	}
	folder := filepath.Base(cwd)
	root := gitToplevelFn(cwd)
	if root == "" {
		return folder
	}
	repo := filepath.Base(root)
	branch := gitCurrentBranch(cwd)
	if branch == "" {
		return repo + " " + folder
	}
	return repo + " " + branch + " " + folder
}

// SetSessionTitle records a title for the given session via the session
// service when it supports it. Services without the title capability (e.g.
// the ADK in-memory service) silently no-op — the title is metadata that is
// safe to lose for ephemeral sessions.
func (a *Agent) SetSessionTitle(sessionID, title string) error {
	tn, ok := a.sessionService.(titleNamer)
	if !ok {
		return nil
	}
	return tn.SetSessionTitle(sessionID, title)
}

// Run sends a user message and returns an iterator over agent events.
// The caller should iterate over the returned sequence to process events.
func (a *Agent) Run(ctx context.Context, sessionID string, userMessage string) iter.Seq2[*session.Event, error] {
	if err := a.runPreTurn(ctx, sessionID); err != nil {
		return failedRun(err)
	}
	msg := genai.NewContentFromText(userMessage, genai.RoleUser)
	return a.runner.Run(ctx, DefaultUserID, sessionID, msg, adkagent.RunConfig{})
}

// RunStreaming sends a user message with SSE streaming enabled.
func (a *Agent) RunStreaming(ctx context.Context, sessionID string, userMessage string) iter.Seq2[*session.Event, error] {
	if err := a.runPreTurn(ctx, sessionID); err != nil {
		return failedRun(err)
	}
	msg := genai.NewContentFromText(userMessage, genai.RoleUser)
	return a.runner.Run(ctx, DefaultUserID, sessionID, msg, adkagent.RunConfig{
		StreamingMode: adkagent.StreamingModeSSE,
	})
}

// failedRun returns a single-error sequence, so a pre-turn failure surfaces
// through the same channel as any other turn error.
func failedRun(err error) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		yield(nil, err)
	}
}

// contextFileNames lists the context-file names checked in each directory
// during discovery, in priority order. The first match per directory wins.
var contextFileNames = []string{
	"AGENT.md",
	"AGENTS.md",
	"CLAUDE.md",
	filepath.Join(".pi-go", "AGENTS.md"),
}

// LoadInstruction appends discovered project context files and a summary of
// discovered skills to the base instruction. Context files (AGENT.md,
// AGENTS.md, CLAUDE.md, or .pi-go/AGENTS.md; first match per directory) are
// discovered by walking from the working directory up to the filesystem
// root; a global ~/.pi-go/AGENTS.md is included first when present.
func LoadInstruction(baseInstruction string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return prependEnvironmentContext(baseInstruction, os.Getenv("HOME"), os.Getenv("USER"), os.Getenv("PWD"), "")
	}
	home, _ := os.UserHomeDir()
	return loadInstructionFrom(baseInstruction, cwd, home)
}

// InstructionParts is the system prompt broken into the sections it is
// assembled from. Callers that only need the finished prompt use
// LoadInstruction; the parts exist so the TUI can attribute context usage to
// each section instead of reporting one opaque total.
type InstructionParts struct {
	// Base is the built-in system prompt.
	Base string
	// Rules is the concatenated AGENTS.md / CLAUDE.md project context.
	Rules string
	// Skills is the "# Available Skills" menu.
	Skills string
}

// String reassembles the parts into the prompt the model receives. Keeping
// composition here means the breakdown can never drift from the real prompt:
// LoadInstruction returns exactly this.
func (p InstructionParts) String() string {
	return p.Base + p.Rules + p.Skills
}

// loadInstructionFrom is the testable core of LoadInstruction, resolving
// context files and skills relative to explicit cwd and home directories.
func loadInstructionFrom(baseInstruction, cwd, home string) string {
	return loadInstructionPartsFrom(baseInstruction, cwd, home).String()
}

// LoadInstructionPartsFor resolves the system prompt for an explicit working
// directory instead of the process's, and returns it broken into its sections.
// Callers that drive the agent over a directory they were not started in — the
// public piagent package, for one — need this; LoadInstructionParts is the
// os.Getwd() convenience on top of it.
func LoadInstructionPartsFor(baseInstruction, cwd string) InstructionParts {
	home, _ := os.UserHomeDir()
	return loadInstructionPartsFrom(baseInstruction, cwd, home)
}

// LoadInstructionParts resolves the system prompt and returns it broken into
// its sections.
func LoadInstructionParts(baseInstruction string) InstructionParts {
	cwd, err := os.Getwd()
	if err != nil {
		return InstructionParts{Base: prependEnvironmentContext(baseInstruction, os.Getenv("HOME"), os.Getenv("USER"), os.Getenv("PWD"), "")}
	}
	home, _ := os.UserHomeDir()
	return loadInstructionPartsFrom(baseInstruction, cwd, home)
}

// prependEnvironmentContext adds the process context before the built-in prompt
// so the agent has authoritative values for common shell variables.
func prependEnvironmentContext(baseInstruction, home, user, pwd, cwd string) string {
	if pwd == "" {
		pwd = cwd
	}
	return fmt.Sprintf("# Runtime Environment\n\nThe following values are from the current process environment. Treat them as authoritative; do not guess or substitute values for them.\n\n- HOME=%q\n- USER=%q\n- PWD=%q\n\n%s", home, user, pwd, baseInstruction)
}

func loadInstructionPartsFrom(baseInstruction, cwd, home string) InstructionParts {
	parts := InstructionParts{Base: prependEnvironmentContext(baseInstruction, os.Getenv("HOME"), os.Getenv("USER"), os.Getenv("PWD"), cwd)}

	if contents := discoverContextFiles(cwd, home); len(contents) > 0 {
		parts.Rules = "\n\n# Project Rules\n\n" + strings.Join(contents, "\n\n")
	}

	// Get skills.
	skillDirs := extension.DefaultSkillDirsIn(cwd)
	skills, err := extension.LoadSkills(skillDirs...)
	if err == nil && len(skills) > 0 {
		parts.Skills = appendSkillsMenu(skills)
	}

	return parts
}

// appendSkillsMenu formats the "# Available Skills" block for a pre-loaded
// skill slice. Exposed so callers that already have skills in hand (e.g. the
// TUI) don't trigger a second LoadSkills.
func appendSkillsMenu(skills []extension.Skill) string {
	var b strings.Builder
	b.WriteString("\n\n# Available Skills\n\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- /%s: %s\n", s.Name, s.Description)
	}
	return b.String()
}

// AppendActiveSkill returns prompt with an "# Active Skill" section appended
// for the given skill body. Use this on top of a prompt produced by
// LoadInstruction to activate a skill for one turn (Level-2 injection).
//
// The format is:
//
//	\n\n# Active Skill: <name>\n\n<body>\n
func AppendActiveSkill(prompt string, skill extension.Skill, body string) string {
	return prompt + fmt.Sprintf("\n\n# Active Skill: %s\n\n%s\n", skill.Name, body)
}

type contextFile struct {
	path    string
	content string
}

// discoverContextFiles returns context-file contents ordered from most
// general to most specific: the global ~/.pi-go/AGENTS.md first, then one
// file per directory from the filesystem root down to cwd.
func discoverContextFiles(cwd, home string) []string {
	// Walk from cwd up to the filesystem root, nearest directory first.
	var found []contextFile
	dir := filepath.Clean(cwd)
	for {
		if f, ok := readContextFileIn(dir); ok {
			found = append(found, f)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	slices.Reverse(found) // most general (root) first

	// Global file comes first, unless the walk already picked it up.
	if home != "" {
		globalPath := filepath.Join(home, ".pi-go", "AGENTS.md")
		alreadyFound := slices.ContainsFunc(found, func(f contextFile) bool {
			return f.path == globalPath
		})
		if !alreadyFound {
			if content, ok := readInstructionFile(globalPath); ok {
				found = append([]contextFile{{path: globalPath, content: content}}, found...)
			}
		}
	}

	contents := make([]string, 0, len(found))
	for _, f := range found {
		contents = append(contents, f.content)
	}
	return contents
}

// readContextFileIn returns the first context file found in dir.
func readContextFileIn(dir string) (contextFile, bool) {
	for _, name := range contextFileNames {
		path := filepath.Join(dir, name)
		if content, ok := readInstructionFile(path); ok {
			return contextFile{path: path, content: content}, true
		}
	}
	return contextFile{}, false
}

// readInstructionFile reads path, rejecting missing or oversized files.
func readInstructionFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxInstructionFileSize {
		return "", false
	}
	return string(data), true
}
