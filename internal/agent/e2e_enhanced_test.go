//go:build e2e

package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/subagent"
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
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
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
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

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
	if err := os.WriteFile(filepath.Join(dir, "utils.go"), []byte("package main\n\nfunc add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
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
		"default": {Model: "claude-sonnet-4-6"},
		"smol":    {Model: "claude-haiku-4-5-20251001"},
		"slow":    {Model: "claude-opus-4-6"},
		"plan":    {Model: "claude-opus-4-6"},
		"commit":  {Model: "claude-haiku-4-5-20251001"},
	}

	tests := []struct {
		role      string
		wantModel string
		wantProv  string
	}{
		{"default", "claude-sonnet-4-6", "anthropic"},
		{"smol", "claude-haiku-4-5-20251001", "anthropic"},
		{"slow", "claude-opus-4-6", "anthropic"},
		{"plan", "claude-opus-4-6", "anthropic"},
		{"commit", "claude-haiku-4-5-20251001", "anthropic"},
		{"unknown", "claude-sonnet-4-6", "anthropic"}, // falls back to default
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

	if len(coreTools) < len(expected) {
		t.Errorf("expected at least %d core tools, got %d", len(expected), len(coreTools))
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

// TestE2ECommitWorkflow tests the /commit flow: create temp repo, stage changes,
// use scenarioLLM to generate a conventional commit message, verify commit created.
func TestE2ECommitWorkflow(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create and stage a new file
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc hello() string { return \"hello\" }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cmd := exec.Command("git", "add", "hello.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s (%v)", out, err)
	}

	// The scenarioLLM simulates the agent reading the diff and making a commit
	llm := &scenarioLLM{
		name: "e2e-commit",
		steps: []scenarioStep{
			// Step 1: Agent calls git-overview to see staged changes
			{functionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "git-overview",
				Args: map[string]any{},
			}},
			// Step 2: Agent calls git-file-diff to see staged diff
			{functionCall: &genai.FunctionCall{
				ID:   "call-2",
				Name: "git-file-diff",
				Args: map[string]any{
					"file":   "hello.go",
					"staged": true,
				},
			}},
			// Step 3: Agent uses bash to commit with conventional message
			{functionCall: &genai.FunctionCall{
				ID:   "call-3",
				Name: "bash",
				Args: map[string]any{
					"command": "cd " + dir + " && git commit -m 'feat(main): add hello function'",
				},
			}},
			// Step 4: Summary
			{text: "Committed: feat(main): add hello function"},
		},
	}

	coreTools, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatalf("CoreTools() error: %v", err)
	}

	a, err := New(Config{
		Model:       llm,
		Tools:       coreTools,
		Instruction: "You are a commit agent.",
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
	for event, err := range a.Run(ctx, sessionID, "Commit the staged changes with a conventional message") {
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

	// Verify expected tool calls were dispatched
	for _, name := range []string{"git-overview", "git-file-diff", "bash"} {
		if toolResponses[name] == 0 {
			t.Errorf("expected function response for %q tool", name)
		}
	}

	// Verify commit was created with conventional format
	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "-1").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %s (%v)", out, err)
	}
	logLine := strings.TrimSpace(string(out))
	if !strings.Contains(logLine, "feat(main): add hello function") {
		t.Errorf("expected conventional commit message, got: %s", logLine)
	}

	// Verify the file exists in the commit
	out, err = exec.Command("git", "-C", dir, "show", "--name-only", "--format=", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %s (%v)", out, err)
	}
	if !strings.Contains(string(out), "hello.go") {
		t.Errorf("expected hello.go in commit, got: %s", out)
	}
}

// TestE2ESubagentTypes verifies all 6 agent types are defined with valid
// role mappings, worktree settings, instructions, and tool lists.
func TestE2ESubagentTypes(t *testing.T) {
	expectedTypes := []struct {
		name      string
		role      string
		worktree  bool
		minTools  int
		wantTools []string // a subset of tools that must be present
	}{
		{"explore", "smol", false, 3, []string{"read", "grep", "find"}},
		{"plan", "plan", false, 3, []string{"read", "grep", "git-overview"}},
		{"designer", "slow", true, 5, []string{"read", "write", "edit", "bash"}},
		{"reviewer", "slow", false, 3, []string{"read", "git-overview", "git-file-diff", "git-hunk"}},
		{"task", "default", true, 5, []string{"read", "write", "edit", "bash", "git-overview"}},
		{"quick_task", "smol", false, 3, []string{"read", "write", "edit", "bash"}},
	}

	if len(subagent.AgentTypes) != 6 {
		t.Fatalf("expected 6 agent types, got %d", len(subagent.AgentTypes))
	}

	for _, tt := range expectedTypes {
		t.Run(tt.name, func(t *testing.T) {
			def, ok := subagent.AgentTypes[tt.name]
			if !ok {
				t.Fatalf("agent type %q not found", tt.name)
			}

			if def.Role != tt.role {
				t.Errorf("role = %q, want %q", def.Role, tt.role)
			}
			if def.Worktree != tt.worktree {
				t.Errorf("worktree = %v, want %v", def.Worktree, tt.worktree)
			}
			if def.Instruction == "" {
				t.Error("instruction is empty")
			}
			if len(def.Tools) < tt.minTools {
				t.Errorf("tools count = %d, want at least %d", len(def.Tools), tt.minTools)
			}

			toolSet := make(map[string]bool)
			for _, name := range def.Tools {
				toolSet[name] = true
			}
			for _, want := range tt.wantTools {
				if !toolSet[want] {
					t.Errorf("missing expected tool %q in tools %v", want, def.Tools)
				}
			}

			// Verify role is valid (maps to a known config role)
			if err := subagent.ValidateType(tt.name); err != nil {
				t.Errorf("ValidateType(%q) error: %v", tt.name, err)
			}
		})
	}

	// Verify unknown type is rejected
	if err := subagent.ValidateType("nonexistent"); err == nil {
		t.Error("expected error for unknown agent type, got nil")
	}
}

// TestE2EWorktreeLifecycle tests creating a worktree, verifying it exists as a
// separate working copy, and cleaning it up.
func TestE2EWorktreeLifecycle(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	mgr := subagent.NewWorktreeManager(dir)

	// Create a worktree
	wtPath, err := mgr.Create("test-agent-001")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify worktree path exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatalf("worktree path does not exist: %s", wtPath)
	}

	// Verify it's a separate working copy (has its own .git or gitdir link)
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); os.IsNotExist(err) {
		t.Error("worktree directory does not contain .git marker")
	}

	// Verify the initial file from the repo is present in the worktree
	if _, err := os.Stat(filepath.Join(wtPath, "main.go")); os.IsNotExist(err) {
		t.Error("main.go not found in worktree")
	}

	// Verify Active count
	if mgr.Active() != 1 {
		t.Errorf("Active() = %d, want 1", mgr.Active())
	}

	// Verify PathFor
	if mgr.PathFor("test-agent-001") != wtPath {
		t.Errorf("PathFor() = %q, want %q", mgr.PathFor("test-agent-001"), wtPath)
	}

	// Make a change in the worktree
	if err := os.WriteFile(filepath.Join(wtPath, "wt-file.txt"), []byte("worktree change\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Cleanup
	err = mgr.Cleanup("test-agent-001")
	if err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	// Verify worktree is removed
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree path still exists after cleanup: %s", wtPath)
	}

	// Verify Active count is back to 0
	if mgr.Active() != 0 {
		t.Errorf("Active() = %d, want 0 after cleanup", mgr.Active())
	}

	// Verify PathFor returns empty after cleanup
	if mgr.PathFor("test-agent-001") != "" {
		t.Errorf("PathFor() = %q after cleanup, want empty", mgr.PathFor("test-agent-001"))
	}
}

// TestE2ELSPToolRegistration verifies LSPTools() returns 5 tools with the correct names.
func TestE2ELSPToolRegistration(t *testing.T) {
	mgr := lsp.NewManager(nil)
	defer mgr.Shutdown()

	lspTools, err := tools.LSPTools(mgr)
	if err != nil {
		t.Fatalf("LSPTools() error: %v", err)
	}

	if len(lspTools) != 5 {
		t.Fatalf("expected 5 LSP tools, got %d", len(lspTools))
	}

	expected := map[string]bool{
		"lsp-diagnostics": false,
		"lsp-definition":  false,
		"lsp-references":  false,
		"lsp-hover":       false,
		"lsp-symbols":     false,
	}

	for _, tool := range lspTools {
		name := tool.Name()
		if _, ok := expected[name]; !ok {
			t.Errorf("unexpected LSP tool name: %s", name)
			continue
		}
		expected[name] = true

		// Verify each tool has a description
		if tool.Description() == "" {
			t.Errorf("tool %s has empty description", name)
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing expected LSP tool: %s", name)
		}
	}
}

// TestE2EAgentToolRegistration verifies the agent tool is created with correct
// name and that it has the expected schema fields (type and prompt).
func TestE2EAgentToolRegistration(t *testing.T) {
	cfg := config.Defaults()
	cfg.Roles["smol"] = config.RoleConfig{Model: "claude-haiku"}
	cfg.Roles["slow"] = config.RoleConfig{Model: "claude-opus"}
	cfg.Roles["plan"] = config.RoleConfig{Model: "claude-sonnet"}

	orch := subagent.NewOrchestrator(&cfg, "")

	agentTools, err := tools.AgentTools(orch, nil)
	if err != nil {
		t.Fatalf("AgentTools() error: %v", err)
	}

	if len(agentTools) != 1 {
		t.Fatalf("expected 1 agent tool, got %d", len(agentTools))
	}

	agentTool := agentTools[0]
	if agentTool.Name() != "agent" {
		t.Errorf("expected tool name 'agent', got %q", agentTool.Name())
	}

	if agentTool.Description() == "" {
		t.Error("agent tool has empty description")
	}

	// Verify description mentions available agent types
	desc := agentTool.Description()
	for _, typeName := range []string{"explore", "plan", "designer", "reviewer", "task", "quick_task"} {
		if !strings.Contains(desc, typeName) {
			t.Errorf("agent tool description does not mention type %q", typeName)
		}
	}

	// Verify the tool implements Declaration to check schema has type and prompt fields
	type declarator interface {
		Declaration() *genai.FunctionDeclaration
	}
	if d, ok := agentTool.(declarator); ok {
		decl := d.Declaration()
		if decl == nil {
			t.Fatal("Declaration() returned nil")
		}
		if decl.Name != "agent" {
			t.Errorf("declaration name = %q, want 'agent'", decl.Name)
		}
		// Marshal ParametersJsonSchema to JSON and check for required fields
		if decl.ParametersJsonSchema != nil {
			jsonBytes, err := json.Marshal(decl.ParametersJsonSchema)
			if err != nil {
				t.Fatalf("failed to marshal schema: %v", err)
			}
			var schema map[string]any
			if err := json.Unmarshal(jsonBytes, &schema); err != nil {
				t.Fatalf("failed to unmarshal schema: %v", err)
			}
			props, _ := schema["properties"].(map[string]any)
			if props == nil {
				t.Error("schema missing 'properties'")
			} else {
				if _, ok := props["type"]; !ok {
					t.Error("schema missing 'type' property")
				}
				if _, ok := props["prompt"]; !ok {
					t.Error("schema missing 'prompt' property")
				}
			}
		} else {
			t.Error("declaration has nil ParametersJsonSchema")
		}
	} else {
		t.Error("agent tool does not implement Declaration interface; schema check skipped")
	}
}
