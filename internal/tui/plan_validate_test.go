package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

// writeTestSpec lays down a spec under workDir/specs/<name>.
func writeTestSpec(t *testing.T, workDir, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(workDir, "specs", filepath.FromSlash(name))
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runnableSpec() map[string]string {
	return map[string]string{
		"plan.md": "# Plan\n\n- [ ] Step 1: Add the type\n- [ ] Step 2: Wire it up\n",
		"PROMPT.md": "# Thing\n\n## Objective\nAdd a thing.\n\n" +
			"## Implementation Slices\n\n" +
			"1. **Add the type** — define it, files: `a.go`, verify: `go build ./...`, parallel-safe: no\n" +
			"2. **Wire it up** — call it, files: `b.go`, verify: `go build ./...`, parallel-safe: yes\n\n" +
			"## Gates\n- **build**: `go build ./...`\n",
	}
}

func TestRunPreflightAcceptsRunnableSpec(t *testing.T) {
	work := t.TempDir()
	writeTestSpec(t, work, "features/x", runnableSpec())

	m := &model{cfg: Config{WorkDir: work}}
	findings, ok := m.runPreflight("features/x")
	if !ok {
		t.Fatalf("preflight rejected a runnable spec:\n%s", findings.Format())
	}
}

func TestRunPreflightRejectsPlaceholderGates(t *testing.T) {
	work := t.TempDir()
	files := runnableSpec()
	files["PROMPT.md"] = strings.Replace(files["PROMPT.md"],
		"- **build**: `go build ./...`",
		"- **build**: `<build command discovered during research>`", 1)
	writeTestSpec(t, work, "features/x", files)

	m := &model{cfg: Config{WorkDir: work}}
	findings, ok := m.runPreflight("features/x")
	if ok {
		t.Fatal("preflight accepted a spec whose gate is still template text")
	}
	if !strings.Contains(findings.Format(), "template placeholder") {
		t.Errorf("findings do not name the placeholder:\n%s", findings.Format())
	}
}

func TestRunPreflightRejectsMissingSpec(t *testing.T) {
	m := &model{cfg: Config{WorkDir: t.TempDir()}}
	if _, ok := m.runPreflight("nope"); ok {
		t.Fatal("preflight accepted a spec that does not exist")
	}
}

func TestRunPreflightRejectsOversizedSlice(t *testing.T) {
	work := t.TempDir()
	files := runnableSpec()
	var paths []string
	for i := 0; i < 14; i++ {
		paths = append(paths, "`f"+string(rune('a'+i))+".go`")
	}
	files["PROMPT.md"] = "# T\n\n## Implementation Slices\n\n1. **Big** — files: " +
		strings.Join(paths, ", ") + ", verify: `go build ./...`, parallel-safe: no\n\n" +
		"## Gates\n- **build**: `go build ./...`\n"
	files["plan.md"] = "# Plan\n\n- [ ] Step 1: Big\n"
	writeTestSpec(t, work, "features/x", files)

	m := &model{cfg: Config{WorkDir: work}}
	findings, ok := m.runPreflight("features/x")
	if ok {
		t.Fatal("preflight accepted a slice naming 14 files")
	}
	if !strings.Contains(findings.Format(), "file budget") {
		t.Errorf("findings do not name the budget:\n%s", findings.Format())
	}
}

func TestWriteRunManifestRecordsResult(t *testing.T) {
	work := t.TempDir()
	dir := writeTestSpec(t, work, "features/x", runnableSpec())

	m := &model{cfg: Config{WorkDir: work}}
	m.writeRunManifest("features/x")

	manifest, err := sop.ReadManifest(dir)
	if err != nil {
		t.Fatalf("no manifest written: %v", err)
	}
	if !manifest.Valid {
		t.Errorf("manifest reports invalid for a runnable spec: %+v", manifest.Findings)
	}
	if manifest.Contract != "run-preflight" {
		t.Errorf("contract = %q, want run-preflight", manifest.Contract)
	}
	if !manifest.Compatible() {
		t.Error("freshly written manifest is not compatible")
	}
}

func TestFormatPreflightBlockNamesTheOverride(t *testing.T) {
	out := formatPreflightBlock("features/x", validate.Findings{
		{Artifact: "PROMPT.md", Rule: "gates_are_executable", Severity: validate.SeverityError, Message: "bad gate"},
	})
	if !strings.Contains(out, "--force") {
		t.Errorf("block message does not mention the override:\n%s", out)
	}
	if !strings.Contains(out, "bad gate") {
		t.Errorf("block message does not include the finding:\n%s", out)
	}
}

// The planner must be shown the contract it is held to, or validation is a
// late failure rather than a loop.
func TestPlanContractDescribeIsInstructional(t *testing.T) {
	got := validate.PlanContract().Describe()
	for _, want := range []string{"outline.md", "PROMPT.md", "Done Criteria", "parallel_safe", "research/"} {
		if !strings.Contains(got, want) {
			t.Errorf("contract description omits %q:\n%s", want, got)
		}
	}
}

// writeValidPlanSpec writes a spec satisfying the full PDD contract into
// specDir. Tests that drive the end-of-plan teardown need one, because
// finishPlanWorktree now validates before it merges.
func writeValidPlanSpec(t *testing.T, specDir string) {
	t.Helper()
	files := map[string]string{
		"rough-idea.md": "# Rough Idea\n\nDo it.\n",
		"requirements.md": "# Requirements\n\n## Questions & Answers\n\n" +
			"Q1: What is in scope?\nA: The feature.\n\n" +
			"Q2: What are the edge cases?\nA: Empty input.\n\n" +
			"Q3: How is it verified?\nA: go build.\n",
		"design.md": "# Design\n\n## Current State\nNone.\n\n## Desired End State\nIt exists.\n\n" +
			"## Acceptance Criteria\n\n- Given no feature, when added, then it exists.\n\n" +
			"## Testing Strategy\n\nTable tests.\n",
		"research/notes.md":    "# Notes\n\nThe code does x today.\n",
		"research/patterns.md": "# Patterns\n\nThe repo uses functional options.\n",
		"outline.md":           "# Outline\n\n1. Add the type\n2. Wire it up\n",
		"plan.md":              "# Plan\n\n## Progress\n\n- [ ] Step 1: Add the type\n- [ ] Step 2: Wire it up\n",
		"PROMPT.md": "# Feature\n\n## Objective\nDo it.\n\n" +
			"## Acceptance Criteria\n- Given no feature, when added, then it exists.\n\n" +
			"## Implementation Slices\n\n" +
			"1. **Add the type** — define it, files: `a.go`, verify: `go build ./...`, parallel-safe: no\n" +
			"2. **Wire it up** — call it, files: `b.go`, verify: `go build ./...`, parallel-safe: yes\n\n" +
			"## Done Criteria\n- [ ] the type exists\n- [ ] it is called\n- [ ] no stubs remain\n\n" +
			"## Gates\n- **build**: `go build ./...`\n",
	}
	for rel, body := range files {
		full := filepath.Join(specDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFinishPlanWorktreeHoldsBackInvalidSpec is the behavior change: a
// PROMPT.md that merely exists used to be enough to merge. Now a spec that
// fails the contract keeps the worktree so the next planning turn can fix it.
func TestValidatePlanArtifactsHoldsBackInvalidSpec(t *testing.T) {
	work := t.TempDir()
	const taskName = "features/x"
	specDir := writeTestSpec(t, work, taskName, map[string]string{
		"PROMPT.md": "# Feature\n\n## Objective\nDo it.\n",
	})

	m := &model{cfg: Config{WorkDir: work}, planTaskName: taskName}
	if m.validatePlanArtifacts() {
		t.Fatal("a spec with only a stub PROMPT.md was accepted")
	}

	msg := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	if !strings.Contains(msg, "Plan not yet complete") {
		t.Errorf("message does not frame the findings as remaining work:\n%s", msg)
	}
	for _, want := range []string{"outline.md", "plan.md", "Done Criteria"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message omits the missing %q:\n%s", want, msg)
		}
	}

	// The manifest is written even for an invalid plan, so the failure is a
	// record rather than only a chat message.
	manifest, err := sop.ReadManifest(specDir)
	if err != nil {
		t.Fatalf("no manifest written for an invalid plan: %v", err)
	}
	if manifest.Valid {
		t.Error("manifest reports an invalid plan as valid")
	}
}

func TestValidatePlanArtifactsAcceptsConformingSpec(t *testing.T) {
	work := t.TempDir()
	const taskName = "features/x"
	specDir := filepath.Join(work, "specs", filepath.FromSlash(taskName))
	writeValidPlanSpec(t, specDir)

	m := &model{cfg: Config{WorkDir: work}, planTaskName: taskName}
	if !m.validatePlanArtifacts() {
		t.Fatalf("a conforming spec was rejected:\n%s",
			m.chatModel.Messages[len(m.chatModel.Messages)-1].content)
	}

	manifest, err := sop.ReadManifest(specDir)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Valid || manifest.Contract != "pdd-plan" {
		t.Errorf("manifest = %+v, want a valid pdd-plan record", manifest)
	}
}
