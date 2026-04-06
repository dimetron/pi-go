package tui

import (
	"context"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	llmmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestCommitCommand_NoLLM(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{GenerateCommitMsg: nil},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd when no LLM configured")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestCommitCommand_NotARepo(t *testing.T) {
	dir := t.TempDir() // not a git repo

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			WorkDir:           dir,
			GenerateCommitMsg: func(ctx context.Context, diffs string) (string, error) { return "", nil },
		},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd for non-repo")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Not a git repository") {
		t.Errorf("expected 'Not a git repository' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestCommitCommand_NoStagedChanges(t *testing.T) {
	dir := setupGitRepo(t)

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			WorkDir:           dir,
			GenerateCommitMsg: func(ctx context.Context, diffs string) (string, error) { return "", nil },
		},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd when no staged changes")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "No staged changes") {
		t.Errorf("expected 'No staged changes' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestCommitCommand_GeneratesConventionalFormat(t *testing.T) {
	dir := setupGitRepo(t)

	// Create and stage a file.
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	gitExec(t, dir, "add", "main.go")

	expectedMsg := "feat(main): add entry point"

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	m := &model{
		ctx:       ctx,
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			WorkDir: dir,
			GenerateCommitMsg: func(ctx context.Context, diffs string) (string, error) {
				// Verify diffs contain the file content.
				if !strings.Contains(diffs, "main.go") {
					t.Errorf("expected diffs to contain 'main.go', got %q", diffs)
				}
				return expectedMsg, nil
			},
		},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd == nil {
		t.Fatal("expected non-nil cmd for async LLM call")
	}
	if mm.commit == nil || mm.commit.phase != "generating" {
		t.Error("expected commit state phase 'generating'")
	}

	// Execute the cmd to simulate the LLM call.
	result := cmd()
	genMsg, ok := result.(commitGeneratedMsg)
	if !ok {
		t.Fatalf("expected commitGeneratedMsg, got %T", result)
	}
	if genMsg.err != nil {
		t.Fatalf("unexpected error: %v", genMsg.err)
	}
	if genMsg.message != expectedMsg {
		t.Errorf("expected %q, got %q", expectedMsg, genMsg.message)
	}

	// Process the generated message.
	newM2, _ := mm.handleCommitGenerated(genMsg)
	mm2 := newM2.(*model)

	if mm2.commit == nil || mm2.commit.phase != "confirming" {
		t.Error("expected commit state phase 'confirming'")
	}
	if mm2.commit.message != expectedMsg {
		t.Errorf("expected commit message %q, got %q", expectedMsg, mm2.commit.message)
	}
}

func TestCommitCommand_ConfirmCommits(t *testing.T) {
	dir := setupGitRepo(t)

	// Create and stage a file.
	writeFile(t, filepath.Join(dir, "hello.txt"), "hello world\n")
	gitExec(t, dir, "add", "hello.txt")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	commitMsg := "feat: add hello"
	m := &model{
		ctx:       ctx,
		chatModel: ChatModel{Messages: make([]message, 0)},
		commit:    &commitState{phase: "confirming", message: commitMsg},
		cfg:       Config{WorkDir: dir},
	}

	// Confirm the commit.
	newM, cmd := m.handleCommitConfirm()
	mm := newM.(*model)

	if cmd == nil {
		t.Fatal("expected non-nil cmd for git commit")
	}
	if mm.commit != nil {
		t.Error("expected commit state to be cleared")
	}

	// Execute the git commit.
	result := cmd()
	doneMsg, ok := result.(commitDoneMsg)
	if !ok {
		t.Fatalf("expected commitDoneMsg, got %T", result)
	}
	if doneMsg.err != nil {
		t.Fatalf("commit failed: %v\n%s", doneMsg.err, doneMsg.output)
	}

	// Verify the commit was created.
	log, err := gitCmd(dir, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(log, "feat: add hello") {
		t.Errorf("expected commit message in log, got %q", log)
	}
}

func TestCommitCommand_CancelCommit(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		commit:    &commitState{phase: "confirming", message: "some message"},
	}

	newM, _ := m.handleCommitCancel()
	mm := newM.(*model)

	if mm.commit != nil {
		t.Error("expected commit state to be nil after cancel")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "canceled") {
		t.Errorf("expected 'cancelled' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestCommitCommand_LLMError(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "assistant", content: "Generating..."}}},
		commit:    &commitState{phase: "generating"},
	}

	newM, _ := m.handleCommitGenerated(commitGeneratedMsg{
		err: fmt.Errorf("API rate limit exceeded"),
	})
	mm := newM.(*model)

	if mm.commit != nil {
		t.Error("expected commit state to be nil after error")
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Error generating") {
		t.Errorf("expected error message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestCommitParsePorcelain(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		staged    []string
		unstaged  []string
		untracked []string
	}{
		{
			name:   "staged modification",
			input:  "M  file.go\n",
			staged: []string{"M file.go"},
		},
		{
			name:     "unstaged modification",
			input:    " M file.go\n",
			unstaged: []string{"M file.go"},
		},
		{
			name:      "untracked file",
			input:     "?? newfile.go\n",
			untracked: []string{"newfile.go"},
		},
		{
			name:     "staged and unstaged",
			input:    "MM file.go\n",
			staged:   []string{"M file.go"},
			unstaged: []string{"M file.go"},
		},
		{
			name:   "added file",
			input:  "A  file.go\n",
			staged: []string{"A file.go"},
		},
		{
			name:   "renamed file",
			input:  "R  old.go -> new.go\n",
			staged: []string{"R new.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staged, unstaged, untracked := commitParsePorcelain(tt.input)

			if len(staged) != len(tt.staged) {
				t.Errorf("staged: expected %d, got %d: %v", len(tt.staged), len(staged), staged)
			}
			for i, s := range staged {
				if i < len(tt.staged) && s != tt.staged[i] {
					t.Errorf("staged[%d]: expected %q, got %q", i, tt.staged[i], s)
				}
			}

			if len(unstaged) != len(tt.unstaged) {
				t.Errorf("unstaged: expected %d, got %d: %v", len(tt.unstaged), len(unstaged), unstaged)
			}

			if len(untracked) != len(tt.untracked) {
				t.Errorf("untracked: expected %d, got %d: %v", len(tt.untracked), len(untracked), untracked)
			}
		})
	}
}

func TestHandleSlashCommandHelpContainsCommit(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/help"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleSlashCommand("/help")
	mm := newM.(*model)

	if !strings.Contains(mm.chatModel.Messages[0].content, "/commit") {
		t.Errorf("expected /help to mention /commit, got %q", mm.chatModel.Messages[0].content)
	}
}

// Test helpers

func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.email", "test@test.com")
	gitExec(t, dir, "config", "user.name", "Test")

	// Create initial commit so we have a HEAD.
	writeFile(t, filepath.Join(dir, "README.md"), "# Test\n")
	gitExec(t, dir, "add", "README.md")
	gitExec(t, dir, "commit", "-m", "initial commit")

	return dir
}

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing file %s: %v", path, err)
	}
}

