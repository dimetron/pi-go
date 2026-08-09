package agent

import (
	"context"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/extension"
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

func TestSystemInstruction_SubagentWorktreeGuidance(t *testing.T) {
	for _, phrase := range []string{
		"isolated git worktrees",
		"not automatically applied to the current tree",
		"exact patch/file list",
		`"worker"/"quick-task"`,
	} {
		if !strings.Contains(SystemInstruction, phrase) {
			t.Errorf("SystemInstruction should contain %q", phrase)
		}
	}
}

func TestSystemInstruction_PresentingChoices(t *testing.T) {
	for _, phrase := range []string{
		"# Presenting choices",
		"(recommended)",
		"put it first in the list",
		"Never present a flat list with no guidance",
	} {
		if !strings.Contains(SystemInstruction, phrase) {
			t.Errorf("SystemInstruction should contain %q", phrase)
		}
	}
}

func TestSystemInstruction_ClarifyingQuestions(t *testing.T) {
	for _, phrase := range []string{
		"# Clarifying questions",
		"Default: act",
		"ask before acting",
		"multiple valid interpretations",
		"Ask BEFORE exploring deeply",
		"multiple-choice",
		"Do NOT ask when",
		"The request is clear",
	} {
		if !strings.Contains(SystemInstruction, phrase) {
			t.Errorf("SystemInstruction should contain %q", phrase)
		}
	}
}

func TestSystemInstruction_GitSafetyGuidance(t *testing.T) {
	for _, phrase := range []string{
		"# Git safety",
		"backup branch",
		"Never work directly on main/master",
		"small, self-contained batches",
		"git add -p",
		"--force-with-lease",
	} {
		if !strings.Contains(SystemInstruction, phrase) {
			t.Errorf("SystemInstruction should contain %q", phrase)
		}
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

// titleRecordingService is a session.Service that captures SetSessionTitle
// calls. It satisfies the titleNamer interface so the *Agent wrapper forwards.
type titleRecordingService struct {
	session.Service
	mu     sync.Mutex
	titles []string
}

func (s *titleRecordingService) SetSessionTitle(_ string, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titles = append(s.titles, title)
	return nil
}

func (s *titleRecordingService) lastTitle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.titles) == 0 {
		return ""
	}
	return s.titles[len(s.titles)-1]
}

func TestSetSessionTitle_ForwardsToService(t *testing.T) {
	llm := &mockLLM{name: "test", response: "ok"}
	svc := &titleRecordingService{Service: session.InMemoryService()}
	a, err := New(Config{Model: llm, SessionService: svc})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.SetSessionTitle("sid", "fix the bug"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	if got := svc.lastTitle(); got != "fix the bug" {
		t.Errorf("service recorded %q, want %q", got, "fix the bug")
	}
}

func TestSetSessionTitle_NoOpForInMemoryService(t *testing.T) {
	llm := &mockLLM{name: "test", response: "ok"}
	// session.InMemoryService does not implement SetSessionTitle.
	a, err := New(Config{Model: llm, SessionService: session.InMemoryService()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Must not error even though the service has no SetSessionTitle.
	if err := a.SetSessionTitle("sid", "anything"); err != nil {
		t.Errorf("SetSessionTitle on in-memory service: %v", err)
	}
}

func TestDefaultSessionTitle_GitRepo(t *testing.T) {
	// Stub both git lookups so the test is hermetic — no real `git` invocation.
	origToplevel := gitToplevelFn
	origBranch := gitCurrentBranch
	gitToplevelFn = func(dir string) string { return "/home/dev/myrepo" }
	gitCurrentBranch = func(dir string) string { return "main" }
	t.Cleanup(func() {
		gitToplevelFn = origToplevel
		gitCurrentBranch = origBranch
	})

	if got, want := defaultSessionTitle("/home/dev/myrepo/src"), "myrepo main src"; got != want {
		t.Errorf("defaultSessionTitle = %q, want %q", got, want)
	}
}

func TestDefaultSessionTitle_NotGitRepo(t *testing.T) {
	origToplevel := gitToplevelFn
	origBranch := gitCurrentBranch
	gitToplevelFn = func(dir string) string { return "" } // not a repo
	gitCurrentBranch = func(dir string) string { return "" }
	t.Cleanup(func() {
		gitToplevelFn = origToplevel
		gitCurrentBranch = origBranch
	})

	if got, want := defaultSessionTitle("/tmp/some/dir"), "dir"; got != want {
		t.Errorf("defaultSessionTitle = %q, want %q", got, want)
	}
}

func TestDefaultSessionTitle_EmptyCwd(t *testing.T) {
	if got := defaultSessionTitle(""); got != "" {
		t.Errorf("defaultSessionTitle(\"\") = %q, want \"\"", got)
	}
}

func TestDefaultSessionTitle_GitRepoNoBranch(t *testing.T) {
	// Detached HEAD / unborn branch: gitCurrentBranch returns "". The title
	// should drop the branch token but keep the "<repo> <folder>" frame so
	// the structure still parses as "this is the repo, in this folder".
	origToplevel := gitToplevelFn
	origBranch := gitCurrentBranch
	gitToplevelFn = func(dir string) string { return "/home/dev/myrepo" }
	gitCurrentBranch = func(dir string) string { return "" } // detached / no commits
	t.Cleanup(func() {
		gitToplevelFn = origToplevel
		gitCurrentBranch = origBranch
	})

	if got, want := defaultSessionTitle("/home/dev/myrepo/src"), "myrepo src"; got != want {
		t.Errorf("defaultSessionTitle = %q, want %q", got, want)
	}
}

func TestDefaultSessionTitle_RealGitRepo(t *testing.T) {
	// Use a real temp git repo to exercise the actual subprocess path.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	// commit.gpgsign is set globally in this repo and signs through 1Password,
	// which a temp repo inherits; without this the commit blocks on the agent
	// and fails after a 60s timeout.
	runGit("config", "commit.gpgsign", "false")
	runGit("config", "tag.gpgsign", "false")
	runGit("commit", "--allow-empty", "-q", "-m", "init")

	// A fresh repo on the default branch should produce
	// "<basename(dir)> <branch> <basename(dir)>" — same value on both
	// sides because the test creates the repo at t.TempDir() and the default
	// branch (whatever it is) is real.
	got := defaultSessionTitle(dir)
	branch := gitCurrentBranch(dir)
	if branch == "" {
		t.Fatalf("gitCurrentBranch returned empty for fresh repo; expected a real branch name")
	}
	want := filepath.Base(dir) + " " + branch + " " + filepath.Base(dir)
	if got != want {
		t.Errorf("defaultSessionTitle = %q, want %q", got, want)
	}
}

func TestCreateSession_DefaultTitleFromGitRepo(t *testing.T) {
	// Drive the real default-title path through CreateSession by recording
	// the title the agent sets. Stub both git lookups so the test does not
	// depend on the test binary's CWD being inside a git repo or on its
	// current branch.
	origToplevel := gitToplevelFn
	origBranch := gitCurrentBranch
	gitToplevelFn = func(dir string) string { return "/home/dev/piname" }
	gitCurrentBranch = func(dir string) string { return "main" }
	t.Cleanup(func() {
		gitToplevelFn = origToplevel
		gitCurrentBranch = origBranch
	})

	svc := &titleRecordingService{Service: session.InMemoryService()}
	a, err := New(Config{Model: &mockLLM{name: "t", response: "ok"}, SessionService: svc})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cwd, _ := os.Getwd()
	want := "piname main " + filepath.Base(cwd)
	sid, _, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sid == "" {
		t.Fatal("CreateSession returned empty id")
	}
	if got := svc.lastTitle(); got != want {
		t.Errorf("default title recorded = %q, want %q", got, want)
	}
}

func TestCreateSession(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{Model: llm})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, _, err := a.CreateSession(ctx)
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
	sessionID, _, err := a.CreateSession(ctx)
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
	sessionID, _, err := a.CreateSession(ctx)
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
	result := loadInstructionFrom(base, t.TempDir(), "")

	if !strings.Contains(result, base) {
		t.Errorf("LoadInstruction() should contain base instruction, got %q", result)
	}
	if strings.Contains(result, "# Project Rules") {
		t.Errorf("LoadInstruction() should not contain project rules without AGENTS.md, got %q", result)
	}
}

func TestLoadInstructionWithAgentsFile(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".pi-go")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "AGENTS.md"), []byte("# Custom Rules\n- Rule 1"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error: %v", err)
	}
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

func TestLoadInstructionWithAgentFileInCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Local Rules\n- Rule A"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error: %v", err)
	}
	defer os.Chdir(origDir)

	base := "Base instruction."
	result := LoadInstruction(base)

	if !strings.Contains(result, "Local Rules") {
		t.Errorf("LoadInstruction() should contain AGENT.md content, got %q", result)
	}
	if !strings.Contains(result, base) {
		t.Errorf("LoadInstruction() should contain base instruction, got %q", result)
	}
}

func TestLoadInstructionPrefersAgentFileInCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Local Rules\n- Local"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	agentsDir := filepath.Join(dir, ".pi-go")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "AGENTS.md"), []byte("# Project Rules\n- Project"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error: %v", err)
	}
	defer os.Chdir(origDir)

	result := LoadInstruction("Base instruction.")

	if !strings.Contains(result, "Local Rules") {
		t.Errorf("LoadInstruction() should prefer AGENT.md content, got %q", result)
	}
	if strings.Contains(result, "- Project") {
		t.Errorf("LoadInstruction() should not append .pi-go/AGENTS.md when AGENT.md exists, got %q", result)
	}
}

func TestLoadInstructionDiscoversRootAgentsFile(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "internal", "agent")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Repo Rules\n- Root"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	result := loadInstructionFrom("Base instruction.", child, "")

	if !strings.Contains(result, "Repo Rules") {
		t.Fatalf("LoadInstruction() should discover parent AGENTS.md, got %q", result)
	}
}

func TestLoadInstructionDiscoversClaudeFile(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "cmd", "pi")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Claude Rules\n- Parent"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	result := loadInstructionFrom("Base instruction.", child, "")

	if !strings.Contains(result, "Claude Rules") {
		t.Fatalf("LoadInstruction() should discover parent CLAUDE.md, got %q", result)
	}
}

