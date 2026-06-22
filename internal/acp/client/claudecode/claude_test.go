package claudecode

import (
	"context"
	"fmt"
	"os"
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

// ---------------------------------------------------------------------------
// resolveCommand table-driven tests
// ---------------------------------------------------------------------------

func TestResolveCommand(t *testing.T) {
	// Create a temporary binary that exists on disk for stat-based branches.
	tmpDir := t.TempDir()
	absBin := filepath.Join(tmpDir, "fake-bin")
	if err := os.WriteFile(absBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: write %s: %v", absBin, err)
	}
	relBin := filepath.Join(".", "fake-bin-test-rel")
	if err := os.WriteFile(relBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: write %s: %v", relBin, err)
	}
	t.Cleanup(func() { _ = os.Remove(relBin) })

	tests := []struct {
		name     string
		binary   string   // Runner.Binary
		cmd      []string // RunRequest.Command
		envVal   string   // PI_ACP_CLAUDE_CMD value ("" means unset)
		wantBin  string   // expected binary (exact match)
		wantArgs []string // expected args
		wantErr  bool
	}{
		// --- Branch 1: Runner.Binary is set ---
		{
			name:     "binary set overrides everything",
			binary:   absBin,
			cmd:      []string{"should-not-be-used"},
			envVal:   "should-not-be-used",
			wantBin:  absBin,
			wantArgs: []string{},
		},
		{
			name:     "binary set with custom args empty",
			binary:   "/usr/local/bin/custom-claude",
			wantBin:  "/usr/local/bin/custom-claude",
			wantArgs: []string{},
		},

		// --- Branch 2: RunRequest.Command set ---
		{
			name:     "command single element",
			cmd:      []string{"/bin/echo"},
			envVal:   "should-not-be-used",
			wantBin:  "/bin/echo",
			wantArgs: []string{},
		},
		{
			name:     "command multiple elements",
			cmd:      []string{"/bin/echo", "hello", "world"},
			envVal:   "should-not-be-used",
			wantBin:  "/bin/echo",
			wantArgs: []string{"hello", "world"},
		},

		// --- Branch 3: PI_ACP_CLAUDE_CMD env override ---
		{
			name:     "env override binary only",
			envVal:   "/usr/bin/my-claude",
			wantBin:  "/usr/bin/my-claude",
			wantArgs: []string{},
		},
		{
			name:     "env override with args",
			envVal:   "/usr/bin/my-claude --flag value",
			wantBin:  "/usr/bin/my-claude",
			wantArgs: []string{"--flag", "value"},
		},

		// --- Branch 4: findBinary fallback ---
		// This uses DefaultBinaryPaths — only assert when bunx is available.
		// (Handled separately in TestResolveCommandUsesDefaultCommandArgs.)

		// --- findBinary fallback with no match ---
		{
			name:    "no binary no command no env not found",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv(envACPClaudeCmd, tt.envVal)
			} else {
				t.Setenv(envACPClaudeCmd, "")
			}

			// For the "no match" case, redirect DefaultBinaryPaths to something
			// that won't exist so we can deterministically get an error.
			if tt.wantErr {
				origPaths := DefaultBinaryPaths
				DefaultBinaryPaths = []string{"definitely-nonexistent-binary-xyz123"}
				t.Cleanup(func() { DefaultBinaryPaths = origPaths })
			}

			r := Runner{Binary: tt.binary}
			gotBin, gotArgs, err := r.resolveCommand(RunRequest{Command: tt.cmd})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveCommand() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCommand() unexpected error: %v", err)
			}
			if gotBin != tt.wantBin {
				t.Errorf("resolveCommand() binary = %q, want %q", gotBin, tt.wantBin)
			}
			if fmt.Sprint(gotArgs) != fmt.Sprint(tt.wantArgs) {
				t.Errorf("resolveCommand() args = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}

// TestResolveCommandBinarySetWithArgs verifies that when Binary is set, the
// command args list is empty (binary takes priority over Command and env).
func TestResolveCommandBinarySetWithArgs(t *testing.T) {
	r := Runner{Binary: "/usr/local/bin/bunx"}
	bin, args, err := r.resolveCommand(RunRequest{
		Command: []string{"ignored", "also-ignored"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bin != "/usr/local/bin/bunx" {
		t.Fatalf("binary = %q, want /usr/local/bin/bunx", bin)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty", args)
	}
}

// ---------------------------------------------------------------------------
// findBinary table-driven tests
// ---------------------------------------------------------------------------

func TestFindBinary(t *testing.T) {
	// Create a temp dir with a binary that exists on disk.
	tmpDir := t.TempDir()
	absPath := filepath.Join(tmpDir, "fake-binary")
	if err := os.WriteFile(absPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Create a relative path binary.
	relPath := "./fake-rel-binary-test"
	if err := os.WriteFile(relPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: write %s: %v", relPath, err)
	}
	t.Cleanup(func() { _ = os.Remove(relPath) })

	tests := []struct {
		name    string
		paths   []string
		want    string
		wantErr bool
	}{
		// empty string skip
		{
			name:  "empty string skipped falls through to PATH lookup",
			paths: []string{"", "ls"},
			want:  "", // resolved via LookPath — just check no error
		},
		{
			name:    "all empty strings returns error",
			paths:   []string{"", "", ""},
			wantErr: true,
		},
		// absolute path via os.Stat success
		{
			name:  "absolute path exists",
			paths: []string{absPath},
			want:  absPath,
		},
		// absolute path via os.Stat failure → continues → error
		{
			name:    "absolute path does not exist",
			paths:   []string{filepath.Join(tmpDir, "nonexistent")},
			wantErr: true,
		},
		// relative path via os.Stat success
		{
			name:  "relative path exists",
			paths: []string{relPath},
			want:  relPath,
		},
		// relative path via os.Stat failure → continues → error
		{
			name:    "relative path does not exist",
			paths:   []string{"./nonexistent-rel-binary-xyz"},
			wantErr: true,
		},
		// exec.LookPath success (bare name)
		{
			name:  "bare name found in PATH",
			paths: []string{"ls"},
			want:  "", // resolved path — just check no error and non-empty
		},
		// exec.LookPath failure
		{
			name:    "bare name not in PATH",
			paths:   []string{"nonexistent-binary-xyz-123"},
			wantErr: true,
		},
		// multiple entries — first match wins (skip empty + missing abs)
		{
			name:  "skip empty and missing then find in PATH",
			paths: []string{"", "/nonexistent/abs/path", "ls"},
			want:  "",
		},
		// absolute path found before PATH lookup
		{
			name:  "absolute found before PATH fallback",
			paths: []string{absPath, "ls"},
			want:  absPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findBinary(tt.paths)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("findBinary() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("findBinary() unexpected error: %v", err)
			}
			if tt.want != "" {
				if got != tt.want {
					t.Errorf("findBinary() = %q, want %q", got, tt.want)
				}
			}
			if got == "" {
				t.Error("findBinary() returned empty path without error")
			}
		})
	}
}
