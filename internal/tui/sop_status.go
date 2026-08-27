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