func TestLoadInstructionMergesGlobalAndParentContext(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	child := filepath.Join(dir, "internal", "agent")
	if err := os.MkdirAll(filepath.Join(home, ".pi-go"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi-go", "AGENTS.md"), []byte("# Global Rules\n- Global"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Repo Rules\n- Repo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(child, "AGENTS.md"), []byte("# Local Rules\n- Local"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	result := loadInstructionFrom("Base instruction.", child, home)

	globalIndex := strings.Index(result, "Global Rules")
	repoIndex := strings.Index(result, "Repo Rules")
	localIndex := strings.Index(result, "Local Rules")
	if globalIndex == -1 || repoIndex == -1 || localIndex == -1 {
		t.Fatalf("LoadInstruction() should merge global, parent, and local context, got %q", result)
	}
	if globalIndex >= repoIndex || repoIndex >= localIndex {
		t.Fatalf("LoadInstruction() should order context from global to local, got %q", result)
	}
}

func TestLoadInstructionSkipsOversizedParentContext(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	oversized := strings.Repeat("A", maxInstructionFileSize+1)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(oversized), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(child, "CLAUDE.md"), []byte("# Local Claude\n- Local"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	result := loadInstructionFrom("Base instruction.", child, "")

	if strings.Contains(result, oversized) {
		t.Fatalf("LoadInstruction() should skip oversized parent context")
	}
	if !strings.Contains(result, "Local Claude") {
		t.Fatalf("LoadInstruction() should keep later valid context files, got %q", result)
	}
}

func TestLoadInstructionWithOversizedAgentsFile(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".pi-go")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	oversized := strings.Repeat("A", maxInstructionFileSize+1)
	if err := os.WriteFile(filepath.Join(agentsDir, "AGENTS.md"), []byte(oversized), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error: %v", err)
	}
	defer os.Chdir(origDir)

	base := "Base instruction."
	result := LoadInstruction(base)

	if !strings.Contains(result, base) {
		t.Fatalf("LoadInstruction() should contain base instruction, got %q", result)
	}
	if strings.Contains(result, "# Project Rules") {
		t.Fatalf("LoadInstruction() should skip oversized AGENTS.md, got %q", result)
	}
	if strings.Contains(result, oversized) {
		t.Fatalf("LoadInstruction() should not include oversized AGENTS.md contents")
	}
}

func TestLoadInstructionWithSkills(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".pi-go", "skills", "lint")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	content := `---
description: Run lint checks before finishing.
---
# Lint Skill
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error: %v", err)
	}
	defer os.Chdir(origDir)

	result := LoadInstruction("Base instruction.")
	if skills, err := extension.LoadSkills(filepath.Join(dir, ".pi-go", "skills")); err != nil {
		t.Fatalf("LoadSkills() error: %v", err)
	} else if len(skills) == 0 {
		t.Fatalf("LoadSkills() returned no skills")
	}
	if !strings.Contains(result, "# Available Skills") {
		t.Fatalf("LoadInstruction() should contain skills section, got %q", result)
	}
	if !strings.Contains(result, "/lint: Run lint checks before finishing.") {
		t.Fatalf("LoadInstruction() should contain discovered skill summary, got %q", result)
	}
}

func TestAppendActiveSkill(t *testing.T) {
	// AppendActiveSkill adds an "# Active Skill" block that includes the
	// full skill body, on top of whatever the caller already has (e.g. the
	// output of LoadInstruction).
	base := "Base instruction.\n\n# Available Skills\n\n- /ponytail: lazy mode\n"
	skill := extension.Skill{Name: "ponytail", Description: "lazy mode"}
	got := AppendActiveSkill(base, skill, "Be lazy. Prefer stdlib.")
	if !strings.Contains(got, "Base instruction.") {
		t.Errorf("missing base, got %q", got)
	}
	if !strings.Contains(got, "# Active Skill: ponytail") {
		t.Errorf("missing active skill header, got %q", got)
	}
	if !strings.Contains(got, "Be lazy. Prefer stdlib.") {
		t.Errorf("missing skill body, got %q", got)
	}
	// Active Skill block should come after the Available Skills menu.
	menuIdx := strings.Index(got, "# Available Skills")
	activeIdx := strings.Index(got, "# Active Skill")
	if menuIdx < 0 || activeIdx < 0 || activeIdx <= menuIdx {
		t.Errorf("Active Skill should follow Available Skills, got menu=%d active=%d", menuIdx, activeIdx)
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
	sessionID, _, err := a.CreateSession(ctx)
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
			// Tool result fed back — no assertion needed, just verifying it's present.
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
	sessionID, _, err := a.CreateSession(ctx)
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
	sessionID, _, err := a.CreateSession(ctx)
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
	sessionID, _, _ := a.CreateSession(ctx)

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
	sessionID, _, _ := a.CreateSession(ctx)

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
	sessionID, _, _ := a.CreateSession(ctx)

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
	sessionID, _, _ := a.CreateSession(ctx)

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
	sessionID, _, _ := a.CreateSession(ctx)

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

func TestRebuildWithModel(t *testing.T) {
	originalLLM := &mockLLM{name: "original-model", response: "Hello!"}
	newLLM := &mockLLM{name: "new-model", response: "Hi!"}

	a, err := New(Config{Model: originalLLM, Instruction: "Test instruction."})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Rebuild with a new LLM.
	if err := a.RebuildWithModel(newLLM); err != nil {
		t.Fatalf("RebuildWithModel() error: %v", err)
	}

	// Verify the model was swapped.
	if a.config.Model != newLLM {
		t.Errorf("config.Model was not updated to the new LLM")
	}
	// Verify instruction is preserved.
	if a.config.Instruction != "Test instruction." {
		t.Errorf("config.Instruction = %q, want %q", a.config.Instruction, "Test instruction.")
	}
}

func TestRebuildWithModelNilLLM(t *testing.T) {
	llm := &mockLLM{name: "test-model", response: "Hello!"}

	a, err := New(Config{Model: llm})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Rebuild with nil LLM should not panic (the runner will use nil model).
	// We don't call Run here, so it won't actually try to generate.
	_ = a.RebuildWithModel(nil)
	// Verify the model was set (even if nil).
	if a.config.Model != nil {
		t.Errorf("config.Model should be nil, got %T", a.config.Model)
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}
	if cfg.InitialDelay != 1*time.Second {
		t.Errorf("InitialDelay = %v, want 1s", cfg.InitialDelay)
	}
	// A minute, because the limits that produce a retryable 429 are per-minute
	// windows: the old 30s cap could only ever retry inside the same exhausted
	// window it was waiting out.
	if cfg.MaxDelay != 60*time.Second {
		t.Errorf("MaxDelay = %v, want 60s", cfg.MaxDelay)
	}
}

// capturingLLM records the LLMRequest it receives, then returns a fixed
// response. Used to assert what the runner actually sent to the model —
// in particular, what the instruction looked like after our
// InstructionProvider ran.
type capturingLLM struct {
	name     string
	response string

	mu          sync.Mutex
	lastRequest *model.LLMRequest
}

func (m *capturingLLM) Name() string { return m.name }

func (m *capturingLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	m.lastRequest = req
	m.mu.Unlock()
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText(m.response, genai.RoleModel),
		}, nil)
	}
}

func (m *capturingLLM) capturedSystemInstruction() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastRequest == nil || m.lastRequest.Config == nil {
		return ""
	}
	sc := m.lastRequest.Config.SystemInstruction
	if sc == nil {
		return ""
	}
	// Flatten all text parts into a single string for substring checks.
	var sb strings.Builder
	for _, p := range sc.Parts {
		if p != nil && p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// TestSafeInstructionProvider_UnknownStateKeyDoesNotFail is the regression
// test for the {AGT_X}-in-CLAUDE.md bug. Before the fix, an instruction
// containing a {state_var} placeholder whose key is not in session state
// caused the whole turn to abort with "failed to append instructions:
// failed to inject session state into instruction: state key does not
// exist". The safe provider must fail open: the literal {state_var}
// substring passes through, the LLM is called, and the turn completes.
func TestSafeInstructionProvider_UnknownStateKeyDoesNotFail(t *testing.T) {
	llm := &capturingLLM{name: "test-model", response: "ok"}

	a, err := New(Config{
		Model:       llm,
		Instruction: "before {unknown_xyz_placeholder} after",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	var runErr error
	for _, err := range a.Run(ctx, sessionID, "hi") {
		if err != nil {
			runErr = err
			break
		}
	}
	if runErr != nil {
		t.Fatalf("Run() yielded error (regression: {state_var} should not abort): %v", runErr)
	}

	// Sanity check: the literal {unknown_xyz_placeholder} was passed through.
	if got := llm.capturedSystemInstruction(); !strings.Contains(got, "{unknown_xyz_placeholder}") {
		t.Errorf("system instruction should contain the literal placeholder, got %q", got)
	}
}

// TestSafeInstructionProvider_KnownStateKeyIsSubstituted asserts the
// positive case still works: a {app:foo} placeholder whose key IS set in
// session state must be substituted into the instruction.
func TestSafeInstructionProvider_KnownStateKeyIsSubstituted(t *testing.T) {
	llm := &capturingLLM{name: "test-model", response: "ok"}

	svc := session.InMemoryService()
	a, err := New(Config{
		Model:          llm,
		SessionService: svc,
		Instruction:    "hello {app:user_name}",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Set the state key the placeholder references. ADK's in-memory
	// service persists state via AppendEvent(StateDelta), not by
	// mutating the session returned from Get (which is a copy).
	seedResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    DefaultUserID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get() session: %v", err)
	}
	if err := svc.AppendEvent(ctx, seedResp.Session, &session.Event{
		Author: "test",
		Actions: session.EventActions{
			StateDelta: map[string]any{"app:user_name": "world"},
		},
	}); err != nil {
		t.Fatalf("AppendEvent(StateDelta): %v", err)
	}

	var runErr error
	for _, err := range a.Run(ctx, sessionID, "hi") {
		if err != nil {
			runErr = err
			break
		}
	}
	if runErr != nil {
		t.Fatalf("Run() yielded error: %v", runErr)
	}

	got := llm.capturedSystemInstruction()
	if !strings.Contains(got, "hello world") {
		t.Errorf("system instruction should contain substituted value, got %q", got)
	}
	if strings.Contains(got, "{app:user_name}") {
		t.Errorf("placeholder should have been substituted, got %q", got)
	}
}

// TestSafeInstructionProvider_DirectCallMissingKey covers the fail-open
// path of the provider in isolation: calling the closure with a context
// that triggers an injection error must return the original template
// without surfacing the error.
func TestSafeInstructionProvider_DirectCallMissingKey(t *testing.T) {
	// A nil ReadonlyContext is not a *icontext.ReadonlyContext, so
	// instructionutil.InjectSessionState returns an "unexpected context
	// type" error. The provider must swallow it and return the template.
	got, err := safeInstructionProvider("before {foo} after", nil)(nil)
	if err != nil {
		t.Fatalf("safeInstructionProvider returned error: %v", err)
	}
	if got != "before {foo} after" {
		t.Errorf("safeInstructionProvider returned %q, want original template", got)
	}
}

// TestSafeInstructionProvider_MixedPlaceholders is the regression test for
// the all-or-nothing fallback: when a single template contains both a
// real state placeholder and a stray prose token, the real one must still
// be substituted. A naive "if any error then return the original template"
// implementation would drop the legitimate substitution; the per-placeholder
// walk in injectWithFailOpen must keep both behaviors independently.
func TestSafeInstructionProvider_MixedPlaceholders(t *testing.T) {
	llm := &capturingLLM{name: "test-model", response: "ok"}

	svc := session.InMemoryService()
	a, err := New(Config{
		Model:          llm,
		SessionService: svc,
		// One real key (app:user_name) and one stray token ({AGT_X})
		// that looks like a state key but isn't.
		Instruction: "real={app:user_name}, stray={AGT_X}",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	sessionID, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Set only the real key.
	seedResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    DefaultUserID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get() session: %v", err)
	}
	if err := svc.AppendEvent(ctx, seedResp.Session, &session.Event{
		Author: "test",
		Actions: session.EventActions{
			StateDelta: map[string]any{"app:user_name": "world"},
		},
	}); err != nil {
		t.Fatalf("AppendEvent(StateDelta): %v", err)
	}

	var runErr error
	for _, err := range a.Run(ctx, sessionID, "hi") {
		if err != nil {
			runErr = err
			break
		}
	}
	if runErr != nil {
		t.Fatalf("Run() yielded error: %v", runErr)
	}

	got := llm.capturedSystemInstruction()
	if !strings.Contains(got, "real=world") {
		t.Errorf("real placeholder should have been substituted, got %q", got)
	}
	if !strings.Contains(got, "stray={AGT_X}") {
		t.Errorf("stray placeholder should have been left as literal, got %q", got)
	}
	if strings.Contains(got, "{app:user_name}") {
		t.Errorf("real placeholder should not appear unsubstituted, got %q", got)
	}
}

// TestInjectWithFailOpen_PerPlaceholder exercises the slicing logic
// directly. It is the unit-level complement to the e2e mixed-placeholder
// test: here we don't need a real session because every per-placeholder
// call returns an error (nil ctx triggers the type-assertion failure in
// instructionutil.InjectSessionState), so every match falls back to its
// literal form.
func TestInjectWithFailOpen_PerPlaceholder(t *testing.T) {
	cases := []struct {
		name     string
		template string
		want     string
	}{
		{"no placeholders", "plain text", "plain text"},
		{"one stray placeholder", "x {foo} y", "x {foo} y"},
		{"two stray placeholders", "{a} and {b}", "{a} and {b}"},
		{"literal-looking but invalid name", "{a-b} and {a b}", "{a-b} and {a b}"},
		{"braces in non-placeholder context", "shell {echo $1} style", "shell {echo $1} style"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := injectWithFailOpen(nil, tc.template, nil)
			if got != tc.want {
				t.Errorf("injectWithFailOpen(%q) = %q, want %q", tc.template, got, tc.want)
			}
		})
	}
}
