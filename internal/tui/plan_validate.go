package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/specdoc"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

// maxPlanFixCycles bounds automatic validation repair. It matches the plan
// SOP's own max_cycles for the spec-review loop: a plan that cannot satisfy the
// contract in this many attempts is not going to, and continuing to spend
// tokens on it hides the real problem.
//
// Overridable via PI_PLAN_MAX_FIX_CYCLES so a long-running session can raise it
// without a rebuild.
const defaultMaxPlanFixCycles = 10

// validatePlanArtifacts checks the spec against the PDD contract and records
// the result as a manifest in the spec directory.
//
// It returns true when the plan may be merged. On failure it appends the
// findings to the conversation — phrased as work for the planner, since the
// planning session is still live and the next turn can act on them — and keeps
// the worktree so that fix lands in the same branch.
//
// This replaces the single os.Stat that used to be the whole check. Across the
// 53 specs planned before it, 3 had a complete artifact set: the SOP asked for
// the rest in prose, and prose does not block a merge.
func (m *model) validatePlanArtifacts() bool {
	workDir := m.cfg.WorkDir
	if m.planWorktreePath != "" {
		workDir = m.planWorktreePath
	}

	spec, err := specdoc.Load(workDir, m.planTaskName)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Could not read the spec for validation: %v", err),
		})
		return false
	}

	manifest := sop.BuildManifest(spec, workDir, validate.PlanContract(), time.Now())
	if err := sop.WriteManifest(spec.Dir, manifest); err != nil {
		// A manifest that cannot be written is not a reason to lose the plan.
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Warning: could not write %s: %v", sop.ManifestName, err),
		})
	}

	findings := validate.Findings(manifest.Findings)
	if manifest.Valid {
		if len(findings) > 0 {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: "**Plan validated** with warnings:\n\n" + findings.Format(),
			})
		} else {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: "**Plan validated** — all artifacts satisfy the PDD contract.",
			})
		}
		return true
	}

	// Automatic fix loop. The findings are already phrased as the planner's
	// next task, and the worktree is intact, so re-dispatch them without a
	// human turn. The outcome is appended to the report itself rather than sent
	// as a separate message, so the findings stay the last thing in the
	// conversation and a caller reading it sees the report, not a status line.
	fix := m.queuePlanRepair(findings)

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: formatPlanValidationFailure(m.planTaskName, findings, fix),
	})
	return false
}

// queuePlanRepair re-enters the planner with the blocking findings as its
// prompt, and reports what it did.
//
// The guard is deliberately not `len(m.pendingPrompts) == 0`: a user prompt
// queued while the planner ran must keep its place, so the repair goes behind
// it. The cycle counter is what bounds the loop, not the queue.
func (m *model) queuePlanRepair(findings validate.Findings) planRepairOutcome {
	maxCycles := planMaxFixCycles()
	if !m.cfg.PlanAutoFix {
		return planRepairOutcome{max: maxCycles,
			reason: "Automatic plan repair is off; apply the findings, then continue."}
	}
	if m.planFixCycle >= maxCycles {
		return planRepairOutcome{max: maxCycles,
			reason: fmt.Sprintf(
				"Automatic plan repair stopped after %d attempt(s) and the plan still fails the PDD contract; "+
					"apply the findings, or raise PI_PLAN_MAX_FIX_CYCLES.", maxCycles)}
	}
	m.planFixCycle++

	var b strings.Builder
	fmt.Fprintf(&b, "Fix the plan validation findings for spec %q (attempt %d of %d).\n\n",
		m.planTaskName, m.planFixCycle, maxCycles)
	b.WriteString("Each finding below names the artifact, the failing rule and the fix that clears it. ")
	b.WriteString("Edit only the files under specs/ — this is a planning session, not an implementation one.\n\n")
	b.WriteString(findings.Format())
	b.WriteString("\nWhen the findings are cleared the plan validates and merges automatically; do not ask for confirmation.")

	m.pendingPrompts = append(m.pendingPrompts, queuedPrompt{text: b.String()})
	return planRepairOutcome{queued: true, cycle: m.planFixCycle, max: maxCycles}
}

// planMaxFixCycles resolves the bound for automatic plan repair.
func planMaxFixCycles() int {
	if v := strings.TrimSpace(os.Getenv("PI_PLAN_MAX_FIX_CYCLES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxPlanFixCycles
}

// planRepairOutcome describes what the automatic fix loop did about a failed
// validation, so the failure report can state it instead of leaving the reader
// to wonder whether anything will happen next.
type planRepairOutcome struct {
	queued bool
	cycle  int
	max    int
	reason string // why no attempt was queued
}

// formatPlanValidationFailure renders blocking findings as the planner's next
// task rather than as an error report: the session is still open, and every
// finding carries the fix that clears it.
func formatPlanValidationFailure(taskName string, findings validate.Findings, fix planRepairOutcome) string {
	errs := findings.Errors()
	var b strings.Builder
	fmt.Fprintf(&b, "**Plan not yet complete** — %d artifact check(s) failed for `%s`.\n\n", len(errs), taskName)
	b.WriteString(findings.Format())
	switch {
	case fix.queued:
		fmt.Fprintf(&b, "\nThe planning worktree is kept. Repairing automatically (attempt %d of %d).\n",
			fix.cycle, fix.max)
	case fix.reason != "":
		fmt.Fprintf(&b, "\nThe planning worktree is kept. %s\n", fix.reason)
	default:
		b.WriteString("\nThe planning worktree is kept. Address these and the plan merges on the next turn.\n")
	}
	return b.String()
}
