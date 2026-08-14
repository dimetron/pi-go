package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	llmmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/agent"
)

// driveRunLoopWithWarnings is driveRunLoop plus the warning stream, which is
// how a recovery announces itself to the user.
func driveRunLoopWithWarnings(t *testing.T, a *agent.Agent, sid, prompt string) (runResult, []string) {
	t.Helper()
	m := &model{cfg: Config{Agent: a, SessionID: sid}, ctx: context.Background()}
	m.agentCh = make(chan agentMsg, 4096)

	var res runResult
	var warnings []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			switch v := msg.(type) {
			case agentTextMsg:
				res.texts = append(res.texts, v.text)
			case agentToolCallMsg:
				res.toolCalls = append(res.toolCalls, v)
			case agentWarningMsg:
				warnings = append(warnings, v.text)
			case agentDoneMsg:
				res.doneErr = v.err
				res.doneCount++
			}
		}
	}()

	go m.runAgentLoop(context.Background(), prompt, m.agentCh, m.agentRun())

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("runAgentLoop did not terminate within 30s")
	}
	return res, warnings
}

// TestRunAgentLoop_RecoversFromRepeatedOutput is the behavior the user asked
// for: the run is not ended on the model's first degenerate stretch. The
// detector's reason is handed back and the turn continues in the same session.
func TestRunAgentLoop_RecoversFromRepeatedOutput(t *testing.T) {
	var mu sync.Mutex
	turn := 0

	phrase := "I will now carefully re-examine the situation and consider the available options once more. "
	llm := &fnLLM{name: "babbler", gen: func(_ int) (*llmmodel.LLMResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		turn++
		if turn == 1 {
			// Enough copies to trip maxOutputRepeats on the first turn.
			return textResp(strings.Repeat(phrase, maxOutputRepeats+4)), nil
		}
		return textResp("Understood - stopping the repetition. The answer is 42."), nil
	}}

	a, sid := newRunTestAgent(t, llm)
	res, warnings := driveRunLoopWithWarnings(t, a, sid, "explain something")

	if res.doneErr != nil {
		t.Fatalf("run ended with an error instead of recovering: %v", res.doneErr)
	}
	if len(warnings) == 0 {
		t.Fatal("recovery happened silently; the user must be told the loop was detected")
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "Loop detected") {
		t.Fatalf("warning does not mention the loop:\n%s", strings.Join(warnings, "\n"))
	}
	joined := strings.Join(res.texts, "")
	if !strings.Contains(joined, "42") {
		t.Fatalf("recovered turn's answer never reached the user; got:\n%s", truncateForTest(joined))
	}
	mu.Lock()
	defer mu.Unlock()
	if turn < 2 {
		t.Fatalf("model was asked %d time(s), want >= 2 (the retry never happened)", turn)
	}
}

// TestRunAgentLoop_GivesUpAfterRecoveryBudget is the other half: recovery is
// bounded, so a model that repeats no matter what still ends the run rather
// than retrying forever.
func TestRunAgentLoop_GivesUpAfterRecoveryBudget(t *testing.T) {
	var mu sync.Mutex
	turns := 0

	phrase := "Let me reconsider this from the beginning one more time before proceeding further. "
	llm := &fnLLM{name: "hopeless", gen: func(_ int) (*llmmodel.LLMResponse, error) {
		mu.Lock()
		turns++
		mu.Unlock()
		return textResp(strings.Repeat(phrase, maxOutputRepeats+4)), nil
	}}

	a, sid := newRunTestAgent(t, llm)
	res, _ := driveRunLoopWithWarnings(t, a, sid, "explain something")

	if res.doneErr == nil {
		t.Fatal("expected the run to end after the recovery budget, got no error")
	}
	if !strings.Contains(res.doneErr.Error(), "aborted") {
		t.Fatalf("error lost the 'aborted' wording: %v", res.doneErr)
	}
	if !strings.Contains(res.doneErr.Error(), "gave up after") {
		t.Fatalf("error does not say recovery was attempted: %v", res.doneErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := 1 + maxStuckRecoveries; turns != want {
		t.Fatalf("model was asked %d times, want exactly %d (1 attempt + %d recoveries)",
			turns, want, maxStuckRecoveries)
	}
}

func truncateForTest(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
