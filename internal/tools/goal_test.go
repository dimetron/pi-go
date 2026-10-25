package tools

import (
	"strings"
	"testing"
)

// A completion is only credible if it names the cycle that is running.
func TestGoalController_Complete(t *testing.T) {
	tests := []struct {
		name        string
		state       GoalState
		input       GoalCompleteInput
		wantAck     bool
		wantContent string
	}{
		{
			name:        "no active goal",
			state:       GoalState{Active: false, Turn: 1},
			input:       GoalCompleteInput{Cycle: 1, Summary: "done"},
			wantAck:     false,
			wantContent: "No goal is active",
		},
		{
			name:        "cycle omitted",
			state:       GoalState{Active: true, Turn: 2, MaxCycles: 5},
			input:       GoalCompleteInput{Summary: "done"},
			wantAck:     false,
			wantContent: "cycle is required",
		},
		{
			name:        "cycle belongs to another turn",
			state:       GoalState{Active: true, Turn: 2, MaxCycles: 5},
			input:       GoalCompleteInput{Cycle: 1, Summary: "done"},
			wantAck:     false,
			wantContent: "not the running cycle",
		},
		{
			name:        "cycle matches",
			state:       GoalState{Active: true, Turn: 2, MaxCycles: 5},
			input:       GoalCompleteInput{Cycle: 2, Summary: "tests pass"},
			wantAck:     true,
			wantContent: "tests pass",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &GoalController{}
			c.SetGoalState(tc.state)

			out, err := c.Complete(tc.input)
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if out.Acknowledged != tc.wantAck {
				t.Errorf("Acknowledged = %v, want %v (content %q)", out.Acknowledged, tc.wantAck, out.Content)
			}
			if !strings.Contains(out.Content, tc.wantContent) {
				t.Errorf("Content = %q, want it to mention %q", out.Content, tc.wantContent)
			}
			if _, ok := c.TakeCompletion(); ok != tc.wantAck {
				t.Errorf("TakeCompletion recorded = %v, want %v", ok, tc.wantAck)
			}
		})
	}
}

// TakeCompletion is a slot, not a queue: the loop reads it once per turn end.
func TestGoalController_TakeCompletionDrains(t *testing.T) {
	c := &GoalController{}
	c.SetGoalState(GoalState{Active: true, Turn: 1})
	if _, err := c.Complete(GoalCompleteInput{Cycle: 1, Summary: "done"}); err != nil {
		t.Fatal(err)
	}

	ev, ok := c.TakeCompletion()
	if !ok {
		t.Fatal("TakeCompletion() = false, want the recorded completion")
	}
	if ev.Cycle != 1 || ev.Summary != "done" {
		t.Errorf("event = %+v, want cycle 1 / done", ev)
	}
	if _, ok := c.TakeCompletion(); ok {
		t.Error("TakeCompletion returned the same completion twice")
	}
}

// The tool must be in the default set only when it is asked for: an agent offered
// goal_complete with no goal can only misuse it, and the default tool count is a
// contract the CLI's context accounting reads.
func TestCoreTools_GoalIsOptIn(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	base, err := CoreTools(sb)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range base {
		if tl.Name() == "goal_complete" {
			t.Fatal("goal_complete is registered without WithGoal")
		}
	}

	opt, ctl := WithGoal()
	if ctl == nil {
		t.Fatal("WithGoal returned a nil controller")
	}
	withGoal, err := CoreTools(sb, opt)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tl := range withGoal {
		if tl.Name() == "goal_complete" {
			found = true
		}
	}
	if !found {
		t.Fatal("goal_complete is not registered with WithGoal")
	}
	if len(withGoal) != len(base)+1 {
		t.Errorf("tool count = %d with the goal tool, %d without; want exactly one more",
			len(withGoal), len(base))
	}
}
