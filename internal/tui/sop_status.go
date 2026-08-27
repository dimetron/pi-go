package tui

import (
	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/sop/exec"
)

// stageTracker turns the workflow engine's event stream into the status map the
// sidebar diagram draws.
//
// It exists because the engine does not offer the obvious thing. RunState lives
// on an unexported scheduler and is never handed to a caller, and node *start*
// is not an event at all. What is observable is the stream: our node bodies
// announce themselves on entry (exec.StageStarted), a paused node carries
// RequestedInput, and a failure arrives as an error. Completion is inferred —
// a node has finished once the next one begins, which the scheduler guarantees
// by only scheduling a successor after its predecessor completes.
type stageTracker struct {
	status  map[string]stageStatus
	current string
}

func newStageTracker() *stageTracker {
	return &stageTracker{status: map[string]stageStatus{}}
}

// observe folds one event into the tracker.
func (t *stageTracker) observe(ev *session.Event) {
	if ev == nil {
		return
	}

	if stage, ok := exec.StageStarted(ev); ok {
		// The previous stage is done: the scheduler does not start a successor
		// until its predecessor has completed. A stage the graph loops back to
		// is re-marked running below, which is what we want — it is running
		// again, not still finished.
		if t.current != "" && t.current != stage {
			t.status[t.current] = stageCompleted
		}
		t.status[stage] = stageRunning
		t.current = stage
		return
	}

	// A node asking for human input is parked, not working. This is the only
	// status the artifact-stat approach could never show.
	if ev.RequestedInput != nil && t.current != "" {
		t.status[t.current] = stageWaiting
	}
}

// fail marks the stage that was running as failed. The engine reports a node
// failure as an error on the stream rather than as an event, so the caller
// passes it in separately.
func (t *stageTracker) fail() {
	if t.current != "" {
		t.status[t.current] = stageFailed
	}
}

// finish marks the stage that was running as completed, for the end of a run
// that no successor follows.
func (t *stageTracker) finish() {
	if t.current != "" {
		t.status[t.current] = stageCompleted
		t.current = ""
	}
}

// statuses returns the map the renderer reads. The zero value of stageStatus is
// stageInactive, so stages never seen simply render as not started.
func (t *stageTracker) statuses() map[string]stageStatus {
	return t.status
}

// Until the workflow engine drives /plan and /run, the diagram takes its status
// from the same evidence the stage list uses: artifacts on disk for /plan, the
// run state machine for /run. The projections below are the bridge, and they
// exist so the list and the diagram can never disagree — they are two views of
// one fact, not two independent guesses. When the engine lands, stageTracker
// replaces both.

// planPhaseStage maps a PDD phase label to the plan SOP's stage id. "Idea" has
// no stage: it is the input to the SOP, not a step in it.
var planPhaseStage = map[string]string{
	"Requirements": "clarify",
	"Research":     "research",
	"Design":       "design",
	"Outline":      "outline",
	"Plan":         "plan",
	"Prompt":       "prompt",
}

// planStageStatus projects the phase checklist onto plan SOP stage ids: every
// finished phase is complete, and the first unfinished one is running.
func planStageStatus(phases []PlanPhase) map[string]stageStatus {
	out := make(map[string]stageStatus, len(phases))
	current := true
	for _, p := range phases {
		id, ok := planPhaseStage[p.Name]
		if !ok {
			continue
		}
		switch {
		case p.Done:
			out[id] = stageCompleted
		case current:
			out[id] = stageRunning
			current = false
		}
	}
	return out
}

// runPhaseStage maps a runState phase to the run SOP's stage id.
var runPhaseStage = map[string]string{
	"running":   "slices",
	"gating":    "gates",
	"verifying": "verify",
	"retrying":  "repair",
	"merging":   "merge",
	"done":      "summary",
	"failed":    "",
}

// runStageStatus projects the imperative run phase onto run SOP stage ids:
// everything before the current stage is complete, the current one is running,
// and a failed run marks where it stopped.
func runStageStatus(order []string, phase string) map[string]stageStatus {
	out := make(map[string]stageStatus, len(order))
	current, ok := runPhaseStage[phase]
	if !ok {
		return out
	}

	// A failed run has no stage of its own; the last one reached is the one
	// that failed, and we cannot tell which from the phase alone.
	if current == "" {
		return out
	}

	for _, id := range order {
		if id == current {
			out[id] = stageRunning
			if phase == "done" {
				out[id] = stageCompleted
			}
			break
		}
		// validate_spec always ran: the run would not have started otherwise.
		out[id] = stageCompleted
	}
	return out
}
