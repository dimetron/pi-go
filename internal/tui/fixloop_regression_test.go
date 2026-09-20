package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixLoopQueuesRepairForContractFailures is the regression test for the
// automatic fix loop: a spec that fails the PDD contract must both report the
// findings and queue a repair prompt carrying them, so the session repairs
// itself instead of waiting for a human turn.
func TestFixLoopQueuesRepairForContractFailures(t *testing.T) {
	work := t.TempDir()
	const taskName = "features/x"

	// Missing outline/design/research plus a plan whose slice count disagrees
	// with PROMPT.md: several independent contract violations at once.
	writeTestSpec(t, work, taskName, map[string]string{
		"PROMPT.md": strings.Join([]string{
			"# T",
			"",
			"## Objective",
			"Do it.",
			"",
			"## Acceptance Criteria",
			"- Given a, when b, then c.",
			"",
			"## Implementation Slices",
			"",
			"1. **One** — do x, files: `a.go`, verify: `go build ./...`, parallel-safe: yes",
			"",
			"## Done Criteria",
			"- [ ] one observable outcome",
			"- [ ] two observable outcome",
			"- [ ] three observable outcome",
			"",
			"## Gates",
			"- **build**: `go build ./...`",
			"",
			"## Reference",
			"- Design: `specs/features/x/design.md`",
		}, "\n"),
		"plan.md": "# P\n\n- [ ] Step 1: One\n",
	})

	m := &model{cfg: Config{WorkDir: work, PlanAutoFix: true}, planTaskName: taskName}
	if m.validatePlanArtifacts() {
		t.Fatal("a spec missing design.md/outline.md/research was accepted")
	}

	report := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	// Assert the shape of the report, not one exact finding list: which
	// artifacts a half-written spec is missing is the contract's business, and
	// pinning it here would make this test fail every time the contract grows.
	for _, want := range []string{"Plan not yet complete", "ERROR", "fix:"} {
		if !strings.Contains(report, want) {
			t.Errorf("report omits %q:\n%s", want, report)
		}
	}
	if !strings.Contains(report, "Repairing automatically") {
		t.Errorf("report does not announce the repair:\n%s", report)
	}
	if len(m.pendingPrompts) != 1 {
		t.Fatalf("queued %d repair prompts, want 1", len(m.pendingPrompts))
	}
	queued := m.pendingPrompts[0].text
	for _, want := range []string{taskName, "attempt 1 of", "design.md", "specs/"} {
		if !strings.Contains(queued, want) {
			t.Errorf("repair prompt omits %q:\n%s", want, queued)
		}
	}
}

// TestHistoricalDefectsNoLongerFailThePlan guards the three parser bugs that
// produced the original report. They were fixed in specdoc, so a spec carrying
// all three shapes must validate.
func TestHistoricalDefectsNoLongerFailThePlan(t *testing.T) {
	work := t.TempDir()
	const taskName = "features/x"
	specDir := filepath.Join(work, "specs", filepath.FromSlash(taskName))
	writeValidPlanSpec(t, specDir)

	promptPath := filepath.Join(specDir, "PROMPT.md")
	body, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := string(body)

	// 1. the underscore spelling the artifact contract documented for slices
	patched = strings.ReplaceAll(patched, "parallel-safe:", "parallel_safe:")
	// 2. a sub-heading that contains "gates" before the real ## Gates
	patched = strings.Replace(patched, "## Gates", "### Dependencies and gates\n- not the gate list\n\n## Gates", 1)
	// 3. a backticked non-path under ## Reference
	patched = strings.Replace(patched, "## Reference", "## Reference\n- Baseline branch: `feature/mcp-sdk-migration`", 1)

	if err := os.WriteFile(promptPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{cfg: Config{WorkDir: work, PlanAutoFix: true}, planTaskName: taskName}
	if !m.validatePlanArtifacts() {
		last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
		t.Fatalf("a spec with the three historical shapes was rejected:\n%s", last)
	}
}
