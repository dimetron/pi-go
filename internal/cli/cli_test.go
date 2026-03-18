package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// cliMockLLM returns a fixed text response.
type cliMockLLM struct {
	name     string
	response string
}

func (m *cliMockLLM) Name() string { return m.name }

func (m *cliMockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText(m.response, genai.RoleModel),
		}, nil)
	}
}

// cliToolCallingLLM returns a FunctionCall on first call, then text.
type cliToolCallingLLM struct {
	name         string
	functionCall *genai.FunctionCall
	finalText    string
	callCount    int
	mu           sync.Mutex
}

func (m *cliToolCallingLLM) Name() string { return m.name }

func (m *cliToolCallingLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	call := m.callCount
	m.callCount++
	m.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		var resp *model.LLMResponse
		if call == 0 {
			resp = &model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						{FunctionCall: m.functionCall},
					},
				},
			}
		} else {
			resp = &model.LLMResponse{
				Content: genai.NewContentFromText(m.finalText, genai.RoleModel),
			}
		}
		yield(resp, nil)
	}
}

// captureStdout captures os.Stdout during fn execution.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

// captureStderr captures os.Stderr during fn execution.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

// newTestAgent creates an agent with the given mock LLM for output mode testing.
func newTestAgent(t *testing.T, llm model.LLM) (*agent.Agent, string) {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Model:       llm,
		Instruction: "Test agent.",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sessionID, err := ag.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return ag, sessionID
}

func TestNewRootCmd(t *testing.T) {
	cmd := newRootCmd()

	if cmd.Use != "pi [prompt]" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}

	// Verify flags exist
	flags := []string{"model", "mode", "session", "continue", "smol", "slow", "plan"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag: %s", name)
		}
	}
}

