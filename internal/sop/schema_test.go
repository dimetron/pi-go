package sop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/sop/validate"
)

// The shipped definitions must parse, lint and compile. If they do not, the
// declarative SOPs are decoration.
func TestEmbeddedDefinitionsCompile(t *testing.T) {
	for _, name := range []string{"plan", "run"} {
		t.Run(name, func(t *testing.T) {
			def, err := LoadDefinition(t.TempDir(), name)
			if err != nil {
				t.Fatalf("LoadDefinition(%q): %v", name, err)
			}
			if def.SOP != name {
				t.Errorf("sop = %q, want %q", def.SOP, name)
			}
			if def.Version != SOPVersion {
				t.Errorf("version = %d, want %d", def.Version, SOPVersion)
			}

			compiled, err := Compile(def, DescribeFactory{})
			if err != nil {
				t.Fatalf("Compile(%q): %v", name, err)
			}
			if len(compiled.Edges) == 0 {
				t.Error("compiled to no edges")
			}
			if _, err := compiled.Workflow(); err != nil {
				t.Fatalf("Workflow(): %v", err)
			}
		})
	}
}

// The plan SOP must schedule the outline stage. It is the phase the prose SOP
// asked for and 70% of specs skipped; making it a graph node is the fix.
func TestPlanSOPSchedulesOutline(t *testing.T) {
	def, err := LoadDefinition(t.TempDir(), "plan")
	if err != nil {
		t.Fatal(err)
	}
	outline, ok := def.Stage("outline")
	if !ok {
		t.Fatal("plan SOP has no outline stage")
	}
	if outline.Review == nil || outline.Review.Kind != "human" {
		t.Error("outline stage has no human review checkpoint")
	}
	if len(outline.Produces) == 0 {
		t.Fatal("outline stage produces nothing")
	}

	// Every stage must be reachable: an unreachable outline is the same as no
	// outline.
	compiled, err := Compile(def, DescribeFactory{})
	if err != nil {
		t.Fatal(err)
	}
	reached := map[string]bool{}
	for _, e := range compiled.Edges {
		reached[e.To.Name()] = true
	}
	for _, s := range def.Stages[1:] { // the first stage is the entry point
		if !reached[s.ID] {
			t.Errorf("stage %q is unreachable", s.ID)
		}
	}
}

// The run SOP must route the verifier's verdict. An unrouted verdict is why
// the prose Verifier reported PASS in 9 of 9 runs and nothing acted on it.
func TestRunSOPRoutesTheVerdict(t *testing.T) {
	def, err := LoadDefinition(t.TempDir(), "run")
	if err != nil {
		t.Fatal(err)
	}
	verify, ok := def.Stage("verify")
	if !ok {
		t.Fatal("run SOP has no verify stage")
	}
	for _, want := range []string{"PASS", "FAIL"} {
		if _, ok := verify.Routes[want]; !ok {
			t.Errorf("verify stage does not route %s", want)
		}
	}
	if verify.Routes["FAIL"] == verify.Routes["PASS"] {
		t.Error("PASS and FAIL route to the same stage")
	}

	repair, ok := def.Stage("repair")
	if !ok {
		t.Fatal("run SOP has no repair stage")
	}
	if repair.LoopBack != "verify" {
		t.Errorf("repair loops back to %q, want verify", repair.LoopBack)
	}
	if repair.MaxCycle <= 0 {
		t.Error("repair loop is unbounded")
	}
}

func TestLintRejectsWorktreeAgent(t *testing.T) {
	def := mustParse(t, `
sop: run
version: 2
stages:
  - id: build
    agent: designer
    next: done
  - id: done
    kind: function
`)
	findings := LintDefinition(def)
	if findings.OK() {
		t.Fatal("a stage dispatching to a [worktree] agent was accepted")
	}
	if !hasRule(findings, "worktree_agent") {
		t.Errorf("no worktree_agent finding: %s", findings.Format())
	}
	if !strings.Contains(findings.Format(), "never merged") {
		t.Errorf("finding does not explain the consequence:\n%s", findings.Format())
	}
}

func TestLintRejectsDanglingEdge(t *testing.T) {
	def := mustParse(t, `
sop: run
version: 2
stages:
  - id: a
    kind: function
    next: nowhere
`)
	if findings := LintDefinition(def); !hasRule(findings, "dangling_edge") {
		t.Errorf("a next: pointing at no stage was accepted: %s", findings.Format())
	}
}

func TestLintRejectsUnboundedLoop(t *testing.T) {
	def := mustParse(t, `
sop: run
version: 2
stages:
  - id: a
    kind: function
    next: b
  - id: b
    kind: function
    loop_back: a
`)
	if findings := LintDefinition(def); !hasRule(findings, "unbounded_loop") {
		t.Errorf("a loop with no max_cycles was accepted: %s", findings.Format())
	}
}

func TestLintRejectsUnknownValidator(t *testing.T) {
	def := mustParse(t, `
sop: plan
version: 2
stages:
  - id: a
    agent: plan
    produces:
      - path: plan.md
        validate: [no_such_rule]
`)
	if findings := LintDefinition(def); !hasRule(findings, "unknown_rule") {
		t.Errorf("an unknown validator was accepted: %s", findings.Format())
	}
}

