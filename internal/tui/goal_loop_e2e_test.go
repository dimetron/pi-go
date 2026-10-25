package tui

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
)

// goalLoopLLM is a model that plays the agent's part in a goal loop: on each turn
// it either keeps working (plain text, so the turn ends with the goal still
// active) or declares the goal complete by calling goal_complete with the cycle
// the prompt names.
//
// It parses the cycle out of the request rather than hard-coding one, so the test
// fails if the loop stops telling the model which cycle it is running — which is
// the check that keeps a stale report from closing the goal.
type goalLoopLLM struct {
	name string
	// completeOnTurn is the 1-based model turn at which it reports completion,
	// or 0 to never complete (the budget-spending case).
	completeOnTurn int
	// badCycle makes it report for a cycle other than the running one.
	badCycle bool

	mu    sync.Mutex
	turns int
	seen  []string
}

func (m *goalLoopLLM) Name() string { return m.name }

func (m *goalLoopLLM) GenerateContent(_ context.Context, req *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	m.mu.Lock()
	m.turns++
	turn := m.turns
	var promptText strings.Builder
	if req != nil {
		// The system instruction is where the loop publishes the running cycle,
		// so the model can only report a completion the tool will accept if this
		// text carries it.
		if req.Config != nil && req.Config.SystemInstruction != nil {
			for _, p := range req.Config.SystemInstruction.Parts {
				if p != nil && p.Text != "" {
					promptText.WriteString(p.Text)
				}
			}
		}
		for _, c := range req.Contents {
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.Text != "" {
					promptText.WriteString(p.Text)
				}
			}
		}
	}
	m.seen = append(m.seen, promptText.String())
	m.mu.Unlock()

	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		if m.completeOnTurn != 0 && turn >= m.completeOnTurn {
			cycle := goalCycleFromPrompt(promptText.String())
			if m.badCycle {
				cycle++
			}
			yield(&llmmodel.LLMResponse{Content: &genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
					ID:   "goal",
					Name: "goal_complete",
					Args: map[string]any{"cycle": cycle, "summary": "the tests pass"},
				}}},
			}}, nil)
			return
		}
		yield(&llmmodel.LLMResponse{
			Content: genai.NewContentFromText("Still working on it.", genai.RoleModel),
		}, nil)
	}
}

// goalCycleFromPrompt returns the cycle the "# Current Goal" section names, or 0.
func goalCycleFromPrompt(prompt string) int {
	const marker = "cycle="
	i := strings.Index(prompt, marker)
	if i < 0 {
		return 0
	}
	n := 0
	for _, r := range prompt[i+len(marker):] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// goalLoopModel wires a model, a real agent with the goal tool, and a goal, which
// is everything the loop reads.
func goalLoopModel(t *testing.T, llm *goalLoopLLM, maxCycles int) *model {
	t.Helper()
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	// Windows cannot remove a directory with an open handle; the sandbox keeps one.
	t.Cleanup(func() { _ = sb.Close() })

	opt, ctrl := tools.WithGoal()
	ct, err := tools.CoreTools(sb, opt)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	a, err := agent.New(agent.Config{Model: llm, Tools: ct, Instruction: "test"})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sid, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	m := newTestModel(t)
	m.cfg.Agent = a
	m.cfg.SessionID = sid
	m.cfg.GoalTool = ctrl
	m.ctx = ctx
	m.goal = &pisession.Goal{
		Objective: "make the tests pass",
		Status:    pisession.GoalStatusActive,
		MaxCycles: maxCycles,
	}
	return m
}

// runTurns drives the loop exactly as the TUI does: start a cycle, drain the real
// agent channel until the turn ends, and hand that ending to handleAgentDone —
// which is the only thing that can start the next cycle.
//
// It deliberately does not call streamTurn or runAgentLoop itself. The question
// this test answers is whether the loop is wired, and running the turn by hand
// would answer a different one: whether the turn works.
func runTurns(t *testing.T, m *model) []agentDoneMsg {
	t.Helper()
	var ends []agentDoneMsg

	_, cmd := m.beginGoalCycle()
	if cmd == nil {
		t.Fatal("beginGoalCycle did not start the first cycle")
	}

	for i := 0; i < 50; i++ { // hard stop so a runaway test fails instead of hanging
		ch := m.agentCh
		if ch == nil {
			return ends
		}
		// A clean turn ends by closing the channel, not by sending a done
		// message: waitForAgent synthesizes agentDoneMsg{} on the close. Reading
		// until the close is what the real Update goroutine does, so reading it
		// any other way would test a signal production never uses.
		var end agentDoneMsg
		deadline := time.After(20 * time.Second)
	readLoop:
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					break readLoop
				}
				if v, ok := msg.(agentDoneMsg); ok {
					end = v
				}
			case <-deadline:
				t.Fatal("an agent turn did not finish within 20s")
			}
		}
		ends = append(ends, end)

		_, cmd = m.handleAgentDone(end)
		if cmd == nil {
			return ends
		}
	}
	t.Fatal("goal loop did not settle within 50 turns")
	return nil
}

