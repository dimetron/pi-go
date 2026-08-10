package tui

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	llmmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/agent"
)

// apiErrorLLM reproduces how every provider reports an HTTP failure: a
// content-less LLMResponse carrying ErrorCode/ErrorMessage, yielded with a
// *nil* Go error. See internal/provider/openai_completions.go:279 and friends.
type apiErrorLLM struct{ msg string }

func (m *apiErrorLLM) Name() string { return "api-error" }

func (m *apiErrorLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			ErrorCode:    "STREAM_ERROR",
			ErrorMessage: m.msg,
		}, nil)
	}
}

// TestRunAgentLoop_SurfacesProviderAPIError is the regression for the
// 2026-07-28 sessions where a 400 ("Function tools with reasoning_effort are
// not supported ... in /v1/chat/completions") and a 429 from Ollama both landed
// in the session log but never reached the screen: ADK ends the turn cleanly,
// the err branch never fires, and the ev.Content == nil guard dropped the
// event. The user saw the spinner stop and nothing else.
func TestRunAgentLoop_SurfacesProviderAPIError(t *testing.T) {
	const apiMsg = `POST "https://api.openai.com/v1/chat/completions": 400 Bad Request ` +
		`{"message": "Function tools with reasoning_effort are not supported for gpt-5.6-terra"}`

	a, err := agent.New(agent.Config{Model: &apiErrorLLM{msg: apiMsg}, Instruction: "test"})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sid, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	m := &model{cfg: Config{Agent: a, SessionID: sid}, ctx: ctx}
	m.agentCh = make(chan agentMsg, 16)

	done := make(chan struct{})
	var doneErr error
	var sawDone bool
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			if v, ok := msg.(agentDoneMsg); ok {
				sawDone = true
				doneErr = v.err
			}
		}
	}()

	go m.runAgentLoop(ctx, "hello", m.agentCh, m.agentRun())

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runAgentLoop did not terminate within 10s")
	}

	if !sawDone {
		t.Fatal("no agentDoneMsg emitted -- the API error was swallowed")
	}
	if doneErr == nil {
		t.Fatal("agentDoneMsg carried no error -- the API error was swallowed")
	}
	if !strings.Contains(doneErr.Error(), "400 Bad Request") {
		t.Errorf("error does not carry the provider message: %v", doneErr)
	}
}

// TestHandleAgentDone_RendersErrorStyled checks the error reaches the chat
// transcript in the error style, not as a plain assistant bubble that reads
// like part of the model's answer.
func TestHandleAgentDone_RendersErrorStyled(t *testing.T) {
	m := &model{running: true}
	m.chatModel = NewChatModel(nil)

	if _, cmd := m.handleAgentDone(agentDoneMsg{err: context.DeadlineExceeded}); cmd != nil {
		t.Errorf("handleAgentDone returned a Cmd, want nil")
	}

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(m.chatModel.Messages))
	}
	got := m.chatModel.Messages[0]
	if !got.isError {
		t.Error("error message is not flagged isError -- it renders as a normal reply")
	}
	if !strings.Contains(got.content, context.DeadlineExceeded.Error()) {
		t.Errorf("message content = %q, want it to contain the error", got.content)
	}
	if kindOf(&got) != blockError {
		t.Errorf("kindOf = %v, want blockError so the minimap marks it", kindOf(&got))
	}
	if m.running {
		t.Error("running flag not cleared")
	}
}

// TestChatRender_ErrorMessageIsDistinct guards that an error message renders
// differently from an ordinary assistant reply with the same text, and that the
// render cache keys on the flag so the two never alias.
func TestChatRender_ErrorMessageIsDistinct(t *testing.T) {
	plain := message{role: "assistant", content: "boom"}
	errMsg := message{role: "assistant", content: "boom", isError: true}

	if plain.renderKey(80, false, false, false, 0) == errMsg.renderKey(80, false, false, false, 0) {
		t.Fatal("renderKey collides for plain and error messages -- cached render would leak")
	}

	c := NewChatModel(nil)
	c.Width = 80
	c.AppendError("boom")
	out := c.RenderMessages(false)
	if !strings.Contains(out, "✖") {
		t.Errorf("rendered chat has no error bullet:\n%s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("rendered chat lost the error text:\n%s", out)
	}
}
