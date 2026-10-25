package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"

	tea "charm.land/bubbletea/v2"
)

// defaultGoalMaxCycles is the number of automatic turns /goal grants a goal
// before it stops and asks the user to continue.
//
// The number is a budget, not a safety net: the runaway guards inside a turn
// (maxStuckRecoveries, the repeat and output detectors) stop one turn that goes
// in circles, but nothing in them notices a sequence of distinct, plausible
// turns that never reaches the objective. That is the failure this cap is for,
// and each cycle is a full billed agent turn, so the default is small enough to
// notice and large enough to finish real work.
const defaultGoalMaxCycles = 8

// goalMaxCycles resolves the loop budget: PI_GOAL_MAX_CYCLES, then the value
// recorded when the goal was set, then the default. Same shape as
// planMaxFixCycles, so an operator who knows one knows the other.
func goalMaxCycles(recorded int) int {
	if v := strings.TrimSpace(os.Getenv("PI_GOAL_MAX_CYCLES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if recorded > 0 {
		return recorded
	}
	return defaultGoalMaxCycles
}

// goalActive reports whether a goal is currently being pursued.
func (m *model) goalActive() bool {
	return m.goal != nil && m.goal.Status == pisession.GoalStatusActive
}

// setGoal starts a goal loop for objective.
//
// A new objective always starts a fresh budget. The counter bounds one objective
// pursued; carrying it over would let a replaced goal inherit the previous one's
// spent turns and stop immediately, which reads as the feature being broken.
func (m *model) setGoal(objective string) {
	objective = strings.TrimSpace(objective)
	if err := agent.ValidateGoalObjective(objective); err != nil {
		m.chatModel.AppendMeta("Goal not set: " + err.Error())
		return
	}

	now := time.Now().Unix()
	prevCreated := int64(0)
	if m.goal != nil {
		prevCreated = m.goal.CreatedAt
	}
	m.goal = &pisession.Goal{
		Objective: objective,
		Status:    pisession.GoalStatusActive,
		MaxCycles: goalMaxCycles(0),
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.goalCanceled = false
	if prevCreated == 0 {
		m.chatModel.AppendMeta("Goal set: " + objective)
	} else {
		m.chatModel.AppendMeta("Goal replaced: " + objective)
	}
	m.persistGoal()
	m.syncGoalPrompt()
}

// persistGoal writes the goal to session metadata, reporting a failure in the
// transcript rather than dropping it: the in-memory goal still drives this
// session, and the only thing lost is the ability to resume it.
func (m *model) persistGoal() {
	svc := m.cfg.SessionService
	if svc == nil || m.cfg.SessionID == "" {
		return
	}
	if err := svc.UpdateGoal(m.cfg.SessionID, m.goal); err != nil {
		m.cfg.Logger.Errorf("persisting goal: %v", err)
		m.chatModel.AppendMeta("Goal kept for this session only: " + err.Error())
	}
}

// syncGoalPrompt rebuilds the agent instruction so the running turn's prompt
// describes the goal as it stands now.
//
// The cycle passed in is the goal's current Turn: a goal that is only just being
// started has Turn 0, and AppendActiveGoal then omits the cycle number, because a
// turn that has not begun cannot be named. beginGoalCycle stamps the real one
// before the turn's prompt is built.
func (m *model) syncGoalPrompt() {
	if m.cfg.Agent == nil {
		return
	}
	base := agent.LoadInstruction(agent.SystemInstruction)
	if err := m.cfg.Agent.RebuildWithInstruction(
		agent.AppendActiveGoal(base, m.goal, m.goalTurn()),
	); err != nil {
		m.cfg.Logger.Errorf("applying goal to system prompt: %v", err)
		m.chatModel.AppendMeta("Goal saved, but the system prompt was not updated: " + err.Error())
	}
}

// goalTurn is the cycle the agent is currently running, or 0 when no turn of a
// goal loop is in flight.
func (m *model) goalTurn() int {
	if m.goal == nil {
		return 0
	}
	return m.goal.Turn
}

// clearGoal drops the goal and stops the loop.
func (m *model) clearGoal() {
	if m.goal == nil {
		m.chatModel.AppendMeta("No goal is set.")
		return
	}
	m.goal = nil
	m.goalCanceled = true
	m.chatModel.AppendMeta("Goal cleared.")
	m.persistGoal()
	m.syncGoalPrompt()
}

// pauseGoal stops the loop without losing the objective.
func (m *model) pauseGoal() {
	switch {
	case m.goal == nil:
		m.chatModel.AppendMeta("No goal is set.")
		return
	case m.goal.Status == pisession.GoalStatusPaused:
		m.chatModel.AppendMeta("Goal already paused.")
		return
	}
	m.goal.Status = pisession.GoalStatusPaused
	m.goal.UpdatedAt = time.Now().Unix()
	m.chatModel.AppendMeta("Goal paused. /goal resume to continue pursuing it.")
	m.persistGoal()
	m.syncGoalPrompt()
}

// resumeGoal puts a paused goal back into the loop and starts a turn if the UI is
// idle, so resuming is one keystroke rather than a command plus a prompt.
func (m *model) resumeGoal() (tea.Model, tea.Cmd) {
	switch {
	case m.goal == nil:
		m.chatModel.AppendMeta("No goal is set. /goal <objective> to set one.")
		return m, nil
	case m.goal.Status == pisession.GoalStatusActive:
		m.chatModel.AppendMeta("Goal already active.")
		return m, nil
	}
	m.goal.Status = pisession.GoalStatusActive
	m.goal.UpdatedAt = time.Now().Unix()
	// Resuming is a decision to keep going, so a loop stopped by Esc or by the
	// cycle budget is re-armed here. The budget is left as spent: /goal set is
	// the way to ask for more.
	m.goalCanceled = false
	m.chatModel.AppendMeta("Goal resumed.")
	m.persistGoal()
	m.syncGoalPrompt()
	return m.beginGoalCycle()
}

// beginGoalCycle prepares the next automatic turn.
//
// It is called from handleAgentDone, on the Update goroutine, after the previous
// turn has reported. The cycle number is spent before the prompt is built so the
// prompt and the tool's published state agree on which turn is running.
func (m *model) beginGoalCycle() (tea.Model, tea.Cmd) {
	if !m.goalActive() || m.goalCanceled {
		return m, nil
	}

	// Drain the tool's report first: the cycle that just ended is the one a
	// completion can be attributed to, and consuming it here — rather than when
	// the tool call returns — means a report cannot close the goal and then be
	// overwritten by the loop's own bookkeeping.
	if ev, ok := m.takeGoalCompletion(); ok {
		note := ev.Summary
		if note == "" {
			note = "the objective is met"
		}
		m.goal.Status = pisession.GoalStatusComplete
		m.goal.CompletedAt = time.Now().Unix()
		m.goal.UpdatedAt = m.goal.CompletedAt
		m.goal.CompletionNote = note
		m.chatModel.AppendMeta(fmt.Sprintf("Goal complete after %d cycle(s): %s", m.goal.Cycles, note))
		m.persistGoal()
		m.syncGoalPrompt()
		return m, nil
	}

	maxCycles := goalMaxCycles(m.goal.MaxCycles)
	if m.goal.Cycles >= maxCycles {
		// Stop and report. The objective is left active so /goal resume
		// continues it rather than making the user retype it.
		m.chatModel.AppendMeta(fmt.Sprintf(
			"Goal not reached after %d cycle(s); stopping. /goal resume for more, or /goal clear.",
			m.goal.Cycles))
		m.goalCanceled = true
		m.persistGoal()
		return m, nil
	}

	m.goal.Cycles++
	m.goal.Turn = m.goal.Cycles
	m.goal.UpdatedAt = time.Now().Unix()
	m.persistGoal()
	// Rebuild before submitting: the prompt for this cycle has to carry this
	// cycle's number, or the completion the turn reports will be rejected as
	// belonging to a turn that is not running.
	m.syncGoalPrompt()
	m.publishGoalState()

	m.chatModel.AppendMeta(fmt.Sprintf("Goal cycle %d of %d.", m.goal.Cycles, maxCycles))
	// Queued directly rather than through enqueuePrompt: this prompt is the loop's
	// own, and enqueuePrompt's contract is about prompts the user typed.
	if len(m.pendingPrompts) >= maxPendingPrompts {
		m.chatModel.AppendMeta("Goal loop paused: the prompt queue is full.")
		m.goalCanceled = true
		m.persistGoal()
		return m, nil
	}
	m.pendingPrompts = append(m.pendingPrompts, queuedPrompt{
		text:      goalCyclePrompt(m.goal, maxCycles),
		automatic: true,
	})
	return m.startNextPrompt()
}

// goalCyclePrompt is the user-role prompt that opens each automatic cycle.
//
// It is a user message because the agent's turn model has no other entry point,
// and it says plainly that no human typed it: a prompt that impersonated the user
// would put words in their mouth in the transcript they later read back.
func goalCyclePrompt(goal *pisession.Goal, maxCycles int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Continue pursuing the session goal (cycle %d of %d).\n\n", goal.Cycles, maxCycles)
	b.WriteString("Goal: ")
	b.WriteString(goal.Objective)
	b.WriteString("\n\n")
	b.WriteString("This turn was started automatically because the previous one ended with the goal still active. ")
	b.WriteString("Do not restate the goal or ask what to do next — do the remaining work. ")
	b.WriteString("When the whole objective is met, call goal_complete with ")
	fmt.Fprintf(&b, "cycle=%d", goal.Cycles)
	b.WriteString(" and a one-line summary; if it is not met, say what is left and stop.")
	return b.String()
}

// takeGoalCompletion drains the completion slot the goal_complete tool writes.
func (m *model) takeGoalCompletion() (tools.GoalCompletedEvent, bool) {
	if m.cfg.GoalTool == nil {
		return tools.GoalCompletedEvent{}, false
	}
	return m.cfg.GoalTool.TakeCompletion()
}

// publishGoalState tells the goal_complete tool which turn is running and whether
// a goal is being pursued at all, so the tool can reject a report that belongs to
// a turn which is not the one making it.
func (m *model) publishGoalState() {
	if m.cfg.GoalTool == nil {
		return
	}
	maxCycles := 0
	if m.goal != nil {
		maxCycles = goalMaxCycles(m.goal.MaxCycles)
	}
	// A canceled loop publishes Active=false even though the goal is still
	// active: the tool must reject a report from the turn that is no longer
	// running, or a late completion would close a goal the user just stopped.
	m.cfg.GoalTool.SetGoalState(tools.GoalState{
		Active:    m.goalActive() && !m.goalCanceled,
		Turn:      m.goalTurn(),
		MaxCycles: maxCycles,
	})
}

// goalIndicatorText is the status-bar fragment for the goal loop, empty when no
// goal is set. It reports the cycle as well as the objective because a loop that
// is spending turns is something the user needs to see while it happens, not
// afterwards in the transcript.
func (m *model) goalIndicatorText() string {
	if m.goal == nil {
		return ""
	}
	objective := truncatePlain(m.goal.Objective, 32)
	switch m.goal.Status {
	case pisession.GoalStatusActive:
		maxCycles := goalMaxCycles(m.goal.MaxCycles)
		if m.goal.Cycles > 0 {
			return fmt.Sprintf("goal %d/%d: %s", m.goal.Cycles, maxCycles, objective)
		}
		return "goal: " + objective
	case pisession.GoalStatusPaused:
		return "goal paused: " + objective
	case pisession.GoalStatusComplete:
		return "goal done: " + objective
	}
	return ""
}

// truncatePlain shortens s to at most n runes plus an ellipsis, collapsing
// newlines so a multi-line objective cannot break the one-line status bar.
func truncatePlain(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// handleGoalCommand dispatches /goal.
//
// The bare form is a status card rather than usage text: once a goal is set, the
// question a user has is what it is and how far the loop has got, and printing a
// synopsis of sub-commands in reply is how a command that works still feels
// broken.
func (m *model) handleGoalCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.printGoalSummary()
		return m, nil
	}

	// A sub-command only when it is the whole line. "pause" is a common first
	// word in an objective ("pause the flaky test"), and swallowing the rest of
	// the line on the strength of one keyword would silently set the wrong goal.
	if len(args) == 1 {
		switch strings.ToLower(args[0]) {
		case "clear":
			m.clearGoal()
			return m, nil
		case "pause":
			m.pauseGoal()
			return m, nil
		case "resume":
			return m.resumeGoal()
		case "help":
			m.chatModel.AppendMeta("Usage: /goal <objective> | /goal | /goal pause | /goal resume | /goal clear")
			return m, nil
		}
	}

	// Everything else is the objective, so `/goal keep the tests green` sets a
	// goal and `/goal pause that test` sets a differently-worded one. Replacing a
	// goal is a normal thing to do, so there is no confirmation step: the
	// transcript records the change.
	m.setGoal(strings.Join(args, " "))
	// Setting a goal while the UI is idle starts the loop immediately: the point
	// of the command is to get work done, and requiring a second keystroke to
	// begin is a step that only exists to be forgotten.
	if m.running || m.goal == nil {
		return m, nil
	}
	return m.beginGoalCycle()
}

// printGoalSummary is the bare /goal output.
func (m *model) printGoalSummary() {
	if m.goal == nil {
		m.chatModel.AppendMeta("No goal is set. /goal <objective> to set one.")
		return
	}
	objective := truncatePlain(m.goal.Objective, 200)
	switch m.goal.Status {
	case pisession.GoalStatusActive:
		maxCycles := goalMaxCycles(m.goal.MaxCycles)
		m.chatModel.AppendMeta(fmt.Sprintf(
			"Goal (cycle %d of %d): %s\n/goal pause to stop the loop | /goal clear to drop it",
			m.goal.Cycles, maxCycles, objective))
	case pisession.GoalStatusPaused:
		m.chatModel.AppendMeta(fmt.Sprintf(
			"Goal paused: %s\n/goal resume to continue | /goal clear to drop it", objective))
	case pisession.GoalStatusComplete:
		note := m.goal.CompletionNote
		if note == "" {
			note = "no summary recorded"
		}
		m.chatModel.AppendMeta(fmt.Sprintf(
			"Goal complete: %s\n%s\n/goal <objective> to set a new one | /goal clear to drop it",
			objective, note))
	}
}

// continueGoalLoop is the loop's decision point, called at the end of every turn.
//
// It refuses more often than it continues, and each refusal is deliberate:
//
//   - no active goal, or a goal the user stopped (Esc, /goal pause, the cycle
//     budget). Nothing should restart a loop the user ended.
//   - a steer. The user replaced the prompt, so the replacement owns the turn
//     that follows; the loop resumes when that turn ends.
//   - a turn that this loop did not start (goalTurn() == 0), which is what a goal
//     set during an ordinary turn looks like. A manual turn must not silently
//     become a cycle the budget never counted.
//
// A failed turn does continue, on purpose. The cycles it spent stay spent, and
// the next attempt is a fresh cycle — which is exactly the recovery a provider
// outage needs. What must not happen, and cannot here, is a retry inside the
// failed turn.
func (m *model) continueGoalLoop(turnErr error) (tea.Model, tea.Cmd) {
	if !m.goalActive() || m.goalCanceled || m.steering {
		return m, nil
	}
	if m.goalTurn() == 0 {
		// A report left over from a turn the loop did not start names no running
		// cycle, so it is dropped rather than closing the goal on a claim nothing
		// attributed.
		m.takeGoalCompletion()
		return m, nil
	}
	if turnErr != nil {
		m.cfg.Logger.Errorf("goal cycle %d failed: %v", m.goalTurn(), turnErr)
	}
	m.publishGoalState()
	return m.beginGoalCycle()
}
