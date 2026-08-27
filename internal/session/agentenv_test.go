package session

import (
	"strings"
	"testing"
)

func TestAgentContextFromEnvAbsentForInteractiveSessions(t *testing.T) {
	// No PI_AGENT_* variables set: an ordinary session must record nothing, the
	// same way PlanContext stays absent.
	for _, k := range []string{EnvAgentID, EnvAgentType, EnvRunID, EnvSpecName, EnvParentSession, EnvAgentBranch, EnvSlice, EnvCycle, EnvWorktreeRoot} {
		t.Setenv(k, "")
	}
	if got := AgentContextFromEnv(); got != nil {
		t.Errorf("AgentContextFromEnv() = %+v, want nil for an unattributed process", got)
	}
}

// A worktree path alone is not attribution: the orchestrator sets it for every
// spawned agent, and it answers none of the questions the block exists for.
func TestAgentContextFromEnvIgnoresWorktreeAlone(t *testing.T) {
	t.Setenv(EnvWorktreeRoot, "/repo/.pi-go/tasks/763098722000")
	if got := AgentContextFromEnv(); got != nil {
		t.Errorf("AgentContextFromEnv() = %+v, want nil when only the worktree is set", got)
	}
}

func TestAgentContextFromEnv(t *testing.T) {
	t.Setenv(EnvAgentID, "agent-7")
	t.Setenv(EnvAgentType, "worker")
	t.Setenv(EnvRunID, "run-features-x-123")
	t.Setenv(EnvSpecName, "features/TOO/024-mistral-provider")
	t.Setenv(EnvParentSession, "260827-0205-03249-00fa4")
	t.Setenv(EnvSlice, "3")
	t.Setenv(EnvCycle, "2")
	t.Setenv(EnvWorktreeRoot, "/repo/.pi-go/tasks/x")
	t.Setenv(EnvAgentBranch, "run/features-TOO-024")

	got := AgentContextFromEnv()
	if got == nil {
		t.Fatal("AgentContextFromEnv() = nil, want a populated context")
	}
	want := AgentContext{
		AgentID: "agent-7", AgentType: "worker", RunID: "run-features-x-123",
		SpecName: "features/TOO/024-mistral-provider", ParentID: "260827-0205-03249-00fa4",
		Slice: 3, Cycle: 2, Worktree: "/repo/.pi-go/tasks/x", Branch: "run/features-TOO-024",
	}
	if *got != want {
		t.Errorf("AgentContextFromEnv() = %+v, want %+v", *got, want)
	}
}

func TestAgentContextFromEnvIgnoresUnparseableNumbers(t *testing.T) {
	t.Setenv(EnvAgentID, "agent-7")
	t.Setenv(EnvSlice, "not-a-number")
	t.Setenv(EnvCycle, "-3")

	got := AgentContextFromEnv()
	if got == nil {
		t.Fatal("nil context")
	}
	if got.Slice != 0 || got.Cycle != 0 {
		t.Errorf("Slice/Cycle = %d/%d, want 0/0 for unparseable values", got.Slice, got.Cycle)
	}
}

func TestAgentContextEnvRoundTrip(t *testing.T) {
	src := &AgentContext{
		AgentID: "a1", AgentType: "worker", RunID: "r1", SpecName: "features/x",
		ParentID: "p1", Slice: 4, Cycle: 1, Branch: "run/x",
	}
	for _, kv := range src.Env() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", kv)
		}
		t.Setenv(k, v)
	}
	// Worktree is set by the orchestrator, not by Env; clear it so the
	// round-trip compares like with like.
	t.Setenv(EnvWorktreeRoot, "")

	got := AgentContextFromEnv()
	if got == nil {
		t.Fatal("round trip produced nil")
	}
	if *got != *src {
		t.Errorf("round trip = %+v, want %+v", *got, *src)
	}
}

func TestAgentContextEnvOmitsEmptyFields(t *testing.T) {
	got := (&AgentContext{AgentID: "a1"}).Env()
	if len(got) != 1 || got[0] != EnvAgentID+"=a1" {
		t.Errorf("Env() = %v, want only the set field", got)
	}
	if (*AgentContext)(nil).Env() != nil {
		t.Error("nil context produced env entries")
	}
}

func TestUpdateAndGetAgentContext(t *testing.T) {
	svc := newTestService(t)
	sessionID := createTestSession(t, svc)

	want := &AgentContext{
		AgentID: "agent-7", AgentType: "worker", RunID: "run-1",
		SpecName: "features/x", Slice: 3, Cycle: 1,
		Worktree: "/repo/.pi-go/tasks/x", Branch: "run/features-x",
	}
	if err := svc.UpdateAgentContext(sessionID, want); err != nil {
		t.Fatalf("UpdateAgentContext: %v", err)
	}
	got, err := svc.GetAgentContext(sessionID)
	if err != nil {
		t.Fatalf("GetAgentContext: %v", err)
	}
	if got == nil || *got != *want {
		t.Errorf("GetAgentContext = %+v, want %+v", got, want)
	}

	if err := svc.UpdateAgentContext(sessionID, nil); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if got, _ := svc.GetAgentContext(sessionID); got != nil {
		t.Errorf("context = %+v after clearing, want nil", got)
	}
}

func TestAgentContextUnknownSession(t *testing.T) {
	svc := newTestService(t)
	if err := svc.UpdateAgentContext("nope", &AgentContext{AgentID: "a"}); err == nil {
		t.Error("UpdateAgentContext on an unknown session returned no error")
	}
	if _, err := svc.GetAgentContext("nope"); err == nil {
		t.Error("GetAgentContext on an unknown session returned no error")
	}
}