func TestRootCmdNoPromptExitsCleanly(t *testing.T) {
	// With API key set but no prompt in print mode, the CLI should exit cleanly.
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-4o", "--mode", "print"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLI_SmolFlag(t *testing.T) {
	// --smol flag should resolve to smol role model.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")

	// Write a config with smol role.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	cfgDir := filepath.Join(tmpDir, ".pi-go")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{
		"roles": {
			"default": {"model": "claude-sonnet-4-6"},
			"smol": {"model": "gpt-4o-mini", "provider": "openai"}
		}
	}`), 0o644)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--smol", "--mode", "print"})

	// No prompt → exits cleanly. Model should resolve to smol role.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLI_ModelFlagOverridesDefault(t *testing.T) {
	// --model flag overrides the default role model.
	t.Setenv("OPENAI_API_KEY", "test-key")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-4o", "--mode", "print"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLI_RoleFlagsMutuallyExclusive(t *testing.T) {
	// When multiple role flags are set, the switch statement picks one (smol wins due to order).
	// This just verifies no crash occurs.
	t.Setenv("OPENAI_API_KEY", "test-key")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--smol", "--mode", "print"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootCmdDefaultModelNoPrompt(t *testing.T) {
	// Default model is gpt-5.4, so set OpenAI key.
	// No prompt in print mode → should exit cleanly with info message.
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--mode", "print"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootCmdMissingAPIKey(t *testing.T) {
	// Ensure no API keys are set
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-4o", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestProviderEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"gemini", "GOOGLE_API_KEY"},
		{"custom", "CUSTOM_API_KEY"},
	}

	for _, tt := range tests {
		got := providerEnvVar(tt.provider)
		if got != tt.want {
			t.Errorf("providerEnvVar(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestContinueNoSessionError(t *testing.T) {
	// --continue with no previous sessions should error.
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	// Use a temp dir so there are no existing sessions.
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-4o", "--continue", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --continue with no sessions")
	}
	if got := err.Error(); !contains(got, "no previous session") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestContinueResumesLastSession(t *testing.T) {
	// Create a session on disk, then verify --continue finds it.
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, ".pi-go", "sessions")
	svc, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a session.
	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: agent.AppName,
		UserID:  agent.DefaultUserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	createdID := resp.Session.ID()

	// Verify LastSessionID finds it.
	lastID := svc.LastSessionID(agent.AppName, agent.DefaultUserID)
	if lastID != createdID {
		t.Errorf("LastSessionID = %q, want %q", lastID, createdID)
	}
}

func TestSessionFlagValue(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--session", "my-session-id"})
	_ = cmd.ParseFlags([]string{"--session", "my-session-id"})

	val, err := cmd.Flags().GetString("session")
	if err != nil {
		t.Fatal(err)
	}
	if val != "my-session-id" {
		t.Errorf("session flag = %q, want %q", val, "my-session-id")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Output Mode Tests ---

func TestRunPrintTextOutput(t *testing.T) {
	llm := &cliMockLLM{name: "test-print", response: "Hello from the agent!"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		err := runPrint(context.Background(), ag, sessionID, "Say hello", nil)
		if err != nil {
			t.Fatalf("runPrint error: %v", err)
		}
	})

	if !strings.Contains(stdout, "Hello from the agent!") {
		t.Errorf("stdout should contain agent text, got: %q", stdout)
	}
}

func TestRunPrintToolStatusToStderr(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0o644)

	llm := &cliToolCallingLLM{
		name: "test-print-tool",
		functionCall: &genai.FunctionCall{
			ID:   "call-1",
			Name: "read",
			Args: map[string]any{"file_path": testFile},
		},
		finalText: "Done reading.",
	}

	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { sb.Close() })

	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	ag, err := agent.New(agent.Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "Test agent.",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sessionID, _ := ag.CreateSession(ctx)

	stderr := captureStderr(t, func() {
		_ = runPrint(ctx, ag, sessionID, "Read the file", nil)
	})

	if !strings.Contains(stderr, "⚙ tool: read") {
		t.Errorf("stderr should contain tool start status, got: %q", stderr)
	}
	if !strings.Contains(stderr, "✓ tool: read done") {
		t.Errorf("stderr should contain tool done status, got: %q", stderr)
	}
}

func TestRunJSONTextDelta(t *testing.T) {
	llm := &cliMockLLM{name: "test-json", response: "JSON response text"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		err := runJSON(context.Background(), ag, sessionID, "Say hello", nil)
		if err != nil {
			t.Fatalf("runJSON error: %v", err)
		}
	})

	// Parse each line as JSON event.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 JSONL lines (message_start, text_delta, message_end), got %d: %q", len(lines), stdout)
	}

	// First event should be message_start.
	var first jsonEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("failed to parse first JSONL line: %v", err)
	}
	if first.Type != "message_start" {
		t.Errorf("first event type = %q, want %q", first.Type, "message_start")
	}
	if first.Role == "" {
		t.Error("message_start should have a role field")
	}

	// Should have at least one text_delta event.
	hasTextDelta := false
	for _, line := range lines[1 : len(lines)-1] {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("failed to parse JSONL line: %v", err)
		}
		if ev.Type == "text_delta" {
			hasTextDelta = true
			if ev.Delta == "" {
				t.Error("text_delta event should have non-empty delta field")
			}
		}
	}
	if !hasTextDelta {
		t.Error("expected at least one text_delta event")
	}

	// Last event should be message_end.
	var last jsonEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("failed to parse last JSONL line: %v", err)
	}
	if last.Type != "message_end" {
		t.Errorf("last event type = %q, want %q", last.Type, "message_end")
	}
}

func TestRunJSONToolCallEvents(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("file content"), 0o644)

	llm := &cliToolCallingLLM{
		name: "test-json-tool",
		functionCall: &genai.FunctionCall{
			ID:   "call-1",
			Name: "read",
			Args: map[string]any{"file_path": testFile},
		},
		finalText: "Read complete.",
	}

	sb2, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { sb2.Close() })

	coreTools, err := tools.CoreTools(sb2)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	ag, err := agent.New(agent.Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "Test agent.",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx := context.Background()
	sessionID, _ := ag.CreateSession(ctx)

	stdout := captureStdout(t, func() {
		err := runJSON(ctx, ag, sessionID, "Read the file", nil)
		if err != nil {
			t.Fatalf("runJSON error: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSpace(stdout), "\n")

	// Collect event types in order.
	types := make([]string, 0, len(lines))
	hasToolCall := false
	hasToolResult := false
	for _, line := range lines {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("failed to parse JSONL: %v", err)
		}
		types = append(types, ev.Type)
		if ev.Type == "tool_call" {
			hasToolCall = true
			if ev.ToolName != "read" {
				t.Errorf("tool_call tool_name = %q, want %q", ev.ToolName, "read")
			}
		}
		if ev.Type == "tool_result" {
			hasToolResult = true
			if ev.ToolName != "read" {
				t.Errorf("tool_result tool_name = %q, want %q", ev.ToolName, "read")
			}
		}
	}

	if !hasToolCall {
		t.Error("expected a tool_call event")
	}
	if !hasToolResult {
		t.Error("expected a tool_result event")
	}

	// First event should be message_start, last should be message_end.
	if types[0] != "message_start" {
		t.Errorf("first event = %q, want message_start", types[0])
	}
	if types[len(types)-1] != "message_end" {
		t.Errorf("last event = %q, want message_end", types[len(types)-1])
	}
}

func TestRunJSONValidJSONL(t *testing.T) {
	llm := &cliMockLLM{name: "test-jsonl-valid", response: "Valid JSON test"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		_ = runJSON(context.Background(), ag, sessionID, "Test", nil)
	})

	// Every line should be valid JSON.
	for i, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Errorf("line %d is not valid JSON: %q", i, line)
		}
	}
}
