package agent

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	pisession "github.com/dimetron/pi-go/internal/session"
)

// captureLLM records the contents it was asked to generate from and returns a
// fixed final answer, so a test can assert what the provider would have seen.
type captureLLM struct {
	name string
	mu   sync.Mutex
	reqs []*genai.Content
}

func (m *captureLLM) Name() string { return m.name }

func (m *captureLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	m.reqs = append(m.reqs, req.Contents...)
	m.mu.Unlock()
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{Content: genai.NewContentFromText("done", genai.RoleModel)}, nil)
	}
}

// TestInterruptedCallReachesTheProvider reproduces the
// "adk: dropping function calls with no matching function response" report
// end to end (now a regression guard: the call must survive), against a real ADK agent and a real FileService session store.
//
// It seeds a session exactly the way an interrupted turn leaves one: a model
// event that issued a bash call, followed by the user's next prompt with no
// FunctionResponse in between — which is what steer (agent_loop.go:945),
// cancelAgent (agent_loop.go:830) and the stuck-loop recovery
// (agent_loop.go:1167) all produce.
//
// The assertion is about what reaches the MODEL, not about a log line: pi-go
// cannot see ADK's log without the std-logger redirect, and the redirect is a
// TUI-only concern. If the call survives into the request, the history is
// intact; if it is gone, ADK pruned it and the model has no record that a bash
// command may have run.
func TestInterruptedCallReachesTheProvider(t *testing.T) {
	callPresent, textPresent := runSeededSession(t, false)
	t.Logf("interrupted call present in provider request: %v", callPresent)
	t.Logf("interrupting prompt present in provider request: %v", textPresent)
	if !textPresent {
		t.Fatal("harness is wrong: the interrupting prompt did not reach the model")
	}
	if !callPresent {
		t.Fatal("the interrupted bash call was dropped from the history sent to the provider")
	}
}

// TestAnsweredCallReachesTheProvider is the control for the test above: the
// same session with only the FunctionResponse added. It proves the assertion
// can pass, so a red result in the first test means "the call was dropped" and
// not "the harness never puts calls in front of the model at all".
func TestAnsweredCallReachesTheProvider(t *testing.T) {
	callPresent, _ := runSeededSession(t, true)
	t.Logf("answered call present in provider request: %v", callPresent)
	if !callPresent {
		t.Fatal("control failed: even an answered call never reached the model")
	}
}

// TestTailInterruptedCallSurvivesRestart covers the other recorded shape: the
// turn was cut off as the LAST thing in the session, so the dangling call is
// the tail of events.jsonl rather than something a later prompt moved past.
// This is the 180 session-tail cases in the measurement, and the one a restart
// has to repair: the process that would have answered the call is gone.
func TestTailInterruptedCallSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := svc.Create(ctx, &session.CreateRequest{AppName: AppName, UserID: DefaultUserID})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.Session.ID()

	mustAppend(ctx, t, svc, created.Session, &session.Event{
		Timestamp: time.Now(), Author: "user",
		LLMResponse: model.LLMResponse{Content: genai.NewContentFromText("deploy", genai.RoleUser)},
	})
	mustAppend(ctx, t, svc, created.Session, &session.Event{
		Timestamp: time.Now(), Author: "pi",
		LLMResponse: model.LLMResponse{Content: &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
				ID: "call_tail_1", Name: "bash",
				Args: map[string]any{"command": "kubectl apply -f harness.yaml"},
			}}},
		}},
	})
	// Nothing follows: the process died here.

	// A fresh service, as a restart would build.
	svc2, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &captureLLM{name: "capture"}
	a, err := New(Config{Model: m, Instruction: "t", SessionService: svc2})
	if err != nil {
		t.Fatal(err)
	}
	for range a.Run(ctx, sid, "what happened?") {
	}

	m.mu.Lock()
	seen := m.reqs
	m.mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("model was never called")
	}
	var callPresent bool
	for _, c := range seen {
		for _, p := range c.Parts {
			if p.FunctionCall != nil && p.FunctionCall.ID == "call_tail_1" {
				callPresent = true
			}
		}
	}
	t.Logf("tail dangling call present after restart: %v", callPresent)
	if !callPresent {
		t.Fatal("a session-tail interrupted call was dropped after a restart")
	}
}

// runSeededSession builds the recorded shape of an interrupted turn — prompt,
// model bash call, next user prompt — and reports whether the call and the
// interrupting prompt reached the provider. withResponse appends the
// FunctionResponse the interrupted turn never produced.
func runSeededSession(t *testing.T, withResponse bool) (callPresent, textPresent bool) {
	t.Helper()
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	created, err := svc.Create(ctx, &session.CreateRequest{AppName: AppName, UserID: DefaultUserID})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.Session.ID()

	// 1. The user's first prompt.
	mustAppend(ctx, t, svc, created.Session, &session.Event{
		Timestamp: time.Now(),
		Author:    "user",
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText("deploy the clawd harness", genai.RoleUser),
		},
	})
	// 2. The model issues a bash call.
	mustAppend(ctx, t, svc, created.Session, &session.Event{
		Timestamp: time.Now(),
		Author:    "pi",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{
					ID:   "call_interrupted_1",
					Name: "bash",
					Args: map[string]any{"command": "kubectl apply -f harness.yaml"},
				},
			}}},
		},
	})
	if withResponse {
		mustAppend(ctx, t, svc, created.Session, &session.Event{
			Timestamp: time.Now(),
			Author:    "pi",
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       "call_interrupted_1",
						Name:     "bash",
						Response: map[string]any{"exit_code": 0, "stdout": "applied"},
					},
				}}},
			},
		})
	}
	// 3. The turn is cut off — NO FunctionResponse. The user's next prompt
	//    arrives instead, which is what steering does.
	mustAppend(ctx, t, svc, created.Session, &session.Event{
		Timestamp: time.Now(),
		Author:    "user",
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText("actually use the staging cluster", genai.RoleUser),
		},
	})

	m := &captureLLM{name: "capture"}
	a, err := New(Config{
		Model:          m,
		Instruction:    "t",
		SessionService: svc,
	})
	if err != nil {
		t.Fatal(err)
	}

	for range a.Run(ctx, sid, "status?") {
	}

	m.mu.Lock()
	seen := m.reqs
	m.mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("model was never called")
	}

	for _, c := range seen {
		for _, p := range c.Parts {
			if p.FunctionCall != nil && p.FunctionCall.ID == "call_interrupted_1" {
				callPresent = true
			}
			if strings.Contains(p.Text, "actually use the staging cluster") {
				textPresent = true
			}
		}
	}
	return callPresent, textPresent
}

func mustAppend(ctx context.Context, t *testing.T, svc *pisession.FileService, s session.Session, ev *session.Event) {
	t.Helper()
	if err := svc.AppendEvent(ctx, s, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}
