package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"

	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
)

// TestGoalCompletionReachesTheController runs a REAL ADK agent against the real
// goal_complete tool and asserts the whole handshake: the model calls the tool
// with the cycle the prompt names, the tool accepts it, and the loop reads the
// report afterwards.
//
// The unit tests on either side would pass with the two halves wired to nothing.
// A controller that accepts a report in isolation says nothing about whether the
// registered tool closes over the same controller the TUI holds, and that is the
// exact class of defect the after-tool chain carried for months — every stage
// correct, the wiring dead.
func TestGoalCompletionReachesTheController(t *testing.T) {
	opt, ctrl := tools.WithGoal()
	ctrl.SetGoalState(tools.GoalState{Active: true, Turn: 2, MaxCycles: 5})

	ct, err := tools.CoreTools(testSandbox(t, t.TempDir()), opt)
	if err != nil {
		t.Fatal(err)
	}

	goal := &pisession.Goal{
		Objective: "keep the tests green",
		Status:    pisession.GoalStatusActive,
		MaxCycles: 5,
		Turn:      2,
	}

	llm := &toolCallingLLM{
		name: "goal-llm",
		functionCall: &genai.FunctionCall{
			ID:   "c1",
			Name: "goal_complete",
			Args: map[string]any{"cycle": 2, "summary": "all tests pass"},
		},
		finalText: "The goal is met.",
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       ct,
		Instruction: AppendActiveGoal("base", goal, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	sid, _, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var acked bool
	for ev, err := range a.Run(context.Background(), sid, "continue") {
		if err != nil {
			t.Fatal(err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "goal_complete" {
				if v, ok := p.FunctionResponse.Response["acknowledged"].(bool); ok {
					acked = v
				}
			}
		}
	}
	if !acked {
		t.Fatal("goal_complete was not acknowledged by the running tool")
	}

	completion, ok := ctrl.TakeCompletion()
	if !ok {
		t.Fatal("the TUI-side controller never saw the completion the tool accepted")
	}
	if completion.Cycle != 2 || completion.Summary != "all tests pass" {
		t.Errorf("completion = %+v, want cycle 2 / all tests pass", completion)
	}

	// And the prompt the model was given names the cycle the tool validates, so a
	// well-behaved model can satisfy the check rather than having to guess it.
	if got := AppendActiveGoal("base", goal, 2); !strings.Contains(got, "cycle=2") {
		t.Errorf("prompt does not name the cycle the tool validates: %q", got)
	}
}

// A report naming a cycle that is not the running one must not reach the loop:
// that is what stops a turn which started before the goal was set from closing it.
func TestGoalCompletionRejectsStaleCycle(t *testing.T) {
	opt, ctrl := tools.WithGoal()
	_ = opt
	ctrl.SetGoalState(tools.GoalState{Active: true, Turn: 3})

	out, err := ctrl.Complete(tools.GoalCompleteInput{Cycle: 2, Summary: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Acknowledged {
		t.Fatal("a report from cycle 2 was accepted while cycle 3 was running")
	}
	if _, ok := ctrl.TakeCompletion(); ok {
		t.Fatal("a rejected report was still recorded for the loop")
	}
}
