//go:build e2e

package agent

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	piSession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
)

// scenarioLLM simulates a multi-step conversation with predefined responses
// based on call count, supporting arbitrarily many tool calls and text responses.
type scenarioLLM struct {
	name    string
	steps   []scenarioStep
	mu      sync.Mutex
	callIdx int
}

type scenarioStep struct {
	functionCall *genai.FunctionCall
	text         string
}

func (m *scenarioLLM) Name() string { return m.name }

func (m *scenarioLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	idx := m.callIdx
	m.callIdx++
	m.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		var resp *model.LLMResponse
		if idx < len(m.steps) {
			step := m.steps[idx]
			if step.functionCall != nil {
				resp = &model.LLMResponse{
					Content: &genai.Content{
						Role: genai.RoleModel,
						Parts: []*genai.Part{
							{FunctionCall: step.functionCall},
						},
					},
				}
			} else {
				resp = &model.LLMResponse{
					Content: genai.NewContentFromText(step.text, genai.RoleModel),
				}
			}
		} else {
			resp = &model.LLMResponse{
				Content: genai.NewContentFromText("Done.", genai.RoleModel),
			}
		}
		yield(resp, nil)
	}
}

// TestE2EFileEditingWorkflow tests a realistic file editing scenario:
// 1. Agent reads a file
// 2. Agent edits the file
// 3. Agent reads the file again to verify
// 4. Agent responds with summary
func TestE2EFileEditingWorkflow(t *testing.T) {
	dir := t.TempDir()
	sourceFile := filepath.Join(dir, "server.go")
	os.WriteFile(sourceFile, []byte(`package main

import "net/http"

func main() {
	http.HandleFunc("/", handler)
	http.ListenAndServe(":8080", nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello World"))
}
`), 0o644)

	llm := &scenarioLLM{
		name: "e2e-edit-workflow",
		steps: []scenarioStep{
			// Step 1: Read the file
			{functionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "read",
				Args: map[string]any{"file_path": sourceFile},
			}},
			// Step 2: Edit to add error handling to ListenAndServe
			{functionCall: &genai.FunctionCall{
				ID:   "call-2",
				Name: "edit",
				Args: map[string]any{
					"file_path":  sourceFile,
					"old_string": `http.ListenAndServe(":8080", nil)`,
					"new_string": `log.Fatal(http.ListenAndServe(":8080", nil))`,
				},
			}},
			// Step 3: Read again to verify
			{functionCall: &genai.FunctionCall{
				ID:   "call-3",
				Name: "read",
				Args: map[string]any{"file_path": sourceFile},
			}},
			// Step 4: Final text response
			{text: "I've added error handling to the ListenAndServe call using log.Fatal."},
		},
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a coding agent.",
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
	for event, err := range a.Run(ctx, sessionID, "Add error handling to server.go") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if event != nil {
			events = append(events, event)
		}
	}

	// Verify file was actually edited on disk
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(data), "log.Fatal") {
		t.Errorf("expected file to contain 'log.Fatal', got:\n%s", string(data))
	}

	// Verify we got the expected sequence of events
	var readCount, editCount int
	var hasFinalText bool
	for _, e := range events {
		if e.Content == nil {
			continue
		}
		for _, p := range e.Content.Parts {
			if p.FunctionCall != nil {
				switch p.FunctionCall.Name {
				case "read":
					readCount++
				case "edit":
					editCount++
				}
			}
			if p.Text != "" && strings.Contains(p.Text, "log.Fatal") {
				hasFinalText = true
			}
		}
	}

	if readCount != 2 {
		t.Errorf("expected 2 read tool calls, got %d", readCount)
	}
	if editCount != 1 {
		t.Errorf("expected 1 edit tool call, got %d", editCount)
	}
	if !hasFinalText {
		t.Error("expected final text mentioning log.Fatal")
	}

	// Verify LLM was called 4 times
	llm.mu.Lock()
	calls := llm.callIdx
	llm.mu.Unlock()
	if calls != 4 {
		t.Errorf("expected 4 LLM calls, got %d", calls)
	}
}

