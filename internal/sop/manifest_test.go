package sop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

// writeSpec lays down a spec directory and returns the work dir and spec name.
func writeSpec(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	work := t.TempDir()
	name := "features/x"
	dir := filepath.Join(work, "specs", "features", "x")
	if err := os.MkdirAll(filepath.Join(dir, "research"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return work, name
}

// validSpec is a spec that satisfies the full plan contract. It doubles as the
// worked example of what the contract actually demands.
func validSpec() map[string]string {
	return map[string]string{
		"rough-idea.md": "# Rough Idea\n\nAdd a thing.\n",
		"requirements.md": "# Requirements\n\n## Questions & Answers\n\n" +
			"Q1: What is in scope?\nA: The thing.\n\n" +
			"Q2: What are the edge cases?\nA: Empty input.\n\n" +
			"Q3: What are the acceptance criteria?\nA: Below.\n",
		"design.md": "# Design\n\n## Current State\nx\n\n## Desired End State\ny\n\n" +
			"## Acceptance Criteria\n\n- Given no thing, when added, then it exists.\n\n" +
			"## Testing Strategy\n\nTable tests.\n",
		"research/a.md": "# A\n\nThe code does x.\n",
		"research/b.md": "# B\n\nThe code does y.\n",
		"outline.md":    "# Outline\n\n1. Add the type\n2. Wire it up\n",
		"plan.md": "# Plan\n\n## Progress\n\n" +
			"- [ ] Step 1: Add the type\n" +
			"- [ ] Step 2: Wire it up\n",
		"PROMPT.md": "# Thing\n\n## Objective\nAdd a thing.\n\n" +
			"## Acceptance Criteria\n- Given no thing, when added, then it exists.\n\n" +
			"## Implementation Slices\n\n" +
			"1. **Add the type** — define it, files: `a.go`, verify: `go build ./...`, parallel-safe: no\n" +
			"2. **Wire it up** — call it, files: `b.go`, verify: `go build ./...`, parallel-safe: yes\n\n" +
			"## Done Criteria\n- [ ] the type exists\n- [ ] it is called\n- [ ] no stubs remain\n\n" +
			"## Gates\n- **build**: `go build ./...`\n",
	}
}

func TestBuildManifestValidSpec(t *testing.T) {
	work, name := writeSpec(t, validSpec())
	spec, err := specdoc.Load(work, name)
	if err != nil {
		t.Fatal(err)
	}
	m := BuildManifest(spec, work, validate.PlanContract(), time.Now())
	if !m.Valid {
		t.Fatalf("valid spec reported invalid:\n%s", validate.Findings(m.Findings).Format())
	}
	if m.SOPVersion != SOPVersion {
		t.Errorf("SOPVersion = %d, want %d", m.SOPVersion, SOPVersion)
	}
	if len(m.Artifacts) != len(validate.PlanContract().Artifacts) {
		t.Errorf("got %d artifact records, want %d", len(m.Artifacts), len(validate.PlanContract().Artifacts))
	}
	for _, a := range m.Artifacts {
		if !a.Present {
			t.Errorf("artifact %s recorded absent", a.Name)
		}
		if a.Name == specdoc.Plan && a.Slices != 2 {
			t.Errorf("plan.md slices = %d, want 2", a.Slices)
		}
		if a.Name == specdoc.Prompt && a.Slices != 2 {
			t.Errorf("PROMPT.md slices = %d, want 2", a.Slices)
		}
	}
}

// The gap that motivated this package: a spec whose PROMPT.md exists but is
// hollow used to pass the single os.Stat check.
func TestBuildManifestCatchesHollowPrompt(t *testing.T) {
	files := validSpec()
	files["PROMPT.md"] = "# Thing\n\n## Objective\nAdd a thing.\n\n" +
		"## Gates\n- **build**: `<build command discovered during research>`\n"
	work, name := writeSpec(t, files)
	spec, _ := specdoc.Load(work, name)

	m := BuildManifest(spec, work, validate.PlanContract(), time.Now())
	if m.Valid {
		t.Fatal("a PROMPT.md with placeholder gates and no Done Criteria was reported valid")
	}
	rules := map[string]bool{}
	for _, f := range m.Findings {
		rules[f.Rule] = true
	}
	for _, want := range []string{"gates_are_executable", "done_criteria", "has_headings"} {
		if !rules[want] {
			t.Errorf("expected a %s finding; got %v", want, rules)
		}
	}
}

func TestBuildManifestMissingOutlineBlocks(t *testing.T) {
	files := validSpec()
	delete(files, "outline.md")
	work, name := writeSpec(t, files)
	spec, _ := specdoc.Load(work, name)

	m := BuildManifest(spec, work, validate.PlanContract(), time.Now())
	if m.Valid {
		t.Fatal("a spec missing outline.md was reported valid")
	}
	var found bool
	for _, f := range m.Findings {
		if f.Artifact == specdoc.Outline && f.Rule == "required" {
			found = true
		}
	}
	if !found {
		t.Errorf("no required-artifact finding for outline.md: %v", m.Findings)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	work, name := writeSpec(t, validSpec())
	spec, _ := specdoc.Load(work, name)
	m := BuildManifest(spec, work, validate.PlanContract(), time.Now())

	if err := WriteManifest(spec.Dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(spec.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec != m.Spec || got.Valid != m.Valid || got.Contract != m.Contract {
		t.Errorf("round trip mismatch: %+v vs %+v", got, m)
	}
	if !got.Compatible() {
		t.Error("a manifest this build wrote is reported incompatible")
	}
}

func TestManifestCompatibility(t *testing.T) {
	if (&Manifest{SOPVersion: SOPVersion + 1}).Compatible() {
		t.Error("a manifest from a newer SOP version was accepted")
	}
	if (&Manifest{SOPVersion: 0}).Compatible() {
		t.Error("a manifest with no version was accepted")
	}
	var nilManifest *Manifest
	if nilManifest.Compatible() {
		t.Error("a nil manifest was accepted")
	}
}

func TestReadManifestMissing(t *testing.T) {
	if _, err := ReadManifest(t.TempDir()); err == nil {
		t.Error("ReadManifest on a directory with no manifest returned no error")
	}
}

// RunPreflightContract must be narrower than PlanContract: a hand-written spec
// with only plan.md and PROMPT.md should still be runnable.
func TestRunPreflightAcceptsMinimalSpec(t *testing.T) {
	files := validSpec()
	for _, drop := range []string{"rough-idea.md", "requirements.md", "design.md", "outline.md", "research/a.md", "research/b.md"} {
		delete(files, drop)
	}
	work, name := writeSpec(t, files)
	spec, _ := specdoc.Load(work, name)

	if f := validate.Check(spec, work, validate.PlanContract()); f.OK() {
		t.Fatal("PlanContract accepted a spec missing most artifacts")
	}
	if f := validate.Check(spec, work, validate.RunPreflightContract()); !f.OK() {
		t.Errorf("RunPreflightContract rejected a runnable spec:\n%s", f.Format())
	}
}
