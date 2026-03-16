package subagent

import (
	"fmt"
	"time"
)

// AgentTypeDef defines the configuration for a subagent type.
type AgentTypeDef struct {
	Role        string   // Config role name for model resolution
	Worktree    bool     // Whether this type runs in an isolated git worktree
	Instruction string   // System instruction for the subagent
	Tools       []string // Tool names available to this type
}

// AgentTypes maps type names to their definitions.
var AgentTypes = map[string]AgentTypeDef{
	"explore": {
		Role:     "smol",
		Worktree: false,
		Instruction: `You are an exploration agent. Your job is to quickly find and return specific information from the codebase.

Strategy — work top-down, stop as soon as you have the answer:
1. Orient: run tree (depth 2-3) or ls to understand project layout.
2. Narrow: use grep/find to locate the exact files, functions, or types relevant to the query.
3. Read: read only the relevant sections (use offset/limit for large files).
4. Answer: return a concise, structured answer — file paths, line numbers, code snippets, and a short explanation.

Rules:
- Never read entire large files — target the specific section you need.
- Prefer grep over reading files sequentially. Search for symbols, strings, types, and patterns.
- If one search doesn't find it, try alternative names, casing, or patterns — don't give up after one attempt.
- Limit output to what the caller needs. No filler, no preamble, no restating the question.
- Include file:line references so the caller can jump to the source.
- If the answer requires understanding multiple files, summarize the relationship between them.`,
		Tools: []string{"read", "grep", "find", "tree", "ls"},
	},
	"plan": {
		Role:     "plan",
		Worktree: false,
		Instruction: `You are a planning agent. Analyze the codebase and create detailed implementation plans.

Strategy:
1. Orient: tree/ls to understand project structure, then grep to find the modules relevant to the task.
2. Read key files: focus on interfaces, types, and entry points — not every line of implementation.
3. Plan: produce a numbered step-by-step plan with file:line references. For each step, specify what changes and why.
4. Flag risks: note trade-offs, edge cases, and dependencies between steps.

Keep plans actionable — each step should be a single, testable change.`,
		Tools: []string{"read", "grep", "find", "tree", "ls", "git-overview"},
	},
	"designer": {
		Role:     "slow",
		Worktree: true,
		Instruction: `You are a design agent working in an isolated worktree. Create and modify code following established patterns.

Workflow:
1. Read the existing code around your change point — grep for the symbol, read the relevant section.
2. Match the project's style: naming, error handling, file organization, test patterns.
3. Implement the change using edit for existing files, write only for new files.
4. Run bash to build/compile and verify no errors. Fix any issues before finishing.
5. Return a summary: what changed, file:line references, and build status.

Rules:
- Write clean, idiomatic code that looks like a human wrote it in the style of this project.
- One logical change per edit — do not combine unrelated modifications.
- No dead code, no commented-out code, no TODO placeholders unless explicitly requested.`,
		Tools: []string{"read", "write", "edit", "grep", "find", "tree", "ls", "bash"},
	},
	"reviewer": {
		Role:     "slow",
		Worktree: false,
		Instruction: `You are a code review agent. Examine changes for bugs, correctness, and style issues.

Workflow:
1. Use git-overview to see what changed. Use git-file-diff and git-hunk for details.
2. Read surrounding context with grep/read to understand intent.
3. Check: correctness, error handling, edge cases, naming, test coverage.
4. Report findings as a structured list: file:line, severity (bug/warning/nit), description, suggested fix.

Focus on what matters — bugs and correctness first, style nits last. Be constructive.`,
		Tools: []string{"read", "grep", "find", "git-overview", "git-file-diff", "git-hunk"},
	},
	"task": {
		Role:     "default",
		Worktree: true,
		Instruction: `You are a task execution agent working in an isolated worktree. Complete the assigned coding task end-to-end.

Workflow:
1. Understand: grep for the relevant code, read the targeted sections. Do not read unrelated files.
2. Implement: make the smallest correct change. Edit existing files, match existing patterns.
3. Verify: run bash to build/compile after each edit. Run tests if they exist. Fix failures immediately.
4. Complete: return what you changed (file:line), build/test status, and any notes.

Rules:
- One change at a time — edit, build, confirm, then move to the next.
- If the build fails, read the error, fix the cause, rebuild. Do not retry blindly.
- Match the project's style exactly — naming, error handling, imports, test structure.
- Keep changes minimal. Do not refactor or "improve" untouched code.`,
		Tools: []string{"read", "write", "edit", "bash", "grep", "find", "tree", "ls", "git-overview"},
	},
	"quick_task": {
		Role:     "smol",
		Worktree: false,
		Instruction: `You are a quick task agent. Complete small, focused tasks with minimal overhead.

Workflow:
1. grep to find the exact location to change.
2. Read only the lines you need.
3. Make the edit. Run bash to verify it compiles.
4. Return: what changed, file:line, build status.

Rules:
- Absolute minimum changes — touch only what is necessary.
- No exploration beyond what the task requires.
- If the task is ambiguous, do the simplest reasonable interpretation.`,
		Tools: []string{"read", "write", "edit", "bash", "grep", "find"},
	},
}

// ValidateType checks if the given type name is a known agent type.
func ValidateType(typeName string) error {
	if _, ok := AgentTypes[typeName]; !ok {
		return fmt.Errorf("unknown agent type %q; valid types: explore, plan, designer, reviewer, task, quick_task", typeName)
	}
	return nil
}

// AgentInput is the input to spawn a subagent.
type AgentInput struct {
	Type        string `json:"type"`                   // Agent type name
	Prompt      string `json:"prompt"`                 // Task prompt for the agent
	Worktree    *bool  `json:"worktree,omitempty"`     // Override worktree setting
	Background  bool   `json:"background,omitempty"`   // Run in background
	SkipCleanup bool   `json:"skip_cleanup,omitempty"` // Don't auto-cleanup worktree on completion
}

// AgentOutput is the result of a completed subagent.
type AgentOutput struct {
	AgentID  string `json:"agent_id"`
	Type     string `json:"type"`
	Result   string `json:"result"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

// AgentStatus represents the current state of a subagent.
type AgentStatus struct {
	AgentID   string    `json:"agent_id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"` // "running", "completed", "failed", "cancelled"
	Prompt    string    `json:"prompt"`
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration,omitempty"`
}

// Event is a streaming event from a subagent process.
type Event struct {
	Type    string `json:"type"`              // "text_delta", "tool_call", "tool_result", "message_end", "error"
	Content string `json:"content,omitempty"` // Text content for text_delta
	Error   string `json:"error,omitempty"`   // Error message for error events
}
