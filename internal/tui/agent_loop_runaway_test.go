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

const runawayPhrase = "Let me look at how the TUI renders messages and whether hook output surfaces. "

func TestStuckDetector_ObserveOutput_TripsOnRepeatedPhrase(t *testing.T) {
	s := &stuckDetector{}
	var stuck bool
	var detail string
	for range maxOutputRepeats * 4 {
		if stuck, detail = s.observeOutput(runawayPhrase); stuck {
			break
		}
	}
	if !stuck {
		t.Fatal("expected the repeated phrase to trip the detector")
	}
	if !strings.Contains(detail, "repeated") {
		t.Errorf("detail = %q, want it to mention repetition", detail)
	}
}

func TestStuckDetector_ObserveOutput_AllowsProse(t *testing.T) {
	s := &stuckDetector{}
	// Distinct sentences, well past the byte volume that trips a repeat.
	for i := range 400 {
		text := strings.Repeat("x", i%37) + " unique sentence number " + strings.Repeat("y", i%29) + ". "
		if stuck, detail := s.observeOutput(text); stuck {
			t.Fatalf("non-repeating output tripped the detector at chunk %d: %s", i, detail)
		}
	}
}

func TestStuckDetector_ObserveOutput_IgnoresShortPeriods(t *testing.T) {
	// A horizontal rule is periodic with period 1; it must not count.
	s := &stuckDetector{}
	for range 40 {
		if stuck, _ := s.observeOutput(strings.Repeat("-", 80) + "\n"); stuck {
			t.Fatal("a run of dashes must not be treated as a repetition loop")
		}
	}
}

func TestToolFingerprint_IgnoresPagination(t *testing.T) {
	base := map[string]any{"file_path": "/repo/chat.go"}
	paged := map[string]any{"file_path": "/repo/chat.go", "offset": 230, "limit": 40}
	if toolFingerprint("read", base) != toolFingerprint("read", paged) {
		t.Error("pagination args must not change the fingerprint")
	}
	other := map[string]any{"file_path": "/repo/other.go", "offset": 230, "limit": 40}
	if toolFingerprint("read", paged) == toolFingerprint("read", other) {
		t.Error("a different file must change the fingerprint")
	}
}

func TestStuckDetector_Observe_TripsOnPagedRereads(t *testing.T) {
	s := &stuckDetector{}
	var stuck bool
	for i := range maxRepeatToolCalls {
		stuck, _ = s.observe("read", map[string]any{"file_path": "/repo/chat.go", "offset": i * 40})
	}
	if !stuck {
		t.Fatalf("re-reading one file %d times must trip the detector", maxRepeatToolCalls)
	}
}

// runawayLLM streams one sentence over and over and never calls a tool,
// reproducing the deepseek-v4-flash:0731:cloud turn that emitted 148 KB of the
// same text before the output cap stopped it.
type runawayLLM struct {
	name string
	mu   sync.Mutex
	n    int
}

func (m *runawayLLM) Name() string { return m.name }

func (m *runawayLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	m.mu.Lock()
	m.n++
	m.mu.Unlock()
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		for range 4096 {
			if !yield(&llmmodel.LLMResponse{
				Partial: true,
				Content: &genai.Content{
					Role:  "thinking",
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

// TestRunAgentLoop_AbortsRunawayOutput drives the whole pipeline against a
// model that has collapsed into repeating itself with no tool calls at all —
// the case both tool-call detectors are blind to.
func TestRunAgentLoop_AbortsRunawayOutput(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	a, err := agent.New(agent.Config{Model: &runawayLLM{name: "runaway"}, Tools: coreTools, Instruction: "test"})
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

	go m.runAgentLoop(ctx, "why is the hook message visible")

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("runAgentLoop did not terminate -- runaway output was not detected")
	}

	if doneErr == nil || !strings.Contains(doneErr.Error(), "aborted") {
		t.Fatalf("expected loop-aborted error, got: %v", doneErr)
	}
}

// truncatedLLM ends its turn at the output cap, the finish reason nothing used
// to act on.
type truncatedLLM struct{ name string }

func (m *truncatedLLM) Name() string { return m.name }

func (m *truncatedLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			TurnComplete: true,
			FinishReason: genai.FinishReasonMaxTokens,
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "a reply that stops mid-sen"}},
			},
		}, nil)
	}
}

func TestRunAgentLoop_WarnsOnTruncatedTurn(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	a, err := agent.New(agent.Config{Model: &truncatedLLM{name: "truncated"}, Tools: coreTools, Instruction: "test"})
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
	var warnings []string
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			if v, ok := msg.(agentWarningMsg); ok {
				warnings = append(warnings, v.text)
			}
		}
	}()

	go m.runAgentLoop(ctx, "write something long")

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runAgentLoop did not terminate")
	}

	if len(warnings) != 1 {
		t.Fatalf("got %d truncation warnings, want exactly 1: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "truncated") {
		t.Errorf("warning = %q, want it to say the reply was truncated", warnings[0])
	}
}
