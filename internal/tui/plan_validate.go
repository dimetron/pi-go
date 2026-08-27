package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/specdoc"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

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

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: formatPlanValidationFailure(m.planTaskName, findings),
	})
	return false
}

// formatPlanValidationFailure renders blocking findings as the planner's next
// task rather than as an error report: the session is still open, and every
// finding carries the fix that clears it.
func formatPlanValidationFailure(taskName string, findings validate.Findings) string {
	errs := findings.Errors()
	var b strings.Builder
	fmt.Fprintf(&b, "**Plan not yet complete** — %d artifact check(s) failed for `%s`.\n\n", len(errs), taskName)
	b.WriteString(findings.Format())
	b.WriteString("\nThe planning worktree is kept. Address these and the plan merges on the next turn.\n")
	return b.String()
}
