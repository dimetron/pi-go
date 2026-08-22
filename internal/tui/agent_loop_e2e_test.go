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
	"github.com/dimetron/pi-go/internal/tools"
)

// loopLLM emits the same bash function call with empty args on every turn,
// reproducing the minimax-m3:cloud stuck loop where the model repeatedly emits
// a tool call with missing arguments and never makes progress.
type loopLLM struct {
	name string
	mu   sync.Mutex
	n    int
}

func (m *loopLLM) Name() string { return m.name }

func (m *loopLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	m.mu.Lock()
	m.n++
	m.mu.Unlock()
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			Content: &genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "c", Name: "bash", Args: map[string]any{}}},
				},
			},
		}, nil)
	}
}

// TestRunAgentLoop_AbortsStuckBashLoop exercises the full runAgentLoop event
// pipeline (not just the stuckDetector in isolation) against a model that keeps
// emitting an identical failing bash call. It guards the end-to-end wiring:
// SSE RunStreaming must deliver FunctionResponse events to the loop, and
// observeResult must abort the run once the same call fails maxRepeatErrorCalls
// times in a row. Regression for the minimax-m3:cloud empty-args tool loop.
func TestRunAgentLoop_AbortsStuckBashLoop(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	// The sandbox keeps an os.Root open on dir; Windows will not remove a
	// directory that anything still holds a handle to, so t.TempDir's cleanup
	// fails unless it is closed.
	t.Cleanup(func() { _ = sb.Close() })
	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	a, err := agent.New(agent.Config{Model: &loopLLM{name: "loop"}, Tools: coreTools, Instruction: "test"})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sid, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	m := &model{cfg: Config{Agent: a, SessionID: sid}, ctx: ctx}
	m.agentCh = make(chan agentMsg, 256)

	done := make(chan struct{})
	var toolCalls int
	var doneErr error
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			switch v := msg.(type) {
			case agentToolCallMsg:
				toolCalls++
			case agentDoneMsg:
				doneErr = v.err
			}
		}
	}()

	go m.runAgentLoop(ctx, "run the analysis script", m.agentCh, m.agentRun())

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runAgentLoop did not terminate within 10s -- stuck detector failed to abort")
	}

	if doneErr == nil || !strings.Contains(doneErr.Error(), "aborted") {
		t.Fatalf("expected loop-aborted error, got: %v", doneErr)
	}
	// The guard fires every attempt, and a stuck turn is handed back to the
	// model maxStuckRecoveries times before the run ends — so the bound is
	// per-attempt, not per-run. Anything above this means a guard stopped firing.
	wantCalls := maxRepeatErrorCalls * (1 + maxStuckRecoveries)
	if toolCalls != wantCalls {
		t.Fatalf("aborted after %d identical failing tool calls, want %d (%d per attempt x %d attempts)",
			toolCalls, wantCalls, maxRepeatErrorCalls, 1+maxStuckRecoveries)
	}
}