// The whole feature, end to end: a real agent calls the real goal_complete tool,
// the loop reads the report through the TUI, and the goal closes without spending
// another cycle.
func TestGoalLoop_CompletesEndToEnd(t *testing.T) {
	llm := &goalLoopLLM{name: "goal", completeOnTurn: 2}
	m := goalLoopModel(t, llm, 5)

	runTurns(t, m)

	if m.goal.Status != pisession.GoalStatusComplete {
		t.Fatalf("Status = %q, want complete (cycles spent: %d)", m.goal.Status, m.goal.Cycles)
	}
	if m.goal.Cycles != 2 {
		t.Errorf("Cycles = %d, want 2: the goal closed on the turn that reported it", m.goal.Cycles)
	}
	if m.goal.CompletionNote != "the tests pass" {
		t.Errorf("CompletionNote = %q, want the agent's summary", m.goal.CompletionNote)
	}
	// The prompt for cycle 2 must have named cycle 2, or the tool would have
	// rejected the report and the loop would still be running.
	if got := goalCycleFromPrompt(llm.seen[len(llm.seen)-1]); got != 2 {
		t.Errorf("the model was told cycle %d on its second turn, want 2", got)
	}
}

// A model that never completes must spend exactly the budget and stop, not run
// forever.
func TestGoalLoop_StopsAtTheBudget(t *testing.T) {
	llm := &goalLoopLLM{name: "goal", completeOnTurn: 0}
	m := goalLoopModel(t, llm, 3)

	runTurns(t, m)

	if m.goal.Cycles != 3 {
		t.Fatalf("Cycles = %d, want the budget of 3", m.goal.Cycles)
	}
	if !m.goalCanceled {
		t.Error("goalCanceled = false after the budget was spent")
	}
	// Still active, so /goal resume can continue it rather than losing it.
	if m.goal.Status != pisession.GoalStatusActive {
		t.Errorf("Status = %q, want active so the goal can be resumed", m.goal.Status)
	}
}

// A report for a cycle that is not running is rejected, and the loop keeps going
// instead of closing on it.
func TestGoalLoop_RejectsStaleCompletion(t *testing.T) {
	llm := &goalLoopLLM{name: "goal", completeOnTurn: 2, badCycle: true}
	m := goalLoopModel(t, llm, 3)

	runTurns(t, m)

	if m.goal.Status == pisession.GoalStatusComplete {
		t.Fatal("a report for the wrong cycle closed the goal")
	}
	if m.goal.Cycles != 3 {
		t.Errorf("Cycles = %d, want the full budget of 3: the rejected report did not stop the loop", m.goal.Cycles)
	}
}

// Esc during an automatic cycle must stop the loop at the turn boundary, not just
// clear a flag: the canceled turn still reports done, and that report is what
// would otherwise start the very turn the user just stopped.
func TestGoalLoop_EscStopsAtTheTurnBoundary(t *testing.T) {
	llm := &goalLoopLLM{name: "goal", completeOnTurn: 0}
	m := goalLoopModel(t, llm, 5)

	_, cmd := m.beginGoalCycle()
	if cmd == nil {
		t.Fatal("beginGoalCycle did not start the first cycle")
	}
	cyclesAfterFirst := m.goal.Cycles

	// Esc while the cycle runs.
	m.cancelAgent()

	// The canceled turn's own report is what reaches handleAgentDone.
	m.running = false
	_, next := m.handleAgentDone(agentDoneMsg{})
	if next != nil {
		t.Fatal("handleAgentDone started a new cycle after Esc")
	}
	if m.goal.Cycles != cyclesAfterFirst {
		t.Errorf("Cycles = %d, want %d: Esc must not spend another turn", m.goal.Cycles, cyclesAfterFirst)
	}
	if m.goal.Status != pisession.GoalStatusActive {
		t.Errorf("Status = %q, want active so /goal resume can continue it", m.goal.Status)
	}
}
