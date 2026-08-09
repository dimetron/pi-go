package tui

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/tools"
)

func TestHandleAgentWarning_AppendsWarning(t *testing.T) {
	m := newHandlerModel()
	m.handleAgentWarning(agentWarningMsg{text: "Response truncated: the model hit its output-token limit."})

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	last := m.chatModel.Messages[0]
	if !last.isWarning {
		t.Error("a truncation notice must render as a warning, not as ordinary reply text")
	}
	if !strings.Contains(last.content, "truncated") {
		t.Errorf("content = %q, want it to say the reply was truncated", last.content)
	}
}

func TestUpdateAgentStream_DispatchesWarning(t *testing.T) {
	m := newHandlerModel()
	_, _, handled := m.updateAgentStream(agentWarningMsg{text: "Response truncated."})
	if !handled {
		t.Fatal("agentWarningMsg must be handled by updateAgentStream")
	}
	if len(m.chatModel.Messages) != 1 || !m.chatModel.Messages[0].isWarning {
		t.Fatalf("expected the warning to reach the transcript, got %+v", m.chatModel.Messages)
	}
}

// replyRunawayLLM repeats itself in the reply stream rather than in thinking,
// covering the other branch of the streaming guard.
type replyRunawayLLM struct{ name string }

func (m *replyRunawayLLM) Name() string { return m.name }

func (m *replyRunawayLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		for range 4096 {
			if !yield(&llmmodel.LLMResponse{
				Partial: true,
				Content: &genai.Content{
					Role:  genai.RoleModel,
					Parts: []*genai.Part{{Text: runawayPhrase}},
				},
			}, nil) {
				return
			}
		}
		yield(&llmmodel.LLMResponse{
			TurnComplete: true,
			FinishReason: genai.FinishReasonMaxTokens,
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "done"}},
			},
		}, nil)
	}
}

func TestRunAgentLoop_AbortsRunawayReplyText(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	a, err := agent.New(agent.Config{Model: &replyRunawayLLM{name: "runaway-reply"}, Tools: coreTools, Instruction: "test"})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sid, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	m := &model{cfg: Config{Agent: a, SessionID: sid}, ctx: ctx}
	m.agentCh = make(chan agentMsg, 8192)

	done := make(chan struct{})
	var doneErr error
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			if v, ok := msg.(agentDoneMsg); ok {
				doneErr = v.err
			}
		}
	}()

	go m.runAgentLoop(ctx, "explain the hook")

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("runAgentLoop did not terminate -- runaway reply text was not detected")
	}
	if doneErr == nil || !strings.Contains(doneErr.Error(), "aborted") {
		t.Fatalf("expected loop-aborted error, got: %v", doneErr)
	}
}