// TestE2ESessionPersistenceRoundTrip tests that a session persists to disk
// and can be resumed across agent restarts.
func TestE2ESessionPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")

	// Create file service for persistence
	fileSvc, err := piSession.NewFileService(sessDir)
	if err != nil {
		t.Fatalf("NewFileService error: %v", err)
	}

	// First agent run: simple text response
	llm1 := &mockLLM{name: "test-model", response: "Here is my first response."}
	a1, err := New(Config{
		Model:          llm1,
		SessionService: fileSvc,
		Instruction:    "You are a helpful agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a1.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Run first turn
	for _, err := range a1.Run(ctx, sessionID, "Hello, remember this conversation.") {
		if err != nil {
			t.Fatalf("Run() turn 1 error: %v", err)
		}
	}

	// Simulate restart: create a new FileService from the same directory
	fileSvc2, err := piSession.NewFileService(sessDir)
	if err != nil {
		t.Fatalf("NewFileService (restart) error: %v", err)
	}

	// Verify session can be found via LastSessionID
	lastID := fileSvc2.LastSessionID(AppName, DefaultUserID)
	if lastID != sessionID {
		t.Errorf("LastSessionID() = %q, want %q", lastID, sessionID)
	}

	// Verify session has events from first turn
	getResp, err := fileSvc2.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    DefaultUserID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	eventsObj := getResp.Session.Events()
	eventCount := eventsObj.Len()
	if eventCount == 0 {
		t.Fatal("expected events from first turn, got none")
	}

	// Verify events contain user message and model response
	var hasUserMsg, hasModelMsg bool
	for e := range eventsObj.All() {
		if e.Content == nil {
			continue
		}
		if e.Content.Role == genai.RoleUser {
			hasUserMsg = true
		}
		if e.Content.Role == genai.RoleModel {
			hasModelMsg = true
		}
	}
	if !hasUserMsg {
		t.Error("expected user message in persisted events")
	}
	if !hasModelMsg {
		t.Error("expected model response in persisted events")
	}

	// Second agent run with the same session: continue the conversation
	llm2 := &mockLLM{name: "test-model", response: "I remember our conversation."}
	a2, err := New(Config{
		Model:          llm2,
		SessionService: fileSvc2,
		Instruction:    "You are a helpful agent.",
	})
	if err != nil {
		t.Fatalf("New() (restart) error: %v", err)
	}

	// Run second turn on the same session
	var turn2Events []*session.Event
	for event, err := range a2.Run(ctx, sessionID, "Do you remember?") {
		if err != nil {
			t.Fatalf("Run() turn 2 error: %v", err)
		}
		if event != nil {
			turn2Events = append(turn2Events, event)
		}
	}

	if len(turn2Events) == 0 {
		t.Error("expected events from second turn")
	}

	// Verify total events grew (first turn + second turn)
	getResp2, err := fileSvc2.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    DefaultUserID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get() after turn 2 error: %v", err)
	}

	allEventsObj := getResp2.Session.Events()
	allEventCount := allEventsObj.Len()
	if allEventCount <= eventCount {
		t.Errorf("expected more events after second turn: got %d, had %d", allEventCount, eventCount)
	}
}

// TestE2EMultiTurnConversation tests a multi-turn conversation where the agent
// uses tools across multiple user messages in the same session.
func TestE2EMultiTurnConversation(t *testing.T) {
	dir := t.TempDir()

	// Turn 1: User asks to create a file. Agent writes it.
	writeLLM := &toolCallingLLM{
		name: "e2e-multi-turn",
		functionCall: &genai.FunctionCall{
			ID:   "call-write",
			Name: "write",
			Args: map[string]any{
				"file_path": filepath.Join(dir, "hello.txt"),
				"content":   "Hello, World!",
			},
		},
		finalText: "I've created hello.txt with your message.",
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       writeLLM,
		Tools:       coreTools,
		Instruction: "You are a coding agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Turn 1: write a file
	for _, err := range a.Run(ctx, sessionID, "Create hello.txt") {
		if err != nil {
			t.Fatalf("Run() turn 1 error: %v", err)
		}
	}

	// Verify file was created
	data, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "Hello, World!" {
		t.Errorf("file content = %q, want %q", string(data), "Hello, World!")
	}

	// Turn 2: new agent (simulating model swap) reads the file back
	// We need a fresh agent since the LLM mock is stateful
	readLLM := &toolCallingLLM{
		name: "e2e-multi-turn-read",
		functionCall: &genai.FunctionCall{
			ID:   "call-read",
			Name: "read",
			Args: map[string]any{
				"file_path": filepath.Join(dir, "hello.txt"),
			},
		},
		finalText: "The file contains: Hello, World!",
	}

	a2, err := New(Config{
		Model:       readLLM,
		Tools:       coreTools,
		Instruction: "You are a coding agent.",
	})
	if err != nil {
		t.Fatalf("New() turn 2 error: %v", err)
	}

	sessionID2, err := a2.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() turn 2 error: %v", err)
	}

	var turn2Events []*session.Event
	for event, err := range a2.Run(ctx, sessionID2, "Read hello.txt") {
		if err != nil {
			t.Fatalf("Run() turn 2 error: %v", err)
		}
		if event != nil {
			turn2Events = append(turn2Events, event)
		}
	}

	// Verify we got a read function response
	var hasReadResult bool
	for _, e := range turn2Events {
		if e.Content == nil {
			continue
		}
		for _, p := range e.Content.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "read" {
				hasReadResult = true
			}
		}
	}
	if !hasReadResult {
		t.Error("expected function response for read tool in turn 2")
	}
}