func TestLintRejectsUnroutedAgentVerdict(t *testing.T) {
	def := mustParse(t, `
sop: plan
version: 2
stages:
  - id: a
    agent: plan
    review:
      kind: agent
      agent: spec-reviewer
`)
	if findings := LintDefinition(def); !hasRule(findings, "unrouted_verdict") {
		t.Errorf("an agent review with no routes was accepted: %s", findings.Format())
	}
}

func TestLintRejectsDuplicateStageID(t *testing.T) {
	def := mustParse(t, `
sop: plan
version: 2
stages:
  - id: a
    kind: function
    next: b
  - id: b
    kind: function
  - id: a
    kind: function
`)
	if findings := LintDefinition(def); !hasRule(findings, "stage_id") {
		t.Errorf("a duplicate stage id was accepted: %s", findings.Format())
	}
}

func TestParseDefinitionRejectsUnknownField(t *testing.T) {
	_, err := ParseDefinition([]byte("sop: plan\nversion: 2\nstagez: []\n"))
	if err == nil {
		t.Fatal("an unknown top-level field was accepted; a typo would silently disable behavior")
	}
}

func TestNodeConfigInheritsDefaults(t *testing.T) {
	defaults := Defaults{
		Timeout: Duration(20 * time.Minute),
		Retry:   &Retry{MaxAttempts: 3, InitialDelay: Duration(5 * time.Second), Backoff: 2, Jitter: 1},
	}

	inherited := NodeConfigFor(Stage{ID: "a"}, defaults)
	if inherited.Timeout != 20*time.Minute {
		t.Errorf("timeout = %s, want the default 20m", inherited.Timeout)
	}
	if inherited.RetryConfig == nil || inherited.RetryConfig.MaxAttempts != 3 {
		t.Errorf("retry not inherited: %+v", inherited.RetryConfig)
	}

	override := NodeConfigFor(Stage{
		ID:      "b",
		Timeout: Duration(90 * time.Second),
		Retry:   &Retry{MaxAttempts: 7},
	}, defaults)
	if override.Timeout != 90*time.Second {
		t.Errorf("timeout = %s, want the stage override 90s", override.Timeout)
	}
	if override.RetryConfig.MaxAttempts != 7 {
		t.Errorf("MaxAttempts = %d, want 7", override.RetryConfig.MaxAttempts)
	}
	// Unset fields fall back to the engine defaults, not to zero.
	if override.RetryConfig.MaxDelay == 0 {
		t.Error("MaxDelay = 0; an unset retry field should keep the engine default")
	}
}

func TestFanOutStageIsParallelWorker(t *testing.T) {
	cfg := NodeConfigFor(Stage{ID: "slices", FanOut: &FanOut{Over: "plan.slices"}}, Defaults{})
	if !cfg.ParallelWorker {
		t.Error("a fan_out stage did not compile to a ParallelWorker node")
	}
}

func TestDurationParsing(t *testing.T) {
	def := mustParse(t, "sop: plan\nversion: 2\ndefaults:\n  timeout: 90s\nstages:\n  - id: a\n    kind: function\n")
	if got := def.Defaults.Timeout.Duration(); got != 90*time.Second {
		t.Errorf("timeout = %s, want 90s", got)
	}
	if _, err := ParseDefinition([]byte("sop: plan\nversion: 2\ndefaults:\n  timeout: banana\n")); err == nil {
		t.Error("an unparseable duration was accepted")
	}
}

func TestCompiledDescribeNamesStagesAndEdges(t *testing.T) {
	def, err := LoadDefinition(t.TempDir(), "run")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(def, DescribeFactory{})
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Describe()
	for _, want := range []string{"slices", "verify", "repair", "merge", "->"} {
		if !strings.Contains(got, want) {
			t.Errorf("description omits %q:\n%s", want, got)
		}
	}
}

func TestLoadDefinitionPrefersProjectOverride(t *testing.T) {
	work := t.TempDir()
	writeOverride(t, work, "plan.sop.yaml", `
sop: plan
version: 2
description: project override
stages:
  - id: only
    kind: function
`)
	def, err := LoadDefinition(work, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if def.Description != "project override" {
		t.Errorf("description = %q, want the project override", def.Description)
	}
}

func TestLoadDefinitionRejectsBrokenOverride(t *testing.T) {
	work := t.TempDir()
	writeOverride(t, work, "plan.sop.yaml", `
sop: plan
version: 2
stages:
  - id: a
    kind: function
    next: nowhere
`)
	_, err := LoadDefinition(work, "plan")
	if err == nil {
		t.Fatal("a project override with a dangling edge was accepted")
	}
	if !strings.Contains(err.Error(), "not a stage") {
		t.Errorf("error does not name the problem: %v", err)
	}
}

func hasRule(fs validate.Findings, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func mustParse(t *testing.T, src string) *Definition {
	t.Helper()
	def, err := ParseDefinition([]byte(src))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	return def
}

func writeOverride(t *testing.T, workDir, name, body string) {
	t.Helper()
	dir := filepath.Join(workDir, ".pi-go", "sops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
