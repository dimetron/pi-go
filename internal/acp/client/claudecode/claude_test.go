package claudecode

import (
	"context"
	"fmt"
	"os"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// TestRunnerStartRejectsEmptyPrompt verifies that an empty prompt is rejected.
func TestRunnerStartRejectsEmptyPrompt(t *testing.T) {
	runner := Runner{}
	_, err := runner.Start(context.Background(), RunRequest{})
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

// TestRunnerFindsBinary verifies binary lookup works correctly.
func TestRunnerFindsBinary(t *testing.T) {
	// Test that findBinary returns error when binary not found.
	_, err := findBinary([]string{"nonexistent-binary-xyz"})
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}

	// Test that it finds an existing binary in PATH.
	path, err := findBinary([]string{"ls"})
	if err != nil {
		t.Fatalf("findBinary(ls) failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path for ls")
	}
}

// TestRunRequestValidation verifies prompt validation through Start.
func TestRunRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     RunRequest
		wantErr bool
	}{
		{"empty prompt", RunRequest{Prompt: ""}, true},
		{"whitespace only prompt", RunRequest{Prompt: "   "}, true},
		{"valid prompt with binary", RunRequest{
			Prompt:  "Hello",
			Command: []string{"/bin/true"},
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := Runner{}
			_, err := runner.Start(context.Background(), tt.req)
			// Only check error for validation cases; binary not found is expected
			if tt.wantErr && err == nil {
				t.Errorf("Start() error = nil, wantErr true")
			}
		})
	}
}

// TestResolveCommandUsesDefaultCommandArgs verifies the bunx launcher args are
// appended when resolving via DefaultBinaryPaths.
func TestResolveCommandUsesDefaultCommandArgs(t *testing.T) {
	// Force the DefaultBinaryPaths branch by leaving Binary/Command/env unset.
	t.Setenv(envACPClaudeCmd, "")
	r := Runner{}
	_, args, err := r.resolveCommand(RunRequest{Prompt: "hi"})
	if err != nil {
		// bunx may be absent on CI; only assert when resolution succeeds.
		return
	}
	if fmt.Sprint(args) != fmt.Sprint(DefaultCommand[1:]) {
		t.Fatalf("args = %v, want %v", args, DefaultCommand[1:])
	}
}

// TestHelperACPAgent provides a test helper agent for integration testing.
type TestHelperAgent struct{}

func (TestHelperAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (TestHelperAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (TestHelperAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &acp.Implementation{Name: "test-helper", Version: "test"},
	}, nil
}

func (TestHelperAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.NewSessionResponse{SessionId: acp.SessionId("test-session")}, nil
}

func (TestHelperAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (TestHelperAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	// Echo back the prompt content for testing purposes.
	_ = ctx
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (TestHelperAgent) Cancel(context.Context, acp.CancelNotification) error {
	return nil
}

func (TestHelperAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (TestHelperAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (TestHelperAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (TestHelperAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

// runTestHelperAgent runs the test helper agent from TestMain (below) when
// GO_WANT_HELPER_PROCESS=1. Calling os.Exit inside a running test function
// panics since Go 1.26, so this path lives in TestMain where os.Exit is safe.
func runTestHelperAgent() error {
	agentConn := acp.NewAgentSideConnection(TestHelperAgent{}, os.Stdout, os.Stdin)
	<-agentConn.Done()
	return nil
}

// TestMain intercepts helper-process invocations before the testing framework
// starts, so the ACP dance can complete without test-runtime output (PASS,
// coverage:...) corrupting the stdout pipe the parent reads as JSON-RPC.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		if err := runTestHelperAgent(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestRunnerWithHelperAgent tests the Runner with a test helper ACP agent.
func TestRunnerWithHelperAgent(t *testing.T) {
	runner := Runner{}
	cmd := []string{os.Args[0], "--"}
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")

	session, err := runner.Start(context.Background(), RunRequest{
		Prompt:  "test prompt",
		CWD:     t.TempDir(),
		Command: cmd,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var events []shared.Event
	for event := range session.Events() {
		events = append(events, event)
	}

	result := session.Wait()
	if result.Status != shared.StatusSuccess {
		t.Fatalf("status = %q, want %q (error=%q)", result.Status, shared.StatusSuccess, result.Error)
	}
	if result.SessionID != "test-session" {
		t.Fatalf("session id = %q, want test-session", result.SessionID)
	}
	_ = events // events may be empty depending on helper agent
}

// TestBinaryPaths verifies default binary paths are reasonable.
func TestBinaryPaths(t *testing.T) {
	if len(DefaultBinaryPaths) == 0 {
		t.Fatal("DefaultBinaryPaths should not be empty")
	}
	for _, path := range DefaultBinaryPaths {
		if path == "" {
			t.Error("DefaultBinaryPaths contains empty string")
		}
	}
}