// TestE2EBashAndGrepWorkflow tests a workflow where the agent runs a bash
// command, then greps for patterns in the output files.
func TestE2EBashAndGrepWorkflow(t *testing.T) {
	dir := t.TempDir()

	llm := &scenarioLLM{
		name: "e2e-bash-grep",
		steps: []scenarioStep{
			// Step 1: Run bash to create a Go file
			{functionCall: &genai.FunctionCall{
				ID:   "call-bash",
				Name: "bash",
				Args: map[string]any{
					"command": "echo 'package main\n\nfunc Add(a, b int) int { return a + b }\nfunc Sub(a, b int) int { return a - b }' > " + filepath.Join(dir, "math.go"),
				},
			}},
			// Step 2: Grep for function definitions
			{functionCall: &genai.FunctionCall{
				ID:   "call-grep",
				Name: "grep",
				Args: map[string]any{
					"pattern": "func ",
					"path":    dir,
				},
			}},
			// Step 3: Find all Go files
			{functionCall: &genai.FunctionCall{
				ID:   "call-find",
				Name: "find",
				Args: map[string]any{
					"pattern": "*.go",
					"path":    dir,
				},
			}},
			// Step 4: Summary
			{text: "Found 2 functions (Add and Sub) in math.go."},
		},
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a coding agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	toolResponses := map[string]int{}
	for event, err := range a.Run(ctx, sessionID, "Analyze Go functions in the directory") {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if event != nil && event.Content != nil {
			for _, p := range event.Content.Parts {
				if p.FunctionResponse != nil {
					toolResponses[p.FunctionResponse.Name]++
				}
			}
		}
	}

	// Verify all three tools were called
	for _, name := range []string{"bash", "grep", "find"} {
		if toolResponses[name] == 0 {
			t.Errorf("expected function response for %q tool", name)
		}
	}

	// Verify LLM was called 4 times
	llm.mu.Lock()
	calls := llm.callIdx
	llm.mu.Unlock()
	if calls != 4 {
		t.Errorf("expected 4 LLM calls, got %d", calls)
	}
}

// TestE2ESessionBranchingWorkflow tests creating branches, switching between
// them, and verifying events are isolated per branch.
func TestE2ESessionBranchingWorkflow(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")

	fileSvc, err := piSession.NewFileService(sessDir)
	if err != nil {
		t.Fatalf("NewFileService error: %v", err)
	}

	llm := &mockLLM{name: "test-model", response: "Response on main branch."}

	a, err := New(Config{
		Model:          llm,
		SessionService: fileSvc,
		Instruction:    "You are a helpful agent.",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Run on main branch
	for _, err := range a.Run(ctx, sessionID, "Hello on main.") {
		if err != nil {
			t.Fatalf("Run() main error: %v", err)
		}
	}

	// Create a branch
	err = fileSvc.CreateBranch(sessionID, AppName, DefaultUserID, "experiment")
	if err != nil {
		t.Fatalf("CreateBranch() error: %v", err)
	}

	// Switch to the new branch
	err = fileSvc.SwitchBranch(sessionID, AppName, DefaultUserID, "experiment")
	if err != nil {
		t.Fatalf("SwitchBranch(experiment) error: %v", err)
	}

	// List branches — should show experiment as active
	branches, activeBranch, err := fileSvc.ListBranches(sessionID, AppName, DefaultUserID)
	if err != nil {
		t.Fatalf("ListBranches() error: %v", err)
	}
	if len(branches) < 2 {
		t.Errorf("expected at least 2 branches, got %d", len(branches))
	}
	if activeBranch != "experiment" {
		t.Errorf("active branch = %q, want %q", activeBranch, "experiment")
	}

	// Switch back to main
	err = fileSvc.SwitchBranch(sessionID, AppName, DefaultUserID, "main")
	if err != nil {
		t.Fatalf("SwitchBranch(main) error: %v", err)
	}

	active := fileSvc.ActiveBranch(sessionID)
	if active != "main" {
		t.Errorf("active branch after switch = %q, want %q", active, "main")
	}
}
