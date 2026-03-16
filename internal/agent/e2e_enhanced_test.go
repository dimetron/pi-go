//go:build e2e

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/tools"
)

// initGitRepo creates a git repo in dir with one committed file.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test User"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	// Create and commit an initial file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	for _, args := range [][]string{
		{"add", "main.go"},
		{"commit", "-m", "initial commit"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
}

// TestE2EGitOverviewWorkflow tests a workflow where the agent uses git-overview
// to inspect a repository, then uses git-file-diff to examine a specific change.
func TestE2EGitOverviewWorkflow(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Make a modification to trigger unstaged changes
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)

	llm := &scenarioLLM{
		name: "e2e-git-overview",
		steps: []scenarioStep{
			// Step 1: Call git-overview to see repo state
			{functionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "git-overview",
				Args: map[string]any{},
			}},
			// Step 2: Call git-file-diff on the changed file
			{functionCall: &genai.FunctionCall{
				ID:   "call-2",
				Name: "git-file-diff",
				Args: map[string]any{
					"file": "main.go",
				},
			}},
			// Step 3: Call git-hunk to inspect hunks
			{functionCall: &genai.FunctionCall{
				ID:   "call-3",
				Name: "git-hunk",
				Args: map[string]any{
					"file": "main.go",
				},
			}},
			// Step 4: Summary
			{text: "The repo has unstaged changes in main.go adding a fmt.Println call."},
		},
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a code review agent.",
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
	for event, err := range a.Run(ctx, sessionID, "Review the git changes") {
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

	// Verify all three git tools were called
	for _, name := range []string{"git-overview", "git-file-diff", "git-hunk"} {
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

// TestE2EGitStagedDiffWorkflow tests that git-file-diff works with staged changes.
func TestE2EGitStagedDiffWorkflow(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create a new file and stage it
	os.WriteFile(filepath.Join(dir, "utils.go"), []byte("package main\n\nfunc add(a, b int) int { return a + b }\n"), 0o644)
	cmd := exec.Command("git", "add", "utils.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s (%v)", out, err)
	}

	llm := &scenarioLLM{
		name: "e2e-git-staged",
		steps: []scenarioStep{
			// Step 1: Check overview for staged files
			{functionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "git-overview",
				Args: map[string]any{},
			}},
			// Step 2: Get diff of staged file
			{functionCall: &genai.FunctionCall{
				ID:   "call-2",
				Name: "git-file-diff",
				Args: map[string]any{
					"file":   "utils.go",
					"staged": true,
				},
			}},
			{text: "New file utils.go adds an add function."},
		},
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a code review agent.",
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
	for event, err := range a.Run(ctx, sessionID, "Review staged changes") {
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

	if toolResponses["git-overview"] != 1 {
		t.Errorf("expected 1 git-overview response, got %d", toolResponses["git-overview"])
	}
	if toolResponses["git-file-diff"] != 1 {
		t.Errorf("expected 1 git-file-diff response, got %d", toolResponses["git-file-diff"])
	}
}

// TestE2ERoleResolution tests that model roles are correctly resolved from config.
func TestE2ERoleResolution(t *testing.T) {
	cfg := config.Defaults()
	cfg.Roles = map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-20250514"},
		"smol":    {Model: "claude-haiku-3-20240307"},
		"slow":    {Model: "claude-opus-4-20250514"},
		"plan":    {Model: "claude-opus-4-20250514"},
		"commit":  {Model: "claude-haiku-3-20240307"},
	}

	tests := []struct {
		role      string
		wantModel string
		wantProv  string
	}{
		{"default", "claude-sonnet-4-20250514", "anthropic"},
		{"smol", "claude-haiku-3-20240307", "anthropic"},
		{"slow", "claude-opus-4-20250514", "anthropic"},
		{"plan", "claude-opus-4-20250514", "anthropic"},
		{"commit", "claude-haiku-3-20240307", "anthropic"},
		{"unknown", "claude-sonnet-4-20250514", "anthropic"}, // falls back to default
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			model, prov, err := cfg.ResolveRole(tt.role)
			if err != nil {
				t.Fatalf("ResolveRole(%q) error: %v", tt.role, err)
			}
			if model != tt.wantModel {
				t.Errorf("ResolveRole(%q) model = %q, want %q", tt.role, model, tt.wantModel)
			}
			if prov != tt.wantProv {
				t.Errorf("ResolveRole(%q) provider = %q, want %q", tt.role, prov, tt.wantProv)
			}
		})
	}
}

