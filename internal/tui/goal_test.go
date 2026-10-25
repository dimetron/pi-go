package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
)

// goalTestModel is a model with an armed goal and a tool controller, which is
// what every loop decision reads.
func goalTestModel(t *testing.T, status pisession.GoalStatus) *model {
	t.Helper()
	m := newTestModel(t)
	m.cfg.GoalTool = &tools.GoalController{}
	m.goal = &pisession.Goal{
		Objective: "keep the tests green",
		Status:    status,
		MaxCycles: 3,
		CreatedAt: time.Now().Unix(),
	}
	return m
}

func TestGoalIndicatorText(t *testing.T) {
	tests := []struct {
		name   string
		goal   *pisession.Goal
		cycles int
		want   string
	}{
		{name: "no goal", goal: nil, want: ""},
		{
			name: "active before the first cycle",
			goal: &pisession.Goal{Objective: "ship it", Status: pisession.GoalStatusActive, MaxCycles: 4},
			want: "goal: ship it",
		},
		{
			name:   "active mid-loop reports the cycle",
			goal:   &pisession.Goal{Objective: "ship it", Status: pisession.GoalStatusActive, MaxCycles: 4},
			cycles: 2,
			want:   "goal 2/4: ship it",
		},
		{
			name: "paused",
			goal: &pisession.Goal{Objective: "ship it", Status: pisession.GoalStatusPaused},
			want: "goal paused: ship it",
		},
		{
			name: "complete",
			goal: &pisession.Goal{Objective: "ship it", Status: pisession.GoalStatusComplete},
			want: "goal done: ship it",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			m.goal = tc.goal
			if tc.goal != nil {
				tc.goal.Cycles = tc.cycles
			}
			if got := m.goalIndicatorText(); got != tc.want {
				t.Errorf("goalIndicatorText() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A multi-line objective must not break the one-line status bar.
func TestGoalIndicatorText_CollapsesNewlines(t *testing.T) {
	m := newTestModel(t)
	m.goal = &pisession.Goal{
		Objective: "fix the build\nthen the linter",
		Status:    pisession.GoalStatusActive,
	}
	got := m.goalIndicatorText()
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("indicator contains a newline: %q", got)
	}
	if !strings.Contains(got, "fix the build then the linter") {
		t.Errorf("indicator = %q, want the objective on one line", got)
	}
}

func TestTruncatePlain(t *testing.T) {
	if got := truncatePlain("short", 10); got != "short" {
		t.Errorf("truncatePlain(short) = %q", got)
	}
	got := truncatePlain(strings.Repeat("a", 20), 5)
	if len([]rune(got)) != 6 { // 5 runes + ellipsis
		t.Errorf("truncatePlain(long) = %q, want 5 runes and an ellipsis", got)
	}
}

func TestSetGoal_RejectsEmptyAndOversized(t *testing.T) {
	for _, objective := range []string{"", "   ", strings.Repeat("x", pisession.MaxGoalObjectiveBytes+1)} {
		m := newTestModel(t)
		m.setGoal(objective)
		if m.goal != nil {
			t.Fatalf("setGoal(%d bytes) accepted an invalid objective", len(objective))
		}
		if len(m.chatModel.Messages) == 0 {
			t.Fatalf("setGoal(%d bytes) reported no reason to the user", len(objective))
		}
	}
}

func TestSetGoal_ReplacesAndResetsTheBudget(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.Cycles = 3

	m.setGoal("now do something else")

	if m.goal == nil || m.goal.Objective != "now do something else" {
		t.Fatalf("goal = %+v, want the new objective", m.goal)
	}
	if m.goal.Cycles != 0 {
		t.Errorf("Cycles = %d, want 0: a replaced goal starts a fresh budget", m.goal.Cycles)
	}
	if m.goalCanceled {
		t.Error("goalCanceled = true, want false: setting a goal re-arms the loop")
	}
	if got := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(got, "replaced") {
		t.Errorf("last message = %q, want it to report the replacement", got)
	}
}

func TestClearAndPauseGoal(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)

	m.pauseGoal()
	if m.goal.Status != pisession.GoalStatusPaused {
		t.Fatalf("Status = %q after pauseGoal, want paused", m.goal.Status)
	}
	if m.goalActive() {
		t.Error("goalActive() = true for a paused goal")
	}

	m.clearGoal()
	if m.goal != nil {
		t.Fatalf("goal = %+v after clearGoal, want nil", m.goal)
	}
	if !m.goalCanceled {
		t.Error("goalCanceled = false after clearGoal")
	}
}

// beginGoalCycle is the loop counter, so its arithmetic is the budget.
func TestBeginGoalCycle_CountsAndStopsAtTheBudget(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.MaxCycles = 2
	m.goal.Turn = 0

	// Two cycles are allowed; each queues one prompt and stamps the turn.
	for want := 1; want <= 2; want++ {
		if _, cmd := m.beginGoalCycle(); cmd == nil {
			t.Fatalf("cycle %d queued no prompt", want)
		}
		// Consume the queue so the next call is not refused as "already running"
		// for the wrong reason; the loop itself is driven by handleAgentDone.
		m.pendingPrompts = nil
		m.running = false
		if m.goal.Cycles != want {
			t.Fatalf("Cycles = %d, want %d", m.goal.Cycles, want)
		}
		if m.goal.Turn != want {
			t.Fatalf("Turn = %d, want %d", m.goal.Turn, want)
		}
	}

	// The third is refused, the goal stays active so it can be resumed, and the
	// user is told why the loop stopped.
	if _, cmd := m.beginGoalCycle(); cmd != nil {
		t.Fatal("beginGoalCycle queued a prompt past the cycle budget")
	}
	if m.goal.Status != pisession.GoalStatusActive {
		t.Errorf("Status = %q, want active so /goal resume can continue it", m.goal.Status)
	}
	if !m.goalCanceled {
		t.Error("goalCanceled = false, want true once the budget is spent")
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	if !strings.Contains(last, "not reached") {
		t.Errorf("last message = %q, want it to report the budget being spent", last)
	}
}

// A completion reported by the cycle that just ended closes the goal instead of
// starting another turn.
func TestBeginGoalCycle_ConsumesCompletion(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.Turn = 1
	m.goal.Cycles = 1
	m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1, MaxCycles: 3})
	if _, err := m.cfg.GoalTool.Complete(tools.GoalCompleteInput{Cycle: 1, Summary: "tests pass"}); err != nil {
		t.Fatalf("goal_complete: %v", err)
	}

	if _, cmd := m.beginGoalCycle(); cmd != nil {
		t.Fatal("beginGoalCycle queued a turn after the goal was reported complete")
	}
	if m.goal.Status != pisession.GoalStatusComplete {
		t.Fatalf("Status = %q, want complete", m.goal.Status)
	}
	if m.goal.CompletionNote != "tests pass" {
		t.Errorf("CompletionNote = %q, want the agent's summary", m.goal.CompletionNote)
	}
	if m.goal.Cycles != 1 {
		t.Errorf("Cycles = %d, want 1: completion must not spend a turn", m.goal.Cycles)
	}
}

func TestContinueGoalLoop_Refusals(t *testing.T) {
	t.Run("no goal", func(t *testing.T) {
		m := newTestModel(t)
		if _, cmd := m.continueGoalLoop(nil); cmd != nil {
			t.Fatal("continueGoalLoop started a turn with no goal")
		}
	})
	t.Run("paused goal", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusPaused)
		m.goal.Turn = 1
		if _, cmd := m.continueGoalLoop(nil); cmd != nil {
			t.Fatal("continueGoalLoop started a turn for a paused goal")
		}
	})
	t.Run("user stopped the loop", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.goal.Turn = 1
		m.goalCanceled = true
		if _, cmd := m.continueGoalLoop(nil); cmd != nil {
			t.Fatal("continueGoalLoop restarted a loop the user stopped")
		}
	})
	t.Run("steered turn", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.goal.Turn = 1
		m.steering = true
		if _, cmd := m.continueGoalLoop(nil); cmd != nil {
			t.Fatal("continueGoalLoop restarted a turn the user steered")
		}
	})
	t.Run("manual turn", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.goal.Turn = 0
		if _, cmd := m.continueGoalLoop(nil); cmd != nil {
			t.Fatal("continueGoalLoop treated a manual turn as a cycle")
		}
	})
}

