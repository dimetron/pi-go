package tui

import (
	"context"
	"errors"
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

// fnLLM is a fully scriptable mock LLM: gen is called once per turn with the
// zero-based turn index and returns the response (or error) to yield. This lets
// a single type drive every runAgentLoop branch — text completion, tool calls,
// stuck loops, and streaming errors.
type fnLLM struct {
	name string
	mu   sync.Mutex
	n    int
	gen  func(call int) (*llmmodel.LLMResponse, error)
}

func (m *fnLLM) Name() string { return m.name }

func (m *fnLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	m.mu.Lock()
	call := m.n
	m.n++
	m.mu.Unlock()
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		resp, err := m.gen(call)
		yield(resp, err)
	}
}

func textResp(s string) *llmmodel.LLMResponse {
	return &llmmodel.LLMResponse{Content: genai.NewContentFromText(s, genai.RoleModel)}
}

func callResp(name string, args map[string]any) *llmmodel.LLMResponse {
	return &llmmodel.LLMResponse{
		Content: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "c", Name: name, Args: args}}},
		},
	}
}

// newRunTestAgent builds a real Agent backed by llm with the full core toolset
// over a fresh sandbox, plus a created session.
func newRunTestAgent(t *testing.T, llm llmmodel.LLM) (*agent.Agent, string) {
	t.Helper()
	sb, err := tools.NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	a, err := agent.New(agent.Config{Model: llm, Tools: coreTools, Instruction: "test"})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	sid, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return a, sid
}

// runResult collects everything runAgentLoop emitted before the channel closed.
type runResult struct {
	texts       []string
	toolCalls   []agentToolCallMsg
	toolResults []agentToolResultMsg
	doneErr     error
	doneCount   int
}

// driveRunLoop runs m.runAgentLoop to completion, draining the agent channel,
// and returns the collected messages. It fails the test if the loop does not
// terminate, which is the property the stuck detector must guarantee.
func driveRunLoop(t *testing.T, a *agent.Agent, sid, prompt string) runResult {
	t.Helper()
	m := &model{cfg: Config{Agent: a, SessionID: sid}, ctx: context.Background()}
	m.agentCh = make(chan agentMsg, 1024)

	var res runResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			switch v := msg.(type) {
			case agentTextMsg:
				res.texts = append(res.texts, v.text)
			case agentToolCallMsg:
				res.toolCalls = append(res.toolCalls, v)
			case agentToolResultMsg:
				res.toolResults = append(res.toolResults, v)
			case agentDoneMsg:
				res.doneErr = v.err
				res.doneCount++
			}
		}
	}()

	go m.runAgentLoop(context.Background(), prompt)

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("runAgentLoop did not terminate within 20s")
	}
	return res
}

func TestRunAgentLoop_AgentNotConfigured(t *testing.T) {
	m := &model{cfg: Config{}, ctx: context.Background()}
	m.agentCh = make(chan agentMsg, 4)

	var doneErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			if d, ok := msg.(agentDoneMsg); ok {
				doneErr = d.err
			}
		}
	}()
	go m.runAgentLoop(context.Background(), "hi")
	<-done

	if doneErr == nil || !strings.Contains(doneErr.Error(), "not configured") {
		t.Fatalf("expected 'agent not configured' error, got %v", doneErr)
	}
}

func TestRunAgentLoop_TextCompletion(t *testing.T) {
	a, sid := newRunTestAgent(t, &fnLLM{name: "text", gen: func(int) (*llmmodel.LLMResponse, error) {
		return textResp("all done"), nil
	}})

	res := driveRunLoop(t, a, sid, "say something")

	// Clean completion closes the channel without an explicit done message
	// (waitForAgent synthesizes one from the close in the live TUI).
	if res.doneErr != nil {
		t.Fatalf("expected clean completion, got error: %v", res.doneErr)
	}
	if strings.Join(res.texts, "") == "" {
		t.Fatalf("expected some assistant text, got none")
	}
}

func TestRunAgentLoop_ToolCallThenCompletion(t *testing.T) {
	a, sid := newRunTestAgent(t, &fnLLM{name: "tool", gen: func(call int) (*llmmodel.LLMResponse, error) {
		if call == 0 {
			return callResp("bash", map[string]any{"command": "echo hi"}), nil
		}
		return textResp("finished"), nil
	}})

	res := driveRunLoop(t, a, sid, "run a command")

	if res.doneErr != nil {
		t.Fatalf("expected clean completion, got error: %v", res.doneErr)
	}
	if len(res.toolCalls) != 1 || res.toolCalls[0].name != "bash" {
		t.Fatalf("expected one bash tool call, got %+v", res.toolCalls)
	}
	if len(res.toolResults) != 1 {
		t.Fatalf("expected one tool result, got %d", len(res.toolResults))
	}
}

func TestRunAgentLoop_AbortsIdenticalSuccessfulCallLoop(t *testing.T) {
	// A model that keeps emitting the same successful call never errors, so the
	// error-streak guard never fires; the cycle/streak detector must stop it.
	a, sid := newRunTestAgent(t, &fnLLM{name: "spin", gen: func(int) (*llmmodel.LLMResponse, error) {
		return callResp("bash", map[string]any{"command": "echo ok"}), nil
	}})

	res := driveRunLoop(t, a, sid, "loop forever")

	if res.doneErr == nil || !strings.Contains(res.doneErr.Error(), "aborted") {
		t.Fatalf("expected loop-aborted error, got %v", res.doneErr)
	}
	if len(res.toolCalls) > maxRepeatToolCalls {
		t.Fatalf("detector allowed %d identical calls (want <= %d)", len(res.toolCalls), maxRepeatToolCalls)
	}
}

func TestRunAgentLoop_AbortsSameToolVaryingArgs(t *testing.T) {
	// Each call targets a path outside the sandbox so the read fails, with
	// distinct args every turn — only the same-tool guard can catch this.
	a, sid := newRunTestAgent(t, &fnLLM{name: "flail", gen: func(call int) (*llmmodel.LLMResponse, error) {
		return callResp("read", map[string]any{"file_path": "/nonexistent/path/" + strings.Repeat("x", call+1)}), nil
	}})

	res := driveRunLoop(t, a, sid, "read missing files")

	if res.doneErr == nil || !strings.Contains(res.doneErr.Error(), "aborted") {
		t.Fatalf("expected loop-aborted error, got %v", res.doneErr)
	}
	if len(res.toolCalls) > maxToolErrorStreak {
		t.Fatalf("same-tool guard allowed %d failing calls (want <= %d)", len(res.toolCalls), maxToolErrorStreak)
	}
}

func TestRunAgentLoop_StreamingError(t *testing.T) {
	wantErr := errors.New("model exploded")
	a, sid := newRunTestAgent(t, &fnLLM{name: "boom", gen: func(int) (*llmmodel.LLMResponse, error) {
		return nil, wantErr
	}})

	res := driveRunLoop(t, a, sid, "trigger error")

	if res.doneErr == nil || !strings.Contains(res.doneErr.Error(), "model exploded") {
		t.Fatalf("expected streaming error propagated, got %v", res.doneErr)
	}
}