// TestE2EEditAndReadInGitRepo tests a full edit → read → git-overview workflow.
func TestE2EEditAndReadInGitRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	llm := &scenarioLLM{
		name: "e2e-edit-git",
		steps: []scenarioStep{
			// Step 1: Read the file
			{functionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "read",
				Args: map[string]any{"file_path": filepath.Join(dir, "main.go")},
			}},
			// Step 2: Edit it
			{functionCall: &genai.FunctionCall{
				ID:   "call-2",
				Name: "edit",
				Args: map[string]any{
					"file_path":  filepath.Join(dir, "main.go"),
					"old_string": "func main() {}",
					"new_string": "func main() {\n\t// TODO: implement\n}",
				},
			}},
			// Step 3: Check git status via git-overview
			{functionCall: &genai.FunctionCall{
				ID:   "call-3",
				Name: "git-overview",
				Args: map[string]any{},
			}},
			// Step 4: Get the diff
			{functionCall: &genai.FunctionCall{
				ID:   "call-4",
				Name: "git-file-diff",
				Args: map[string]any{"file": "main.go"},
			}},
			{text: "Added a TODO comment to main.go."},
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
	for event, err := range a.Run(ctx, sessionID, "Add a TODO to main.go and check status") {
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

	// Verify file was edited
	data, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(data), "TODO: implement") {
		t.Errorf("expected file to contain TODO comment, got:\n%s", data)
	}

	// Verify all expected tools were called
	for _, name := range []string{"read", "edit", "git-overview", "git-file-diff"} {
		if toolResponses[name] == 0 {
			t.Errorf("expected function response for %q tool", name)
		}
	}

	// Verify LLM was called 5 times
	llm.mu.Lock()
	calls := llm.callIdx
	llm.mu.Unlock()
	if calls != 5 {
		t.Errorf("expected 5 LLM calls, got %d", calls)
	}
}

// TestE2EAllNewToolsRegistered verifies that all new tools from the enhancement
// project are registered and available.
func TestE2EAllNewToolsRegistered(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	coreTools, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	// Verify all 11 core tools exist
	expected := map[string]bool{
		"read": true, "write": true, "edit": true, "bash": true,
		"grep": true, "find": true, "ls": true, "tree": true,
		"git-overview": true, "git-file-diff": true, "git-hunk": true,
	}

	toolNames := make(map[string]bool)
	for _, t := range coreTools {
		toolNames[t.Name()] = true
	}

	for name := range expected {
		if !toolNames[name] {
			t.Errorf("missing expected core tool: %s", name)
		}
	}

	if len(coreTools) != 11 {
		t.Errorf("expected 11 core tools, got %d", len(coreTools))
	}
}

// TestE2ESandboxRestriction verifies that the sandbox prevents path traversal.
func TestE2ESandboxRestriction(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create a file inside the sandbox
	err := sb.WriteFile("allowed.txt", []byte("inside sandbox"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile inside sandbox failed: %v", err)
	}

	// Read it back
	data, err := sb.ReadFile("allowed.txt")
	if err != nil {
		t.Fatalf("ReadFile inside sandbox failed: %v", err)
	}
	if string(data) != "inside sandbox" {
		t.Errorf("ReadFile content = %q, want %q", data, "inside sandbox")
	}

	// Verify path traversal is blocked
	_, err = sb.ReadFile("../../../etc/passwd")
	if err == nil {
		t.Error("expected error reading outside sandbox, got nil")
	}
}
