package cursor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

func TestRunnerStartRejectsEmptyPrompt(t *testing.T) {
	runner := Runner{}
	if _, err := runner.Start(context.Background(), RunRequest{}); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestRunnerFindsBinary(t *testing.T) {
	if _, err := findBinary([]string{"nonexistent-binary-xyz"}); err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	path, err := findBinary([]string{"ls"})
	if err != nil {
		t.Fatalf("findBinary(ls) failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path for ls")
	}
}

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
			if tt.wantErr && err == nil {
				t.Errorf("Start() error = nil, wantErr true")
			}
		})
	}
}

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

func TestClientInfoCustom(t *testing.T) {
	runner := Runner{ClientInfo: acp.Implementation{Name: "test-client", Version: "1.0.0"}}
	info := runner.clientInfo()
	if info.Name != "test-client" {
		t.Errorf("expected name 'test-client', got %q", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", info.Version)
	}
}

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name              string
		req               RunRequest
		includeSubcommand bool
		want              []string
	}{
		{
			name:              "defaults with subcommand",
			req:               RunRequest{Prompt: "hi"},
			includeSubcommand: true,
			want:              []string{"acp"},
		},
		{
			name:              "with endpoint",
			req:               RunRequest{Prompt: "hi", Endpoint: "https://api2.cursor.sh"},
			includeSubcommand: true,
			want:              []string{"-e", "https://api2.cursor.sh", "acp"},
		},
		{
			name:              "with api key",
			req:               RunRequest{Prompt: "hi", APIKey: "sk-123"},
			includeSubcommand: true,
			want:              []string{"--api-key", "sk-123", "acp"},
		},
		{
			name:              "with auth token",
			req:               RunRequest{Prompt: "hi", AuthToken: "tok-abc"},
			includeSubcommand: true,
			want:              []string{"--auth-token", "tok-abc", "acp"},
		},
		{
			name:              "all auth flags",
			req:               RunRequest{Prompt: "hi", APIKey: "k", AuthToken: "t", Endpoint: "https://ep"},
			includeSubcommand: true,
			want:              []string{"-e", "https://ep", "--api-key", "k", "--auth-token", "t", "acp"},
		},
		{
			name:              "no subcommand when test overrides argv",
			req:               RunRequest{Prompt: "hi", APIKey: "k"},
			includeSubcommand: false,
			want:              []string{"--api-key", "k"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArgs(tt.req, tt.includeSubcommand)
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultBinaryPathsPrefersCursorAgent(t *testing.T) {
	if len(DefaultBinaryPaths) == 0 {
		t.Fatal("DefaultBinaryPaths is empty")
	}
	if DefaultBinaryPaths[0] != "cursor-agent" {
		t.Errorf("first path should be 'cursor-agent', got %q", DefaultBinaryPaths[0])
	}
	// `agent` must appear as a fallback — per Cursor docs the binary is named `agent`.
	if !slices.Contains(DefaultBinaryPaths, "agent") {
		t.Errorf("DefaultBinaryPaths should include 'agent' fallback; got %v", DefaultBinaryPaths)
	}
}

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
			if !filepath.IsAbs(result) {
				t.Errorf("absDir(%q) returned non-absolute path: %q", tt.path, result)
			}
		})
	}
}

func TestContentBlockText(t *testing.T) {
	textBlock := acp.ContentBlock{Text: &acp.ContentBlockText{Text: "hello"}}
	if got := contentBlockText(textBlock); got != "hello" {
		t.Errorf("contentBlockText() = %q, want %q", got, "hello")
	}
	if got := contentBlockText(acp.ContentBlock{}); got != "" {
		t.Errorf("contentBlockText(empty) = %q, want empty", got)
	}
}

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
		if got := stopReasonText(tt.reason); got != tt.expect {
			t.Errorf("stopReasonText(%v) = %q, want %q", tt.reason, got, tt.expect)
		}
	}
}

func TestContentBlockTextResourceLink(t *testing.T) {
	// Test ResourceLink block returns the URI
	linkBlock := acp.ContentBlock{ResourceLink: &acp.ContentBlockResourceLink{Uri: "https://example.com/file.txt"}}
	if got := contentBlockText(linkBlock); got != "https://example.com/file.txt" {
		t.Errorf("contentBlockText(ResourceLink) = %q, want %q", got, "https://example.com/file.txt")
	}

	// Test empty ResourceLink
	emptyLinkBlock := acp.ContentBlock{ResourceLink: &acp.ContentBlockResourceLink{Uri: ""}}
	if got := contentBlockText(emptyLinkBlock); got != "" {
		t.Errorf("contentBlockText(empty ResourceLink) = %q, want empty", got)
	}
}

func TestAppendResultEmptyString(t *testing.T) {
	session := &RunningSession{
		toolFilter: shared.NewToolCallTitleFilter(func(string) {}),
	}
	// Append with existing content
	session.appendResult("Hello ")
	// Appending empty string should be a no-op
	session.appendResult("")
	if session.result.Result != "Hello " {
		t.Errorf("result = %q, want %q after empty append", session.result.Result, "Hello ")
	}
}

func TestRunningSessionCancelAlreadyFinished(t *testing.T) {
	session := &RunningSession{
		finished: true,
		cmd:      &exec.Cmd{},
	}
	// When finished=true, Cancel() should return nil immediately
	if err := session.Cancel(); err != nil {
		t.Errorf("Cancel() on finished session error = %v, want nil", err)
	}
}

func TestRunningSessionCancelNilProcess(t *testing.T) {
	cmd := &exec.Cmd{}
	session := &RunningSession{
		cmd: cmd, // cmd.Process is nil by default
	}
	// When cmd.Process is nil, Cancel() should return nil
	if err := session.Cancel(); err != nil {
		t.Errorf("Cancel() with nil cmd.Process error = %v, want nil", err)
	}
}

func TestRunnerStartUsesCommandField(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "echo_args.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/bash
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"test","version":"1.0"}}}\n'
printf '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"test-session"}}\n'
printf '{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}\n'
exit 0
`), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := Runner{}
	req := RunRequest{
		Prompt:  "test",
		Command: []string{"/bin/bash", scriptPath},
	}
	session, err := runner.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Cancel()

	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not complete in time")
	}
}

func TestRunnerStartBinaryNotFound(t *testing.T) {
	runner := Runner{Binary: "/nonexistent/path/to/cursor-agent-xyz123"}
	_, err := runner.Start(context.Background(), RunRequest{Prompt: "test"})
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if !strings.Contains(err.Error(), "finding cursor-agent") {
		t.Errorf("error should wrap 'finding cursor-agent', got: %v", err)
	}
}

func TestRunnerStartSubprocessFails(t *testing.T) {
	runner := Runner{Binary: "sh"}
	session, err := runner.Start(context.Background(), RunRequest{
		Prompt:  "test",
		Command: []string{"sh", "-c", "exit 1"}, // Exit immediately after start
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not complete in time")
	}

	result := session.Wait()
	if result.Status != shared.StatusError {
		t.Fatalf("result.Status = %s, want %s (error=%q)", result.Status, shared.StatusError, result.Error)
	}
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("expected non-empty error when subprocess exits immediately")
	}
}
