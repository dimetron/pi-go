package session

import (
	"os"
	"strconv"
)

// Environment variables carrying a spawned agent's place in its run tree.
//
// They ride the existing PI_ allowlist prefix (internal/subagent/environ.go),
// so a parent that sets them reaches its child without further plumbing. The
// child records them on its own session metadata at creation time, which turns
// every question the 2026-08-10 investigation answered by inference — which
// coordinator owns this worker, which spec, which slice, is this a retry — into
// a field lookup.
const (
	EnvAgentID       = "PI_AGENT_ID"
	EnvAgentType     = "PI_AGENT_TYPE"
	EnvRunID         = "PI_RUN_ID"
	EnvSpecName      = "PI_SPEC_NAME"
	EnvSlice         = "PI_RUN_SLICE"
	EnvCycle         = "PI_RUN_CYCLE"
	EnvParentSession = "PI_PARENT_SESSION"
	EnvAgentBranch   = "PI_AGENT_BRANCH"
	// EnvWorktreeRoot is already set by the orchestrator for path
	// normalization; the agent context records it too so a worktree on disk
	// can be traced back to the spec it belongs to.
	EnvWorktreeRoot = "PI_WORKTREE_ROOT"
)

// AgentContextFromEnv builds an AgentContext from the spawn environment, or
// returns nil when this process was not spawned as part of an agent tree.
//
// An interactive session sets none of these, so the block stays absent from
// meta.json exactly as PlanContext does.
func AgentContextFromEnv() *AgentContext {
	ctx := &AgentContext{
		AgentID:   os.Getenv(EnvAgentID),
		AgentType: os.Getenv(EnvAgentType),
		RunID:     os.Getenv(EnvRunID),
		SpecName:  os.Getenv(EnvSpecName),
		ParentID:  os.Getenv(EnvParentSession),
		Worktree:  os.Getenv(EnvWorktreeRoot),
		Branch:    os.Getenv(EnvAgentBranch),
		Slice:     envInt(EnvSlice),
		Cycle:     envInt(EnvCycle),
	}
	if ctx.isEmpty() {
		return nil
	}
	return ctx
}

// isEmpty reports whether nothing identifying was set. Worktree alone does not
// count: the orchestrator sets it for every spawned agent including ones with
// no run attribution, and a context holding only a path answers none of the
// questions the block exists for.
func (a *AgentContext) isEmpty() bool {
	return a.AgentID == "" && a.AgentType == "" && a.RunID == "" &&
		a.SpecName == "" && a.ParentID == "" && a.Branch == ""
}

// Env renders the context as environment assignments for a child process.
// Empty fields are omitted so a child never inherits a misleading zero.
func (a *AgentContext) Env() []string {
	if a == nil {
		return nil
	}
	var out []string
	add := func(k, v string) {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	add(EnvAgentID, a.AgentID)
	add(EnvAgentType, a.AgentType)
	add(EnvRunID, a.RunID)
	add(EnvSpecName, a.SpecName)
	add(EnvParentSession, a.ParentID)
	add(EnvAgentBranch, a.Branch)
	if a.Slice > 0 {
		add(EnvSlice, strconv.Itoa(a.Slice))
	}
	if a.Cycle > 0 {
		add(EnvCycle, strconv.Itoa(a.Cycle))
	}
	return out
}

func envInt(key string) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