// A cycle that failed still advances the loop: the spent budget stays spent and
// the next attempt is a new cycle, which is the recovery a provider outage needs.
func TestContinueGoalLoop_ContinuesPastAFailedTurn(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.MaxCycles = 3
	m.goal.Cycles = 1
	m.goal.Turn = 1

	if _, cmd := m.continueGoalLoop(errors.New("provider unreachable")); cmd == nil {
		t.Fatal("continueGoalLoop stopped after a failed turn")
	}
	if m.goal.Cycles != 2 {
		t.Errorf("Cycles = %d, want 2", m.goal.Cycles)
	}
}

func TestHandleGoalCommand(t *testing.T) {
	t.Run("bare with no goal", func(t *testing.T) {
		m := newTestModel(t)
		m.handleGoalCommand(nil)
		last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
		if !strings.Contains(last, "No goal is set") {
			t.Errorf("last message = %q, want the no-goal hint", last)
		}
	})
	t.Run("set then summary", func(t *testing.T) {
		m := newTestModel(t)
		m.handleGoalCommand([]string{"keep", "the", "tests", "green"})
		if m.goal == nil || m.goal.Objective != "keep the tests green" {
			t.Fatalf("goal = %+v, want the joined objective", m.goal)
		}
		m.handleGoalCommand(nil)
		last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
		if !strings.Contains(last, "keep the tests green") {
			t.Errorf("summary = %q, want the objective", last)
		}
	})
	t.Run("pause then resume", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.handleGoalCommand([]string{"pause"})
		if m.goal.Status != pisession.GoalStatusPaused {
			t.Fatalf("Status = %q after /goal pause", m.goal.Status)
		}
		m.handleGoalCommand([]string{"resume"})
		if m.goal.Status != pisession.GoalStatusActive {
			t.Fatalf("Status = %q after /goal resume", m.goal.Status)
		}
	})
	t.Run("clear", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.handleGoalCommand([]string{"clear"})
		if m.goal != nil {
			t.Fatalf("goal = %+v after /goal clear", m.goal)
		}
	})
	t.Run("unknown subcommand is an objective", func(t *testing.T) {
		m := newTestModel(t)
		m.handleGoalCommand([]string{"pause", "the", "flaky", "test"})
		if m.goal == nil || m.goal.Objective != "pause the flaky test" {
			t.Fatalf("goal = %+v, want the whole line as the objective", m.goal)
		}
	})
}

