package tools

import (
	"fmt"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// GoalCompletedEventName is the name the goal sink receives. The TUI switches on
// it exactly as it does on the subagent event names emitted by the `subagent`
// tool, so both reach the UI through one channel and one handler.
const GoalCompletedEventName = "goal_complete"

// GoalCompletedEvent is what the `goal_complete` tool reports when the model says
// the session's objective is met.
//
// The tool cannot mark the goal complete itself: the goal lives in the session
// service, which the tool does not hold, and the same tool must work in modes
// with no TUI. It reports; the owner of the goal decides whether the report is
// credible — see GoalState.Check for the cycle and status checks that make it so.
type GoalCompletedEvent struct {
	// Cycle is the loop iteration the caller was told it was running, or 0 when
	// the model did not say. A report that cannot be attributed to the running
	// cycle is rejected rather than guessed at.
	Cycle int
	// Summary is the one-line account of what satisfied the objective.
	Summary string
}

// GoalState is the view of the session goal the agent is currently running
// under. The TUI republishes it whenever the goal changes, so the tool needs no
// access to session storage and no import of the TUI package.
type GoalState struct {
	// Active reports whether the goal is currently being pursued.
	Active bool
	// Turn is the cycle the agent is running. The prompt injected by
	// agent.AppendActiveGoal names this number, and the model echoes it back.
	Turn int
	// MaxCycles is the loop budget, for the rejection message.
	MaxCycles int
}

// GoalController is the mutable half of the goal_complete tool, shared between
// the tool handler (agent goroutine) and its owner (Update goroutine).
//
// It is a pointer so the tool and the owner that republishes state hold the same
// values rather than two copies. Both fields are set from outside while a turn
// may be running, hence the mutex.
type GoalController struct {
	mu sync.RWMutex
	// GoalState is the goal_complete tool's view of the running goal.
	GoalState GoalState
	// pending is the last accepted completion, waiting to be taken. It is a slot
	// rather than a callback because the reader is the turn's owner, which runs on
	// another goroutine: a callback here would fire mid-turn and the loop would
	// have to reason about whether the turn it just finished is the one that
	// reported. Draining at turn end makes that question unaskable.
	pending *GoalCompletedEvent
}

// SetGoalState republishes the goal the agent is running under. Safe to call
// from any goroutine, including while a turn is in flight.
func (s *GoalController) SetGoalState(state GoalState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.GoalState = state
}

// TakeCompletion returns the accepted completion recorded since the last call,
// and clears it. The bool is false when the tool has not reported one.
func (s *GoalController) TakeCompletion() (GoalCompletedEvent, bool) {
	if s == nil {
		return GoalCompletedEvent{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return GoalCompletedEvent{}, false
	}
	ev := *s.pending
	s.pending = nil
	return ev, true
}

// GoalCompleteInput defines the parameters for the goal_complete tool.
type GoalCompleteInput struct {
	// Cycle is the loop iteration the goal was satisfied in, as named by the
	// `# Current Goal` section of the system prompt.
	Cycle int `json:"cycle,omitempty"`
	// Summary is a one-line account of what satisfied the objective.
	Summary string `json:"summary,omitempty"`
}

// GoalCompleteOutput is what the model sees back.
type GoalCompleteOutput struct {
	// Acknowledged is true only when the report was accepted.
	Acknowledged bool `json:"acknowledged"`
	// Content is the message shown to the model.
	Content string `json:"content"`
}

// newGoalCompleteTool builds the tool the agent calls to declare the session goal
// met.
//
// state is supplied rather than created here: the owner that republishes the
// running cycle and receives the reports must hold the same value, so whoever
// builds the tool owns it and passes it in.
func newGoalCompleteTool(state *GoalController) (tool.Tool, error) {
	t, err := newTool("goal_complete",
		`Declare the session's goal met. Call this once, when the objective in the
"# Current Goal" section of the system prompt is fully satisfied and no part of it
remains. Pass the cycle number that section names and a one-line summary of what
satisfied the goal. This ends the automatic goal loop; a turn that finishes the
work without calling it is followed by another turn, so call it as soon as the
objective is met rather than waiting to be asked.`,
		func(_ agent.Context, input GoalCompleteInput) (GoalCompleteOutput, error) {
			return state.Complete(input)
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Complete validates a report against the goal the agent was actually running
// under and records an accepted one for the loop to collect.
//
// It is exported so the loop's tests can drive the same validation the tool runs,
// rather than a second copy that could accept what the tool rejects.
func (s *GoalController) Complete(input GoalCompleteInput) (GoalCompleteOutput, error) {
	s.mu.RLock()
	state := s.GoalState
	s.mu.RUnlock()

	if !state.Active {
		return GoalCompleteOutput{
			Acknowledged: false,
			Content:      "No goal is active for this session, so there is nothing to complete. Continue with the user's request.",
		}, nil
	}
	// A cycle of 0 means the model left it out. Accepting that would also accept
	// a report from a turn that started before the goal was set, so it is
	// treated as unattributed rather than as "the current one".
	if input.Cycle == 0 {
		return GoalCompleteOutput{
			Acknowledged: false,
			Content: fmt.Sprintf(
				"The cycle is required: pass the cycle number named in the \"# Current Goal\" section (this turn is cycle %d).",
				state.Turn),
		}, nil
	}
	if input.Cycle != state.Turn {
		return GoalCompleteOutput{
			Acknowledged: false,
			Content: fmt.Sprintf(
				"Cycle %d is not the running cycle, so this report cannot be attributed to the work it describes (this turn is cycle %d). If the goal is met, call again with cycle=%d.",
				input.Cycle, state.Turn, state.Turn),
		}, nil
	}

	// The checks above are the guardrail, and they are deliberately the whole of
	// it: the tool decides from published state alone whether a report is
	// attributable to the turn making it, so nothing here needs to reach into the
	// session or the TUI to find out. Ownership of the goal stays with the loop,
	// which reads this slot when the turn ends.
	s.mu.Lock()
	s.pending = &GoalCompletedEvent{Cycle: input.Cycle, Summary: input.Summary}
	s.mu.Unlock()

	summary := input.Summary
	if summary == "" {
		summary = "the objective is met"
	}
	return GoalCompleteOutput{
		Acknowledged: true,
		Content:      "Goal marked complete: " + summary + ". The goal loop has stopped; reply to the user with what you did.",
	}, nil
}
