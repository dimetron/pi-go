package exec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/sop"
)

// runStage is the shorthand every case here needs: one stage, one runner, one
// outcome.
func runStage(t *testing.T, r ShellRunner, stage sop.Stage) (StageOutcome, error) {
	t.Helper()
	return r.RunStage(context.Background(), StageRequest{Stage: stage, Cycle: 1})
}

// TestShellRunnerExitStatusRoutes pins the contract the whole declarative SOP
// rests on: a script's exit status, and nothing else, decides the route.
func TestShellRunnerExitStatusRoutes(t *testing.T) {
	tests := []struct {
		name      string
		script    string
		wantRoute string
		wantRetry bool
	}{
		{name: "success passes", script: "true", wantRoute: sop.VerdictPass},
		{name: "failure routes FAIL", script: "exit 1", wantRoute: sop.VerdictFail},
		{name: "other failure routes FAIL", script: "exit 2", wantRoute: sop.VerdictFail},
		{name: "temp fail asks to be retried", script: "exit 75", wantRetry: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runStage(t, ShellRunner{}, sop.Stage{ID: "s", Run: tt.script})

			if tt.wantRetry {
				if !StageNotReady(err) {
					t.Fatalf("err = %v, want a not-ready error so the engine retries", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Route != tt.wantRoute {
				t.Errorf("route = %q, want %q", out.Route, tt.wantRoute)
			}
		})
	}
}

// TestShellRunnerRunsGateAfterBody verifies the gate runs after the body and
// can fail a stage whose body succeeded — the ordering that makes a "verified"
// stage verified.
func TestShellRunnerRunsGateAfterBody(t *testing.T) {
	dir := t.TempDir()

	// The script names the file relative to the runner's working directory.
	// Interpolating the absolute path would embed a Windows path into a bash
	// script, where the backslashes read as escapes and the redirect lands
	// somewhere else entirely.
	stage := sop.Stage{
		ID:   "s",
		Run:  "echo body >> order",
		Gate: "echo gate >> order; exit 1",
	}
	out, err := runStage(t, ShellRunner{Dir: dir}, stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Route != sop.VerdictFail {
		t.Errorf("route = %q, want %q — a failing gate fails the stage", out.Route, sop.VerdictFail)
	}

	got, err := os.ReadFile(filepath.Join(dir, "order"))
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if want := "body\ngate\n"; string(got) != want {
		t.Errorf("execution order = %q, want %q", got, want)
	}
}

// TestShellRunnerSkipsGateWhenBodyFails verifies a failed body short-circuits:
// running the gate anyway would report on a stage that never happened.
func TestShellRunnerSkipsGateWhenBodyFails(t *testing.T) {
	dir := t.TempDir()

	// Relative, so the gate would really write inside dir on every platform —
	// an absolute Windows path in a bash script would make this pass whether
	// or not the gate ran.
	stage := sop.Stage{ID: "s", Run: "exit 1", Gate: "touch gate-ran"}
	if _, err := runStage(t, ShellRunner{Dir: dir}, stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gate-ran")); err == nil {
		t.Error("gate ran after a failed body")
	}
}

// TestShellRunnerExportsStageEnv verifies a script addresses the run by
// environment variable rather than by text interpolation.
func TestShellRunnerExportsStageEnv(t *testing.T) {
	dir := t.TempDir()
	runDir := t.TempDir()

	stage := sop.Stage{
		ID:  "watch",
		Run: `printf '%s %s %s %s' "$PI_STAGE" "$PI_CYCLE" "$PI_RUN_DIR" "$PI_PR" > env`,
	}
	r := ShellRunner{Dir: dir, RunDir: runDir, Env: []string{"PI_PR=https://example.test/pull/1"}}
	if _, err := r.RunStage(context.Background(), StageRequest{Stage: stage, Cycle: 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	want := "watch 3 " + runDir + " https://example.test/pull/1"
	if string(got) != want {
		t.Errorf("stage env = %q, want %q", got, want)
	}
}

// TestShellRunnerValidatesProduces verifies a stage that exits 0 without
// writing what it declared still fails. A stage reporting success for an
// artifact nobody checked is the failure the SOP's validators exist to catch.
func TestShellRunnerValidatesProduces(t *testing.T) {
	runDir := t.TempDir()
	stage := sop.Stage{
		ID:       "diagnose",
		Run:      "true",
		Produces: []sop.Produces{{Path: "failures.log", Validate: []string{"non_empty"}}},
	}

	out, err := runStage(t, ShellRunner{RunDir: runDir}, stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Route != sop.VerdictFail {
		t.Errorf("route = %q, want %q for a missing declared artifact", out.Route, sop.VerdictFail)
	}

	// Writing it makes the same stage pass.
	if err := os.WriteFile(filepath.Join(runDir, "failures.log"), []byte("boom\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	out, err = runStage(t, ShellRunner{RunDir: runDir}, stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Route != sop.VerdictPass {
		t.Errorf("route = %q, want %q once the artifact exists", out.Route, sop.VerdictPass)
	}
}

// TestShellRunnerDispatchesAgentStages verifies an agent stage goes to the
// injected runner rather than the shell.
func TestShellRunnerDispatchesAgentStages(t *testing.T) {
	var got string
	r := ShellRunner{Agent: RunnerFunc(func(_ context.Context, req StageRequest) (StageOutcome, error) {
		got = req.Stage.AgentName()
		return StageOutcome{Route: sop.VerdictPass}, nil
	})}

	if _, err := runStage(t, r, sop.Stage{ID: "fix", Agent: "worker"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "worker" {
		t.Errorf("dispatched agent = %q, want worker", got)
	}
}

// TestShellRunnerRefusesAgentStageWithoutRunner verifies an agent stage with no
// runner configured fails loudly. Passing silently would let a SOP report a
// stage complete that never ran.
func TestShellRunnerRefusesAgentStageWithoutRunner(t *testing.T) {
	_, err := runStage(t, ShellRunner{}, sop.Stage{ID: "fix", Agent: "worker"})
	if err == nil {
		t.Fatal("expected an error for an agent stage with no agent runner")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Errorf("error %q should name the agent it could not dispatch", err)
	}
}

// TestShellRunnerEmptyStagePasses verifies a stage with no script is a marker
// rather than a failure, so a SOP can name a step it does not yet automate.
func TestShellRunnerEmptyStagePasses(t *testing.T) {
	out, err := runStage(t, ShellRunner{}, sop.Stage{ID: "marker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Route != "" {
		t.Errorf("route = %q, want the forward path", out.Route)
	}
}

// TestShellRunnerReportsUnstartableScript verifies a shell that cannot run at
// all is a broken run, not a stage verdict.
func TestShellRunnerReportsUnstartableScript(t *testing.T) {
	r := ShellRunner{Shell: filepath.Join(t.TempDir(), "no-such-shell")}
	_, err := runStage(t, r, sop.Stage{ID: "s", Run: "true"})
	if err == nil {
		t.Fatal("expected an error when the shell cannot be started")
	}
	if StageNotReady(err) {
		t.Error("an unstartable shell must not be reported as not-ready")
	}
}

// TestShellRunnerObservesOutput verifies stage output reaches the transcript
// hook, which is what the TUI renders.
func TestShellRunnerObservesOutput(t *testing.T) {
	var stage, out string
	r := ShellRunner{Observe: func(s, o string) { stage, out = s, o }}

	if _, err := runStage(t, r, sop.Stage{ID: "triage", Run: "echo hello"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage != "triage" || !strings.Contains(out, "hello") {
		t.Errorf("observed (%q, %q), want triage and output containing hello", stage, out)
	}
}

// TestStageNotReadyIgnoresOtherErrors guards the helper against reporting any
// error as a retry request.
func TestStageNotReadyIgnoresOtherErrors(t *testing.T) {
	if StageNotReady(errors.New("boom")) {
		t.Error("an unrelated error must not read as not-ready")
	}
	if StageNotReady(nil) {
		t.Error("nil must not read as not-ready")
	}
}
