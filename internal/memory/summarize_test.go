package memory

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// fakeSummarizerLLM is a model.LLM that replies with a fixed body and records
// whether it was called at all.
type fakeSummarizerLLM struct {
	reply string
	err   error

	mu     sync.Mutex
	calls  int
	prompt string // the concatenated user content it was handed
}

func (f *fakeSummarizerLLM) Name() string { return "fake-summarizer" }

func (f *fakeSummarizerLLM) GenerateContent(_ context.Context, req *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	f.mu.Lock()
	f.calls++
	if req != nil {
		var b strings.Builder
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				b.WriteString(p.Text)
			}
		}
		f.prompt = b.String()
	}
	reply, err := f.reply, f.err
	f.mu.Unlock()

	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		if err != nil {
			yield(nil, err)
			return
		}
		yield(&llmmodel.LLMResponse{
			Content: genai.NewContentFromText(reply, genai.RoleModel),
		}, nil)
	}
}

func (f *fakeSummarizerLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func testObservations() []*Observation {
	return []*Observation{
		{
			SessionID:   "sess-1",
			Project:     "/project",
			Title:       "Added per-session query",
			Type:        TypeFeature,
			Text:        "Added SessionObservations and a test.",
			SourceFiles: []string{"internal/memory/store.go"},
			ToolName:    "edit",
		},
	}
}

const validSummaryJSON = `{"request":"wire session summaries","investigated":"memory store","learned":"no per-session query existed","completed":"added one","next_steps":"wire piagent"}`

func TestLLMSummarizer_ParsesReply(t *testing.T) {
	llm := &fakeSummarizerLLM{reply: validSummaryJSON}
	s := NewLLMSummarizer(llm)

	sum, err := s.SummarizeSession(context.Background(), "sess-1", "/project", testObservations())
	if err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if sum.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", sum.SessionID)
	}
	if sum.Project != "/project" {
		t.Errorf("Project = %q, want /project", sum.Project)
	}
	// Field mapping is the point: the model emits request/investigated/... and
	// they must land on the matching struct fields, not be transposed.
	if sum.Request != "wire session summaries" {
		t.Errorf("Request = %q", sum.Request)
	}
	if sum.Completed != "added one" {
		t.Errorf("Completed = %q", sum.Completed)
	}
	if sum.NextSteps != "wire piagent" {
		t.Errorf("NextSteps = %q", sum.NextSteps)
	}

	// The prompt must carry the observation text, or the model is summarizing
	// nothing.
	if !strings.Contains(llm.prompt, "Added per-session query") {
		t.Errorf("prompt did not include the observation title; prompt = %q", llm.prompt)
	}
}

// A model that wraps its JSON in a markdown fence is the common real-world
// case, and the subagent path already tolerates it. Both paths share
// parseSummaryResponse, so this guards the shared tolerance.
func TestLLMSummarizer_ParsesFencedReply(t *testing.T) {
	llm := &fakeSummarizerLLM{reply: "```json\n" + validSummaryJSON + "\n```"}
	s := NewLLMSummarizer(llm)

	sum, err := s.SummarizeSession(context.Background(), "sess-1", "/project", testObservations())
	if err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if sum.Request != "wire session summaries" {
		t.Errorf("Request = %q, want the parsed body", sum.Request)
	}
}

func TestLLMSummarizer_EmptyObservationsDoesNotCallModel(t *testing.T) {
	llm := &fakeSummarizerLLM{reply: validSummaryJSON}
	s := NewLLMSummarizer(llm)

	if _, err := s.SummarizeSession(context.Background(), "sess-1", "/project", nil); err == nil {
		t.Fatal("expected an error for an empty observation list")
	}
	if n := llm.callCount(); n != 0 {
		t.Errorf("model was called %d time(s) for an empty session, want 0", n)
	}
}

func TestLLMSummarizer_PropagatesModelError(t *testing.T) {
	sentinel := errors.New("provider exploded")
	llm := &fakeSummarizerLLM{err: sentinel}
	s := NewLLMSummarizer(llm)

	_, err := s.SummarizeSession(context.Background(), "sess-1", "/project", testObservations())
	if err == nil {
		t.Fatal("expected the model error to propagate")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want it to wrap %v", err, sentinel)
	}
}

func TestLLMSummarizer_InvalidJSONIsAnError(t *testing.T) {
	llm := &fakeSummarizerLLM{reply: "I could not summarize that session."}
	s := NewLLMSummarizer(llm)

	if _, err := s.SummarizeSession(context.Background(), "sess-1", "/project", testObservations()); err == nil {
		t.Fatal("expected an error for a non-JSON reply")
	}
}

// A nil model must be an error, not a panic: the callers treat "summarization
// unavailable" as an ordinary degraded path.
func TestLLMSummarizer_NilModel(t *testing.T) {
	var s *LLMSummarizer
	if _, err := s.SummarizeSession(context.Background(), "s", "/p", testObservations()); err == nil {
		t.Error("nil summarizer: expected an error")
	}

	empty := NewLLMSummarizer(nil)
	if _, err := empty.SummarizeSession(context.Background(), "s", "/p", testObservations()); err == nil {
		t.Error("nil model: expected an error")
	}
}
