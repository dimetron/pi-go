package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
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

// /goal resume is the message both stop paths print, so it has to be the command
// that works. Esc and the cycle budget leave the goal Active and set goalCanceled,
// which a status-only check answers with "already active" — the loop then never
// restarts and the objective is unusable until it is retyped.
func TestResumeGoal_ReArmsAStoppedLoop(t *testing.T) {
	t.Run("after Esc", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.goal.Turn, m.goal.Cycles = 1, 1
		m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1})
		m.cancelAgent()

		if _, cmd := m.handleGoalCommand([]string{"resume"}); cmd == nil {
			t.Fatal("resume after Esc started no turn")
		}
		if m.goalCanceled {
			t.Error("goalCanceled still true after resume; the loop stays dead")
		}
	})

	t.Run("after the cycle budget", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		m.goal.MaxCycles = 1
		m.beginGoalCycle()
		m.pendingPrompts, m.running = nil, false
		m.beginGoalCycle() // refused: the single cycle is spent
		if !m.goalCanceled {
			t.Fatal("precondition: the budget should have stopped the loop")
		}

		if _, cmd := m.handleGoalCommand([]string{"resume"}); cmd == nil {
			t.Fatal("resume after a spent budget started no turn")
		}
		// Topping the budget up is what makes the turn runnable at all: with
		// MaxCycles left at Cycles, beginGoalCycle refuses again immediately.
		if m.goal.MaxCycles <= m.goal.Cycles {
			t.Errorf("MaxCycles = %d with Cycles = %d: resume left no room to run",
				m.goal.MaxCycles, m.goal.Cycles)
		}
	})

	t.Run("a healthy active goal is a no-op", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		if _, cmd := m.handleGoalCommand([]string{"resume"}); cmd != nil {
			t.Error("resume on a running goal started a second turn")
		}
	})
}

// Complete is terminal: a finished objective that one command can restart is a
// loop waiting to happen, which the PR advertises as a guarantee.
func TestCompletedGoalIsTerminal(t *testing.T) {
	for _, cmd := range [][]string{{"resume"}, {"pause"}} {
		m := goalTestModel(t, pisession.GoalStatusComplete)
		m.goal.Cycles = 4

		if _, teaCmd := m.handleGoalCommand(cmd); teaCmd != nil {
			t.Errorf("/goal %s on a completed goal started a turn", cmd[0])
		}
		if m.goal.Status != pisession.GoalStatusComplete {
			t.Errorf("/goal %s changed a completed goal to %q", cmd[0], m.goal.Status)
		}
	}
}

// A pending report belongs to the objective whose turn produced it. If the turn is
// stopped before turn-end drains it, the report must not close a goal the user set
// afterwards.
func TestStaleCompletionDoesNotCrossObjectives(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.Cycles, m.goal.Turn = 1, 1
	m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1})
	if _, err := m.cfg.GoalTool.Complete(tools.GoalCompleteInput{Cycle: 1, Summary: "old work"}); err != nil {
		t.Fatalf("goal_complete: %v", err)
	}

	// Esc before the turn ends, then set a different objective.
	m.cancelAgent()
	m.handleGoalCommand([]string{"a", "new", "objective"})

	m.beginGoalCycle()
	if m.goal.Status == pisession.GoalStatusComplete {
		t.Fatalf("the previous objective's report closed the new goal: note %q", m.goal.CompletionNote)
	}
}

// /goal pause has to stop the loop at the turn boundary. Relabelling the status is
// not enough: continueGoalLoop reads goalCanceled, so the loop would start another
// cycle past the pause.
func TestPauseStopsTheLoopAtTheTurnBoundary(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.Cycles, m.goal.Turn = 1, 1
	m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1})

	m.handleGoalCommand([]string{"pause"})

	if !m.goalCanceled {
		t.Fatal("pause left goalCanceled false; handleAgentDone would start another cycle")
	}
	m.running = false
	if _, cmd := m.handleAgentDone(agentDoneMsg{}); cmd != nil {
		t.Error("handleAgentDone started a cycle after /goal pause")
	}
}

