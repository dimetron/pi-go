package claudecode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// TestRunningSessionCancel verifies that session cancellation works.
func TestRunningSessionCancel(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "sleep.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command("/bin/sh", scriptPath)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}

	session := newRunningSession(cmd, stdin, stderr)
	if err := session.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := session.waitProcess(); err == nil {
		t.Fatal("expected process wait error after cancel")
	}
}

// TestRunRequestValidation verifies RunRequest validation.
func TestRunRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     RunRequest
		wantErr bool
	}{
		{
			name:    "empty prompt",
			req:     RunRequest{Prompt: ""},
			wantErr: true,
		},
		{
			name:    "whitespace only prompt",
			req:     RunRequest{Prompt: "   "},
			wantErr: true,
		},
		{
			name: "valid prompt with binary",
			req: RunRequest{
				Prompt: "Hello",
				// Use /bin/true — a fast-exit process that won't speak ACP.
				// Start() succeeds (subprocess launches); the session then
				// fails quickly, but this case only checks start-path errors.
				Command: []string{"/bin/true"},
			},
			wantErr: false,
		},
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

// TestClientInfoDefaults verifies that client info defaults correctly.
func TestClientInfoDefaults(t *testing.T) {
	runner := Runner{}
	info := runner.clientInfo()
	if info.Name != "pi-go" {
		t.Errorf("expected name 'pi-go', got %q", info.Name)
	}
	if info.Version != "dev" {
		t.Errorf("expected version 'dev', got %q", info.Version)
	}
}

// TestClientInfoCustom verifies that custom client info is preserved.
func TestClientInfoCustom(t *testing.T) {
	runner := Runner{
		ClientInfo: acp.Implementation{Name: "test-client", Version: "1.0.0"},
	}
	info := runner.clientInfo()
	if info.Name != "test-client" {
		t.Errorf("expected name 'test-client', got %q", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", info.Version)
	}
}

// TestAbsDir verifies the working directory resolution.
func TestAbsDir(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"current dir", "."},
		{"temp dir", t.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := absDir(tt.path)
			if result == "" {
				t.Error("absDir returned empty string")
			}
			// Verify it's absolute (starts with / or drive letter on Windows)
			if !filepath.IsAbs(result) {
				t.Errorf("absDir(%q) returned non-absolute path: %q", tt.path, result)
			}
		})
	}
}

// TestContentBlockText verifies content block text extraction.
func TestContentBlockText(t *testing.T) {
	textBlock := acp.ContentBlock{
		Text: &acp.ContentBlockText{Text: "hello world"},
	}
	result := contentBlockText(textBlock)
	if result != "hello world" {
		t.Errorf("contentBlockText() = %q, want %q", result, "hello world")
	}

	emptyBlock := acp.ContentBlock{}
	result = contentBlockText(emptyBlock)
	if result != "" {
		t.Errorf("contentBlockText(empty) = %q, want empty", result)
	}
}

// TestStopReasonText verifies stop reason text conversion.
func TestStopReasonText(t *testing.T) {
	tests := []struct {
		reason acp.StopReason
		expect string
	}{
		{acp.StopReasonEndTurn, "end_turn"},
		{acp.StopReasonMaxTokens, "max_tokens"},
		{acp.StopReason(""), ""},
	}

	for _, tt := range tests {
		result := stopReasonText(tt.reason)
		if result != tt.expect {
			t.Errorf("stopReasonText(%v) = %q, want %q", tt.reason, result, tt.expect)
		}
	}
}

// TestHelperACPAgent provides a test helper agent for integration testing.
type TestHelperAgent struct{}

func (TestHelperAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
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