// Esc has to stop the loop, or the canceled turn's own done-handler restarts it.
func TestCancelAgent_StopsTheGoalLoop(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.Turn = 1
	m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1})

	m.cancelAgent()

	if !m.goalCanceled {
		t.Fatal("goalCanceled = false after Esc; the loop would restart")
	}
	// The tool must also stop accepting reports for a cycle that is not running.
	if got := m.cfg.GoalTool.GoalState; got.Active {
		t.Errorf("published state Active = true after cancel, want false")
	}
}

// TestHandleAgentDone_ContinuesTheGoalLoop drives the loop through the real turn
// end rather than calling continueGoalLoop directly.
//
// The difference matters: a test that calls the loop's decision function proves
// the function works, not that anything calls it. Deleting the hook from
// handleAgentDone — the actual wiring — leaves a direct-call test green and the
// feature dead, which is how seven compaction pipelines stayed no-ops for months.
func TestHandleAgentDone_ContinuesTheGoalLoop(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.cfg.WorkDir = t.TempDir()
	m.goal.MaxCycles = 3
	m.goal.Cycles = 1
	m.goal.Turn = 1
	m.running = true

	_, cmd := m.handleAgentDone(agentDoneMsg{})

	if m.goal.Cycles != 2 {
		t.Fatalf("Cycles = %d after a turn ended with the goal active, want 2: handleAgentDone did not continue the loop", m.goal.Cycles)
	}
	if cmd == nil {
		t.Error("handleAgentDone returned no command for the next cycle")
	}
}

// And the reverse: a completed goal must not be followed by another turn.
func TestHandleAgentDone_StopsWhenTheGoalIsReportedComplete(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.cfg.WorkDir = t.TempDir()
	m.goal.MaxCycles = 3
	m.goal.Cycles = 1
	m.goal.Turn = 1
	m.running = true
	m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1, MaxCycles: 3})
	if _, err := m.cfg.GoalTool.Complete(tools.GoalCompleteInput{Cycle: 1, Summary: "tests pass"}); err != nil {
		t.Fatal(err)
	}

	_, _ = m.handleAgentDone(agentDoneMsg{})

	if m.goal.Status != pisession.GoalStatusComplete {
		t.Fatalf("Status = %q after the turn reported completion, want complete", m.goal.Status)
	}
	if m.goal.Cycles != 1 {
		t.Errorf("Cycles = %d, want 1: the loop spent a turn closing a finished goal", m.goal.Cycles)
	}
	if len(m.pendingPrompts) != 0 {
		t.Errorf("pendingPrompts = %d, want 0", len(m.pendingPrompts))
	}
}

// /goal must be discoverable in /help, not only in autocomplete: the help table
// is hand-maintained, so a command missing from it is one users never find.
func TestFormatHelp_ListsGoal(t *testing.T) {
	m := newTestModel(t)
	help := m.formatHelp()
	for _, want := range []string{"Long-running tasks", "/goal <objective>", "/goal pause", "/goal resume"} {
		if !strings.Contains(help, want) {
			t.Errorf("formatHelp() is missing %q", want)
		}
	}
}

// The loop's own prompts must not rename the session or become the prompt /retry
// replays: eight cycles of "Continue pursuing the session goal" would overwrite
// both.
func TestSubmit_AutomaticPromptLeavesTitleAndRetryAlone(t *testing.T) {
	m := newTestModel(t)
	m.cfg.SessionID = "s1"
	m.lastPrompt = "fix the parser"
	m.sessionTitle = "fix the parser"

	m.submitQueuedPrompt(queuedPrompt{text: "Continue pursuing the session goal (cycle 2 of 8).", automatic: true})

	if m.sessionTitle != "fix the parser" {
		t.Errorf("sessionTitle = %q, want it unchanged by an automatic prompt", m.sessionTitle)
	}
	if m.lastPrompt != "fix the parser" {
		t.Errorf("lastPrompt = %q, want the user's prompt kept for /retry", m.lastPrompt)
	}
	// The prompt itself must still reach the transcript and the agent.
	if len(m.chatModel.Messages) == 0 || m.chatModel.Messages[0].role != "user" {
		t.Fatalf("automatic prompt was not added to the transcript: %+v", m.chatModel.Messages)
	}
	if !m.running {
		t.Error("running = false; the automatic prompt did not start a turn")
	}
}
