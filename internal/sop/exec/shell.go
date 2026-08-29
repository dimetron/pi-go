package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/validate"
)

// RetryExitCode is the exit status a stage script uses to say "not yet" rather
// than "failed".
//
// The distinction cannot be expressed by success and failure alone, and it is
// the difference between a poll and a defeat: a `watch` stage that finds CI
// still running has not failed, it simply has no answer yet, and what it wants
// is the engine's backoff — not the SOP's failure edge. The value is sysexits'
// EX_TEMPFAIL, which already means exactly this.
const RetryExitCode = 75

// errStageNotReady is returned for RetryExitCode. The engine applies the
// stage's RetryConfig to a returned error, so reporting "not ready" as an error
// is what makes retry/backoff do the waiting.
var errStageNotReady = errors.New("stage not ready")

// ShellRunner executes the stages a SOP declares as data.
//
// It is the piece that lets a SOP be written in YAML alone. A function stage
// runs its `run:` script and reports the route its exit status implies; an
// agent stage is handed to Agent, which the caller supplies because dispatching
// one needs the orchestrator and this package must not depend on it.
//
// Exit status is the contract:
//
//	0                 PASS — take the stage's PASS route, or its forward path
//	RetryExitCode     not ready — return an error so the engine backs off and
//	                  runs the stage again, which is how polling is expressed
//	anything else     FAIL — take the stage's FAIL route
type ShellRunner struct {
	// Dir is the working directory for stage scripts. Empty means the
	// process's own.
	Dir string

	// RunDir holds a run's artifacts. It is exported to scripts as
	// PI_RUN_DIR, and `produces:` paths resolve inside it.
	RunDir string

	// Env is extra environment for stage scripts, as "K=V" strings, on top of
	// the process environment.
	Env []string

	// Shell interprets a stage's script. Empty means "bash".
	Shell string

	// Agent runs an agent stage. When nil, an agent stage fails rather than
	// silently passing — a stage that quietly does nothing is the failure mode
	// the declarative SOP exists to prevent.
	Agent StageRunner

	// Observe receives each stage's combined output as it completes, for the
	// transcript. Optional.
	Observe func(stage, output string)
}

// RunStage implements StageRunner.
func (r ShellRunner) RunStage(ctx context.Context, req StageRequest) (StageOutcome, error) {
	if req.Stage.AgentName() != "" {
		if r.Agent == nil {
			return StageOutcome{}, fmt.Errorf("stage %q dispatches to agent %q but no agent runner is configured",
				req.Stage.ID, req.Stage.AgentName())
		}
		return r.Agent.RunStage(ctx, req)
	}

	// A function stage with no script is a marker — a join or a routing point.
	// It passes rather than failing, so a SOP can name a step it does not yet
	// automate.
	if strings.TrimSpace(req.Stage.Run) == "" && strings.TrimSpace(req.Stage.Gate) == "" {
		return StageOutcome{}, nil
	}

	if route, err := r.runScript(ctx, req, req.Stage.Run); err != nil || route != "" {
		return StageOutcome{Route: route}, err
	}

	// The gate runs after the body, and a failing gate fails the stage: that
	// ordering is what makes "verified" mean verified.
	if route, err := r.runScript(ctx, req, req.Stage.Gate); err != nil || route != "" {
		return StageOutcome{Route: route}, err
	}

	if findings := r.validateProduces(req.Stage); !findings.OK() {
		r.observe(req.Stage.ID, findings.Format())
		return StageOutcome{Route: sop.VerdictFail, Output: findings.Format()}, nil
	}
	return StageOutcome{Route: sop.VerdictPass}, nil
}

// runScript runs one script and maps its exit status onto a route. It returns
// an empty route when the script succeeded, so the caller can continue to the
// next step of the stage.
func (r ShellRunner) runScript(ctx context.Context, req StageRequest, script string) (string, error) {
	if strings.TrimSpace(script) == "" {
		return "", nil
	}

	shell := r.Shell
	if shell == "" {
		shell = "bash"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", script)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), r.stageEnv(req)...)

	out, err := cmd.CombinedOutput()
	r.observe(req.Stage.ID, string(out))
	if err == nil {
		return "", nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == RetryExitCode {
			return "", fmt.Errorf("%w: %s", errStageNotReady, req.Stage.ID)
		}
		return sop.VerdictFail, nil
	}
	// The script could not be started at all — a missing shell, a bad working
	// directory. That is not a stage verdict, it is a broken run.
	return "", fmt.Errorf("running stage %q: %w", req.Stage.ID, err)
}

// stageEnv exports the run's context to a stage script, so the SOP addresses it
// by name instead of having values interpolated into its text.
func (r ShellRunner) stageEnv(req StageRequest) []string {
	env := []string{
		"PI_STAGE=" + req.Stage.ID,
		"PI_CYCLE=" + strconv.Itoa(req.Cycle),
	}
	if r.RunDir != "" {
		env = append(env, "PI_RUN_DIR="+r.RunDir)
	}
	return append(env, r.Env...)
}

// validateProduces applies each declared artifact's rules. The rules are the
// same registry the plan artifacts use, so a SOP author has one vocabulary
// rather than two.
func (r ShellRunner) validateProduces(stage sop.Stage) validate.Findings {
	var out validate.Findings
	for _, p := range stage.Produces {
		path := p.Path
		if !filepath.IsAbs(path) && r.RunDir != "" {
			path = filepath.Join(r.RunDir, path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			out = append(out, validate.Finding{
				Artifact: p.Path, Rule: "produces", Severity: validate.SeverityError,
				Message: fmt.Sprintf("stage %q declares %s but it was not written: %v", stage.ID, p.Path, err),
			})
			continue
		}
		target := validate.Target{Artifact: p.Path, Content: string(content), RepoRoot: r.Dir}
		for _, spec := range p.Validate {
			out = append(out, validate.Apply(spec, target)...)
		}
	}
	return out
}

func (r ShellRunner) observe(stage, output string) {
	if r.Observe != nil && strings.TrimSpace(output) != "" {
		r.Observe(stage, output)
	}
}

// StageNotReady reports whether err is a stage asking to be run again.
func StageNotReady(err error) bool { return errors.Is(err, errStageNotReady) }
