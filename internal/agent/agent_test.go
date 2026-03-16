package agent

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/tools"
)

func testSandbox(t *testing.T, dir string) *tools.Sandbox {
	t.Helper()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox(%s): %v", dir, err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

// mockLLM implements model.LLM for testing.
type mockLLM struct {
	name     string
	response string
}

func (m *mockLLM) Name() string { return m.name }

func (m *mockLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp := &model.LLMResponse{
			Content: genai.NewContentFromText(m.response, genai.RoleModel),
		}
		yield(resp, nil)
	}
}

// toolCallingLLM returns a FunctionCall on the first invocation,
// then returns a text response on the second (after the tool result).
type toolCallingLLM struct {
	name         string
	callCount    int
	mu           sync.Mutex
	functionCall *genai.FunctionCall
	finalText    string
}

func (m *toolCallingLLM) Name() string { return m.name }

func (m *toolCallingLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	call := m.callCount
	m.callCount++
	m.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		var resp *model.LLMResponse
		if call == 0 {
			// First call: return a function call
			resp = &model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						{FunctionCall: m.functionCall},
					},
				},
			}
		} else {
			// Subsequent calls: return final text
			resp = &model.LLMResponse{
				Content: genai.NewContentFromText(m.finalText, genai.RoleModel),
			}
		}
		yield(resp, nil)
	}
}

func TestNew(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{
		Model: llm,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if a == nil {
		t.Fatal("New() returned nil agent")
	}
	if a.runner == nil {
		t.Error("agent.runner is nil")
	}
	if a.sessionService == nil {
		t.Error("agent.sessionService is nil")
	}
}

func TestNewWithCustomInstruction(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{
		Model:       llm,
		Instruction: "Custom instruction for testing.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if a == nil {
		t.Fatal("New() returned nil agent")
	}
}

func TestNewWithCustomSessionService(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}
	svc := session.InMemoryService()

	a, err := New(Config{
		Model:          llm,
		SessionService: svc,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if a.sessionService != svc {
		t.Error("expected custom session service to be used")
	}
}

func TestCreateSession(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{Model: llm})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}
	if sessionID == "" {
		t.Error("CreateSession() returned empty session ID")
	}
}

func TestRun(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "I can help with that!"}

	a, err := New(Config{Model: llm})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	var events []*session.Event
	for event, err := range a.Run(ctx, sessionID, "Hello, agent!") {
		if err != nil {
			t.Fatalf("Run() yielded error: %v", err)
		}
		if event != nil {
			events = append(events, event)
		}
	}

	if len(events) == 0 {
		t.Error("Run() produced no events")
	}

	// Check that at least one event has model content.
	hasModelContent := false
	for _, e := range events {
		if e.Content != nil && e.Content.Role == genai.RoleModel {
			hasModelContent = true
			break
		}
	}
	if !hasModelContent {
		t.Error("Run() produced no events with model content")
	}
}

func TestRunStreaming(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Streamed response!"}

	a, err := New(Config{Model: llm})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	var events []*session.Event
	for event, err := range a.RunStreaming(ctx, sessionID, "Stream this!") {
		if err != nil {
			t.Fatalf("RunStreaming() yielded error: %v", err)
		}
		if event != nil {
			events = append(events, event)
		}
	}

	if len(events) == 0 {
		t.Error("RunStreaming() produced no events")
	}
}

func TestLoadInstruction(t *testing.T) {
	base := "Base instruction."
	result := LoadInstruction(base)

	// Without an AGENTS.md file in cwd, should return base unchanged.
	if result != base {
		t.Errorf("LoadInstruction() = %q, want %q", result, base)
	}
}