func TestNormalizeCommitMessage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`"feat(auth): add login"`, "feat(auth): add login"},
		{"  fix(bug): null check  ", "fix(bug): null check"},
		{"`refactor(core): cleanup`", "refactor(core): cleanup"},
		{"'chore: update deps'", "chore: update deps"},
		{"plain message", "plain message"},
		{"  ", ""},
		{"", ""},
		{`"""triple quotes"""`, `triple quotes`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeCommitMessage(tt.input)
			if got != tt.want {
				t.Errorf("normalizeCommitMessage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// commitMockLLM is a configurable mock for GenerateCommitMsgFunc tests.
type commitMockLLM struct {
	// responses is the sequence of (parts, error) to yield.
	responses []commitMockResponse
}

type commitMockResponse struct {
	parts []string // text parts in Content; nil means Content is nil
	err   error
}

func (m *commitMockLLM) Name() string { return "commit-mock" }

func (m *commitMockLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		for _, r := range m.responses {
			if r.err != nil {
				if !yield(nil, r.err) {
					return
				}
				continue
			}
			resp := &llmmodel.LLMResponse{}
			if r.parts != nil {
				parts := make([]*genai.Part, len(r.parts))
				for i, txt := range r.parts {
					parts[i] = &genai.Part{Text: txt}
				}
				resp.Content = &genai.Content{
					Role:  string(genai.RoleModel),
					Parts: parts,
				}
			}
			if !yield(resp, nil) {
				return
			}
		}
	}
}

func TestGenerateCommitMsgFunc_Success(t *testing.T) {
	llm := &commitMockLLM{
		responses: []commitMockResponse{
			{parts: []string{"feat(auth): add OAuth2 login flow"}},
		},
	}

	fn := GenerateCommitMsgFunc(llm)
	msg, err := fn(context.Background(), "diff --git a/auth.go ...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "feat(auth): add OAuth2 login flow" {
		t.Errorf("expected %q, got %q", "feat(auth): add OAuth2 login flow", msg)
	}
}

func TestGenerateCommitMsgFunc_LLMError(t *testing.T) {
	llm := &commitMockLLM{
		responses: []commitMockResponse{
			{err: fmt.Errorf("rate limit exceeded")},
		},
	}

	fn := GenerateCommitMsgFunc(llm)
	_, err := fn(context.Background(), "some diff")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "LLM error") {
		t.Errorf("expected wrapped LLM error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("expected original error message, got %q", err.Error())
	}
}

func TestGenerateCommitMsgFunc_EmptyResponse(t *testing.T) {
	llm := &commitMockLLM{
		responses: []commitMockResponse{
			{parts: []string{""}},
		},
	}

	fn := GenerateCommitMsgFunc(llm)
	_, err := fn(context.Background(), "some diff")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
	if !strings.Contains(err.Error(), "LLM returned empty commit message") {
		t.Errorf("expected empty message error, got %q", err.Error())
	}
}

func TestGenerateCommitMsgFunc_NilContent(t *testing.T) {
	llm := &commitMockLLM{
		responses: []commitMockResponse{
			{parts: nil}, // Content will be nil
		},
	}

	fn := GenerateCommitMsgFunc(llm)
	_, err := fn(context.Background(), "some diff")
	if err == nil {
		t.Fatal("expected error for nil content, got nil")
	}
	if !strings.Contains(err.Error(), "LLM returned empty commit message") {
		t.Errorf("expected empty message error, got %q", err.Error())
	}
}

func TestGenerateCommitMsgFunc_NormalizesQuotesAndWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{
			name:     "surrounding double quotes",
			raw:      `"fix(core): handle nil pointer"`,
			expected: "fix(core): handle nil pointer",
		},
		{
			name:     "surrounding whitespace",
			raw:      "  chore: update deps  ",
			expected: "chore: update deps",
		},
		{
			name:     "surrounding backticks",
			raw:      "`refactor(tui): simplify render`",
			expected: "refactor(tui): simplify render",
		},
		{
			name:     "surrounding single quotes",
			raw:      "'feat: add new command'",
			expected: "feat: add new command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &commitMockLLM{
				responses: []commitMockResponse{
					{parts: []string{tt.raw}},
				},
			}
			fn := GenerateCommitMsgFunc(llm)
			msg, err := fn(context.Background(), "diff")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, msg)
			}
		})
	}
}

func TestGenerateCommitMsgFunc_MultiPartResponse(t *testing.T) {
	llm := &commitMockLLM{
		responses: []commitMockResponse{
			{parts: []string{"feat(api):", " add pagination", " support"}},
		},
	}

	fn := GenerateCommitMsgFunc(llm)
	msg, err := fn(context.Background(), "diff with pagination changes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "feat(api): add pagination support" {
		t.Errorf("expected %q, got %q", "feat(api): add pagination support", msg)
	}
}

func TestGenerateCommitMsgFunc_MultiYieldResponse(t *testing.T) {
	// Simulates streaming: multiple yields each with a part.
	llm := &commitMockLLM{
		responses: []commitMockResponse{
			{parts: []string{"fix(db): "}},
			{parts: []string{"close connection on error"}},
		},
	}

	fn := GenerateCommitMsgFunc(llm)
	msg, err := fn(context.Background(), "diff with db fix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "fix(db): close connection on error" {
		t.Errorf("expected %q, got %q", "fix(db): close connection on error", msg)
	}
}