// The loop starts its next cycle from handleAgentDone, before the event loop reads
// another keystroke, so pause and clear have to be dispatchable while a turn runs.
func TestGoalControlCommandsReachTheRunningLoop(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/goal pause", true},
		{"/goal clear", true},
		{"/GOAL PAUSE", true},
		{"/goal", false},
		{"/goal resume", false},
		{"/goal keep the tests green", false},
		{"/goal pause the flaky test", false},
		{"/clear", false},
	}
	for _, tc := range tests {
		if got := isGoalControlCommand(tc.input); got != tc.want {
			t.Errorf("isGoalControlCommand(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}

	// And the dispatch actually lets one through while running.
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.running = true
	m.handleInputSubmit(InputSubmitMsg{Text: "/goal pause"})
	if m.goal.Status != pisession.GoalStatusPaused {
		t.Errorf("Status = %q after /goal pause during a turn, want paused", m.goal.Status)
	}
}

// PI_GOAL_MAX_CYCLES outranks the recorded budget, so a resume that only raised
// MaxCycles would be a no-op under the override: every attempt stops again at the
// environment limit.
func TestResumeRenewsTheBudgetUnderAnEnvOverride(t *testing.T) {
	t.Setenv("PI_GOAL_MAX_CYCLES", "2")
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.MaxCycles = 2
	m.goal.Cycles, m.goal.Turn = 2, 2
	m.goalCanceled = true

	if _, cmd := m.handleGoalCommand([]string{"resume"}); cmd == nil {
		t.Fatalf("resume under PI_GOAL_MAX_CYCLES=2 queued no turn: Cycles=%d MaxCycles=%d effective=%d",
			m.goal.Cycles, m.goal.MaxCycles, goalMaxCycles(m.goal.MaxCycles))
	}
	if got := goalMaxCycles(m.goal.MaxCycles); got <= m.goal.Cycles-1 {
		t.Errorf("effective budget = %d with Cycles = %d: the renewed goal cannot run", got, m.goal.Cycles)
	}
}

// --system replaces the built-in instruction. Rebuilding the goal prompt from
// agent.SystemInstruction instead of the configured base silently discards it.
func TestGoalPromptKeepsTheConfiguredInstruction(t *testing.T) {
	// A real agent, because the assertion is about what RebuildWithInstruction
	// actually installs — a stub would only prove the stub was called.
	a, err := agent.New(agent.Config{
		Model:       &goalLoopLLM{name: "instruction"},
		Instruction: "CUSTOM SYSTEM PROMPT",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	m := newTestModel(t)
	m.cfg.Agent = a
	m.cfg.GoalTool = &tools.GoalController{}
	m.cfg.BaseInstruction = "CUSTOM SYSTEM PROMPT"
	m.goal = &pisession.Goal{Objective: "ship it", Status: pisession.GoalStatusActive, MaxCycles: 3, Cycles: 1, Turn: 1}

	m.syncGoalPrompt()

	got := m.cfg.Agent.Instruction()
	if !strings.Contains(got, "CUSTOM SYSTEM PROMPT") {
		t.Errorf("the configured instruction was discarded; prompt = %q", got)
	}
	if !strings.Contains(got, "Current Goal") {
		t.Errorf("the goal section was not appended; prompt = %q", got)
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

// --- Branch coverage: the paths a working loop takes only when something goes
// wrong, plus the sub-command refusals. They are easy to leave untested because
// no happy-path run reaches them, and they are exactly the paths a user hits when
// they mistype a command or the session service is missing.

func TestGoalMaxCycles(t *testing.T) {
	t.Setenv("PI_GOAL_MAX_CYCLES", "")
	if got := goalMaxCycles(0); got != defaultGoalMaxCycles {
		t.Errorf("goalMaxCycles(0) = %d, want the default %d", got, defaultGoalMaxCycles)
	}
	if got := goalMaxCycles(5); got != 5 {
		t.Errorf("goalMaxCycles(5) = %d, want the recorded 5", got)
	}
	// Larger wins, so an operator can raise the budget mid-session and a resume that
	// renewed a spent budget is not undone by a smaller override.
	t.Setenv("PI_GOAL_MAX_CYCLES", "12")
	if got := goalMaxCycles(5); got != 12 {
		t.Errorf("goalMaxCycles(5) with PI_GOAL_MAX_CYCLES=12 = %d, want 12", got)
	}
	if got := goalMaxCycles(30); got != 30 {
		t.Errorf("goalMaxCycles(30) with PI_GOAL_MAX_CYCLES=12 = %d, want the recorded 30", got)
	}
	// Garbage and non-positive values fall through rather than disabling the loop.
	for _, bad := range []string{"abc", "0", "-3", "  "} {
		t.Setenv("PI_GOAL_MAX_CYCLES", bad)
		if got := goalMaxCycles(4); got != 4 {
			t.Errorf("goalMaxCycles(4) with PI_GOAL_MAX_CYCLES=%q = %d, want 4", bad, got)
		}
	}
}

func TestGoalSubcommandRefusals(t *testing.T) {
	t.Run("clear with no goal", func(t *testing.T) {
		m := newTestModel(t)
		m.clearGoal()
		assertLastMeta(t, m, "No goal is set")
	})
	t.Run("pause with no goal", func(t *testing.T) {
		m := newTestModel(t)
		m.pauseGoal()
		assertLastMeta(t, m, "No goal is set")
	})
	t.Run("pause an already paused goal", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusPaused)
		m.pauseGoal()
		assertLastMeta(t, m, "already paused")
	})
	t.Run("resume with no goal", func(t *testing.T) {
		m := newTestModel(t)
		if _, cmd := m.resumeGoal(); cmd != nil {
			t.Error("resumeGoal started a turn with no goal")
		}
		assertLastMeta(t, m, "No goal is set")
	})
	t.Run("resume an active goal", func(t *testing.T) {
		m := goalTestModel(t, pisession.GoalStatusActive)
		if _, cmd := m.resumeGoal(); cmd != nil {
			t.Error("resumeGoal started a second turn for an already-active goal")
		}
		assertLastMeta(t, m, "already active")
	})
	t.Run("/goal help", func(t *testing.T) {
		m := newTestModel(t)
		m.handleGoalCommand([]string{"help"})
		assertLastMeta(t, m, "Usage: /goal")
	})
}

// Every status has a summary card, and a completed goal with no recorded note
// must say so rather than printing an empty line.
func TestPrintGoalSummary_AllStatuses(t *testing.T) {
	for _, tc := range []struct {
		status pisession.GoalStatus
		want   string
	}{
		{pisession.GoalStatusActive, "Goal (cycle"},
		{pisession.GoalStatusPaused, "Goal paused"},
		{pisession.GoalStatusComplete, "Goal complete"},
	} {
		m := goalTestModel(t, tc.status)
		m.goal.Cycles = 2
		m.printGoalSummary()
		assertLastMeta(t, m, tc.want)
	}

	m := goalTestModel(t, pisession.GoalStatusComplete)
	m.printGoalSummary()
	assertLastMeta(t, m, "no summary recorded")
}

// A prompt queue that is already full must stop the loop rather than drop the
// cycle's prompt: a spent cycle with no turn behind it is a budget leak.
func TestBeginGoalCycle_FullQueueStops(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.MaxCycles = 5
	for i := 0; i < maxPendingPrompts; i++ {
		m.pendingPrompts = append(m.pendingPrompts, queuedPrompt{text: "user"})
	}

	if _, cmd := m.beginGoalCycle(); cmd != nil {
		t.Fatal("beginGoalCycle started a turn with a full queue")
	}
	if !m.goalCanceled {
		t.Error("goalCanceled = false after the queue filled; the loop would retry forever")
	}
	assertLastMeta(t, m, "queue is full")
}

// A goal whose cycle reports no summary still needs a note: an empty completion
// note would render as a blank line under "Goal complete".
func TestBeginGoalCycle_EmptySummaryGetsADefaultNote(t *testing.T) {
	m := goalTestModel(t, pisession.GoalStatusActive)
	m.goal.Turn = 1
	m.cfg.GoalTool.SetGoalState(tools.GoalState{Active: true, Turn: 1})
	if _, err := m.cfg.GoalTool.Complete(tools.GoalCompleteInput{Cycle: 1}); err != nil {
		t.Fatal(err)
	}

	m.beginGoalCycle()

	if m.goal.CompletionNote == "" {
		t.Fatal("CompletionNote is empty for a report with no summary")
	}
	if m.goal.Status != pisession.GoalStatusComplete {
		t.Errorf("Status = %q, want complete", m.goal.Status)
	}
}

// A nil tool or controller must be a no-op, not a panic: print/JSON modes and the
// TUI's own unit tests run without one.
func TestGoalToolNilSafety(t *testing.T) {
	m := newTestModel(t)
	m.goal = &pisession.Goal{Objective: "x", Status: pisession.GoalStatusActive}

	m.publishGoalState() // must not panic with cfg.GoalTool == nil
	if _, ok := m.takeGoalCompletion(); ok {
		t.Error("takeGoalCompletion reported a completion with no controller")
	}

	var ctl *tools.GoalController
	ctl.SetGoalState(tools.GoalState{Active: true})
	if _, ok := ctl.TakeCompletion(); ok {
		t.Error("TakeCompletion on a nil controller reported a completion")
	}
}

// assertLastMeta checks the most recent transcript entry is a meta note
// containing want. Meta notes are how every /goal outcome reaches the user, so
// an outcome that changes state silently fails here.
func assertLastMeta(t *testing.T, m *model, want string) {
	t.Helper()
	if len(m.chatModel.Messages) == 0 {
		t.Fatalf("no messages; wanted a meta note containing %q", want)
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !strings.Contains(last.content, want) {
		t.Errorf("last message = %q, want it to contain %q", last.content, want)
	}
}

// --- restoreGoal: the /goal half of --continue. Everything below is a resumed
// session, which is the one path a normal interactive run never exercises.

// newGoalServiceModel builds a model backed by a real FileService with one
// session, which is what restoreGoal reads.
func newGoalServiceModel(t *testing.T) (*model, *pisession.FileService, string) {
	t.Helper()
	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := svc.Create(ctx, newCreateReq("goal-resume"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t)
	m.cfg.SessionService = svc
	m.cfg.SessionID = created.Session.ID()
	return m, svc, m.cfg.SessionID
}

func TestRestoreGoal_EachStatus(t *testing.T) {
	tests := []struct {
		name       string
		stored     *pisession.Goal
		wantStatus pisession.GoalStatus
		wantNote   string
	}{
		{
			name: "active goal is armed but not restarted",
			stored: &pisession.Goal{
				Objective: "keep tests green", Status: pisession.GoalStatusActive,
				Cycles: 3, MaxCycles: 8,
			},
			wantStatus: pisession.GoalStatusActive,
			wantNote:   "Loaded goal from cycle 3 of 8",
		},
		{
			name: "paused goal",
			stored: &pisession.Goal{
				Objective: "keep tests green", Status: pisession.GoalStatusPaused,
			},
			wantStatus: pisession.GoalStatusPaused,
			wantNote:   "paused goal",
		},
		{
			name: "completed goal",
			stored: &pisession.Goal{
				Objective: "keep tests green", Status: pisession.GoalStatusComplete,
			},
			wantStatus: pisession.GoalStatusComplete,
			wantNote:   "completed goal",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, svc, sid := newGoalServiceModel(t)
			if err := svc.UpdateGoal(sid, tc.stored); err != nil {
				t.Fatal(err)
			}

			m.restoreGoal()

			if m.goal == nil {
				t.Fatal("restoreGoal left the goal nil")
			}
			if m.goal.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", m.goal.Status, tc.wantStatus)
			}
			// A resume is the user returning to a session, not deciding to spend
			// another turn on it: the loop must be armed, not running.
			if !m.goalCanceled {
				t.Error("goalCanceled = false; resuming a session restarted the loop on its own")
			}
			assertLastMeta(t, m, tc.wantNote)
		})
	}
}

// No stored goal, no session service, no session id: each is a silent no-op, and
// none may panic — --continue on a session that never used /goal is the common case.
func TestRestoreGoal_NoOps(t *testing.T) {
	t.Run("no stored goal", func(t *testing.T) {
		m, _, _ := newGoalServiceModel(t)
		m.restoreGoal()
		if m.goal != nil {
			t.Errorf("goal = %+v, want nil", m.goal)
		}
		if len(m.chatModel.Messages) != 0 {
			t.Errorf("messages = %v, want none", m.chatModel.Messages)
		}
	})
	t.Run("no session service", func(t *testing.T) {
		m := newTestModel(t)
		m.restoreGoal() // must not panic
		if m.goal != nil {
			t.Errorf("goal = %+v, want nil", m.goal)
		}
	})
	t.Run("no session id", func(t *testing.T) {
		m, svc, _ := newGoalServiceModel(t)
		m.cfg.SessionID = ""
		_ = svc
		m.restoreGoal() // must not panic
		if m.goal != nil {
			t.Errorf("goal = %+v, want nil", m.goal)
		}
	})
}

// --- Remaining branches, all cheap and all reachable in normal use.

func TestGoalTurnDefaults(t *testing.T) {
	m := newTestModel(t)
	if got := m.goalTurn(); got != 0 {
		t.Errorf("goalTurn() with no goal = %d, want 0", got)
	}
	// A status written by a newer build still renders, under the generic heading
	// rather than a half-empty indicator.
	m.goal = &pisession.Goal{Objective: "x", Status: pisession.GoalStatus("future")}
	if got := plain(sidebarGoalLines(
		SidebarRenderInput{Goal: m.goal}, 27, testSidebarStyles())); !strings.Contains(got, "Goal") {
		t.Errorf("sidebarGoalLines() for an unknown status = %q, want the generic heading", got)
	}
}

// /goal while a turn is already running must set the goal without starting a
// competing turn.
func TestHandleGoalCommand_WhileRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true

	if _, cmd := m.handleGoalCommand([]string{"ship", "it"}); cmd != nil {
		t.Error("handleGoalCommand started a turn while one was already running")
	}
	if m.goal == nil || m.goal.Objective != "ship it" {
		t.Fatalf("goal = %+v, want it set anyway", m.goal)
	}
	if m.goal.Cycles != 0 {
		t.Errorf("Cycles = %d, want 0: no cycle was started", m.goal.Cycles)
	}
}

// /goal <invalid> while idle must report the error and start nothing.
func TestHandleGoalCommand_InvalidObjectiveStartsNothing(t *testing.T) {
	m := newTestModel(t)
	if _, cmd := m.handleGoalCommand([]string{strings.Repeat("x", pisession.MaxGoalObjectiveBytes+1)}); cmd != nil {
		t.Error("an over-length objective started a turn")
	}
	if m.goal != nil {
		t.Fatalf("goal = %+v, want nil", m.goal)
	}
	assertLastMeta(t, m, "at most")
}