func TestLoadInstructionWithAgentsFile(t *testing.T) {
	// Create a temp dir with .pi-go/AGENTS.md
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".pi-go")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "AGENTS.md"), []byte("# Custom Rules\n- Rule 1"), 0o644)

	// Change to temp dir, restore after test
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	base := "Base instruction."
	result := LoadInstruction(base)

	if !strings.Contains(result, "Custom Rules") {
		t.Errorf("LoadInstruction() should contain AGENTS.md content, got %q", result)
	}
	if !strings.Contains(result, base) {
		t.Errorf("LoadInstruction() should contain base instruction, got %q", result)
	}
}

func TestIntegrationToolExecution(t *testing.T) {
	// Create a temp file that the "read" tool will read.
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("hello from test file\n"), 0o644)

	// Mock LLM that calls the "read" tool, then returns final text.
	llm := &toolCallingLLM{
		name: "test-tool-calling",
		functionCall: &genai.FunctionCall{
			ID:   "call-1",
			Name: "read",
			Args: map[string]any{
				"file_path": testFile,
			},
		},
		finalText: "The file contains: hello from test file",
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a test agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	var events []*session.Event
	for event, err := range a.Run(ctx, sessionID, "Read the test file") {
		if err != nil {
			t.Fatalf("Run() yielded error: %v", err)
		}
		if event != nil {
			events = append(events, event)
		}
	}

	if len(events) == 0 {
		t.Fatal("Run() produced no events")
	}

	// Verify the mock LLM was called twice (tool call + final response).
	llm.mu.Lock()
	calls := llm.callCount
	llm.mu.Unlock()
	if calls != 2 {
		t.Errorf("expected LLM called 2 times (tool call + response), got %d", calls)
	}

	// Verify we got both a function call event and a final text event.
	var hasFunctionCall, hasFinalText bool
	for _, e := range events {
		if e.Content == nil {
			continue
		}
		for _, p := range e.Content.Parts {
			if p.FunctionCall != nil && p.FunctionCall.Name == "read" {
				hasFunctionCall = true
			}
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "read" {
				// Tool result was fed back.
			}
			if p.Text != "" && strings.Contains(p.Text, "hello from test file") {
				hasFinalText = true
			}
		}
	}

	if !hasFunctionCall {
		t.Error("expected a function call event for 'read' tool")
	}
	if !hasFinalText {
		t.Error("expected final text response containing tool result")
	}
}

func TestIntegrationBashToolExecution(t *testing.T) {
	dir := t.TempDir()

	// Mock LLM that calls the "bash" tool with "echo hello-integration"
	llm := &toolCallingLLM{
		name: "test-bash-calling",
		functionCall: &genai.FunctionCall{
			ID:   "call-bash-1",
			Name: "bash",
			Args: map[string]any{
				"command": "echo hello-integration",
			},
		},
		finalText: "The command output was: hello-integration",
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a test agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	var events []*session.Event
	for event, err := range a.Run(ctx, sessionID, "Run echo command") {
		if err != nil {
			t.Fatalf("Run() yielded error: %v", err)
		}
		if event != nil {
			events = append(events, event)
		}
	}

	if len(events) == 0 {
		t.Fatal("Run() produced no events")
	}

	// Verify we got a function response with the bash output.
	var hasBashResult bool
	for _, e := range events {
		if e.Content == nil {
			continue
		}
		for _, p := range e.Content.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "bash" {
				hasBashResult = true
			}
		}
	}
	if !hasBashResult {
		t.Error("expected a function response event for 'bash' tool")
	}
}

func TestIntegrationWriteAndReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "output.txt")

	// First agent call: mock LLM calls "write" tool
	writeLLM := &toolCallingLLM{
		name: "test-write",
		functionCall: &genai.FunctionCall{
			ID:   "call-write-1",
			Name: "write",
			Args: map[string]any{
				"file_path": targetFile,
				"content":   "integration test content",
			},
		},
		finalText: "File written successfully.",
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       writeLLM,
		Tools:       coreTools,
		Instruction: "You are a test agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Run the write tool call
	for _, err := range a.Run(ctx, sessionID, "Write a file") {
		if err != nil {
			t.Fatalf("Run() write error: %v", err)
		}
	}

	// Verify the file was actually written to disk
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("file was not written: %v", err)
	}
	if string(data) != "integration test content" {
		t.Errorf("file content = %q, want %q", string(data), "integration test content")
	}
}

func TestIntegrationEditToolExecution(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "edit-target.txt")
	os.WriteFile(targetFile, []byte("hello world"), 0o644)

	llm := &toolCallingLLM{
		name: "test-edit-calling",
		functionCall: &genai.FunctionCall{
			ID:   "call-edit-1",
			Name: "edit",
			Args: map[string]any{
				"file_path":  targetFile,
				"old_string": "hello",
				"new_string": "goodbye",
			},
		},
		finalText: "Replaced hello with goodbye.",
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{Model: llm, Tools: coreTools, Instruction: "Test agent."})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, _ := a.CreateSession(ctx)

	for _, err := range a.Run(ctx, sessionID, "Edit the file") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("file read error: %v", err)
	}
	if string(data) != "goodbye world" {
		t.Errorf("file content = %q, want %q", string(data), "goodbye world")
	}
}

func TestIntegrationGrepToolExecution(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("func main() {\n\tfmt.Println(\"test\")\n}\n"), 0o644)

	llm := &toolCallingLLM{
		name: "test-grep-calling",
		functionCall: &genai.FunctionCall{
			ID:   "call-grep-1",
			Name: "grep",
			Args: map[string]any{
				"pattern": "func",
				"path":    dir,
			},
		},
		finalText: "Found func main.",
	}

	coreTools, _ := tools.CoreTools(testSandbox(t, dir))
	a, _ := New(Config{Model: llm, Tools: coreTools, Instruction: "Test agent."})
	ctx := context.Background()
	sessionID, _ := a.CreateSession(ctx)

	var hasFunctionResponse bool
	for event, err := range a.Run(ctx, sessionID, "Search for func") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if event != nil && event.Content != nil {
			for _, p := range event.Content.Parts {
				if p.FunctionResponse != nil && p.FunctionResponse.Name == "grep" {
					hasFunctionResponse = true
				}
			}
		}
	}
	if !hasFunctionResponse {
		t.Error("expected function response for 'grep' tool")
	}
}

func TestIntegrationFindToolExecution(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)

	llm := &toolCallingLLM{
		name: "test-find-calling",
		functionCall: &genai.FunctionCall{
			ID:   "call-find-1",
			Name: "find",
			Args: map[string]any{
				"pattern": "*.go",
				"path":    dir,
			},
		},
		finalText: "Found main.go.",
	}

	coreTools, _ := tools.CoreTools(testSandbox(t, dir))
	a, _ := New(Config{Model: llm, Tools: coreTools, Instruction: "Test agent."})
	ctx := context.Background()
	sessionID, _ := a.CreateSession(ctx)

	var hasFunctionResponse bool
	for event, err := range a.Run(ctx, sessionID, "Find go files") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if event != nil && event.Content != nil {
			for _, p := range event.Content.Parts {
				if p.FunctionResponse != nil && p.FunctionResponse.Name == "find" {
					hasFunctionResponse = true
				}
			}
		}
	}
	if !hasFunctionResponse {
		t.Error("expected function response for 'find' tool")
	}
}

func TestIntegrationLsToolExecution(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	llm := &toolCallingLLM{
		name: "test-ls-calling",
		functionCall: &genai.FunctionCall{
			ID:   "call-ls-1",
			Name: "ls",
			Args: map[string]any{
				"path": dir,
			},
		},
		finalText: "Directory listing complete.",
	}

	coreTools, _ := tools.CoreTools(testSandbox(t, dir))
	a, _ := New(Config{Model: llm, Tools: coreTools, Instruction: "Test agent."})
	ctx := context.Background()
	sessionID, _ := a.CreateSession(ctx)

	var hasFunctionResponse bool
	for event, err := range a.Run(ctx, sessionID, "List directory") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if event != nil && event.Content != nil {
			for _, p := range event.Content.Parts {
				if p.FunctionResponse != nil && p.FunctionResponse.Name == "ls" {
					hasFunctionResponse = true
				}
			}
		}
	}
	if !hasFunctionResponse {
		t.Error("expected function response for 'ls' tool")
	}
}

// multiToolLLM simulates an LLM that calls two tools sequentially.
type multiToolLLM struct {
	name      string
	calls     []*genai.FunctionCall
	finalText string
	callCount int
	mu        sync.Mutex
}

func (m *multiToolLLM) Name() string { return m.name }

func (m *multiToolLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	call := m.callCount
	m.callCount++
	m.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		var resp *model.LLMResponse
		if call < len(m.calls) {
			resp = &model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						{FunctionCall: m.calls[call]},
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

func TestIntegrationMultiToolChain(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "chain.txt")

	// LLM calls write, then read, then returns text.
	llm := &multiToolLLM{
		name: "test-multi-tool",
		calls: []*genai.FunctionCall{
			{
				ID:   "call-1",
				Name: "write",
				Args: map[string]any{
					"file_path": targetFile,
					"content":   "chained content",
				},
			},
			{
				ID:   "call-2",
				Name: "read",
				Args: map[string]any{
					"file_path": targetFile,
				},
			},
		},
		finalText: "Write and read complete.",
	}

	coreTools, _ := tools.CoreTools(testSandbox(t, dir))
	a, _ := New(Config{Model: llm, Tools: coreTools, Instruction: "Test agent."})
	ctx := context.Background()
	sessionID, _ := a.CreateSession(ctx)

	toolNames := map[string]bool{}
	for event, err := range a.Run(ctx, sessionID, "Write then read a file") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if event != nil && event.Content != nil {
			for _, p := range event.Content.Parts {
				if p.FunctionResponse != nil {
					toolNames[p.FunctionResponse.Name] = true
				}
			}
		}
	}

	if !toolNames["write"] {
		t.Error("expected function response for 'write' tool")
	}
	if !toolNames["read"] {
		t.Error("expected function response for 'read' tool")
	}

	// Verify the file was actually written
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("file read error: %v", err)
	}
	if string(data) != "chained content" {
		t.Errorf("file content = %q, want %q", string(data), "chained content")
	}

	// Verify LLM was called 3 times (write + read + final text)
	llm.mu.Lock()
	calls := llm.callCount
	llm.mu.Unlock()
	if calls != 3 {
		t.Errorf("expected 3 LLM calls, got %d", calls)
	}
}

func TestNewWithTools(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	coreTools, err := tools.CoreTools(testSandbox(t, t.TempDir()))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model: llm,
		Tools: coreTools,
	})
	if err != nil {
		t.Fatalf("New() with tools error: %v", err)
	}
	if a == nil {
		t.Fatal("New() returned nil agent")
	}
}

func TestRebuildWithInstruction(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{Model: llm, Instruction: "Original instruction."})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Rebuild with new instruction
	err = a.RebuildWithInstruction("New custom instruction.")
	if err != nil {
		t.Fatalf("RebuildWithInstruction() error: %v", err)
	}

	// Verify the new instruction was applied
	if a.config.Instruction != "New custom instruction." {
		t.Errorf("config.Instruction = %q, want %q", a.config.Instruction, "New custom instruction.")
	}
}

func TestRebuildWithInstructionEmptyError(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{Model: llm})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Rebuild with empty instruction should fail
	err = a.RebuildWithInstruction("")
	if err == nil {
		t.Error("RebuildWithInstruction() should return error for empty instruction")
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.InitialDelay != 1*time.Second {
		t.Errorf("InitialDelay = %v, want 1s", cfg.InitialDelay)
	}
	if cfg.MaxDelay != 30*time.Second {
		t.Errorf("MaxDelay = %v, want 30s", cfg.MaxDelay)
	}
}
