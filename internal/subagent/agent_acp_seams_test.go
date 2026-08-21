package subagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
)

func TestApplyAgentFrontmatterKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		start AgentConfig
		key   string
		value string
		want  AgentConfig
	}{
		{"name", AgentConfig{Name: "from-file"}, "name", "explore", AgentConfig{Name: "explore"}},
		{"description", AgentConfig{}, "description", "does things", AgentConfig{Description: "does things"}},
		{"role", AgentConfig{}, "role", "smol", AgentConfig{Role: "smol"}},
		{"worktree true", AgentConfig{}, "worktree", "true", AgentConfig{Worktree: true}},
		{"worktree is case-insensitive", AgentConfig{}, "worktree", "TRUE", AgentConfig{Worktree: true}},
		{"worktree anything else is false", AgentConfig{}, "worktree", "yes", AgentConfig{}},
		{"worktree false clears a prior true", AgentConfig{Worktree: true}, "worktree", "false", AgentConfig{}},
		{"lsp is lowercased", AgentConfig{}, "lsp", "  Full  ", AgentConfig{LSP: "full"}},
		{"tools split and trimmed", AgentConfig{}, "tools", "read, write ,edit", AgentConfig{Tools: []string{"read", "write", "edit"}}},
		{"tools drops empty entries", AgentConfig{}, "tools", "read,,  ,edit", AgentConfig{Tools: []string{"read", "edit"}}},
		{"tools appends to existing", AgentConfig{Tools: []string{"bash"}}, "tools", "read", AgentConfig{Tools: []string{"bash", "read"}}},
		{"timeout applied", AgentConfig{}, "timeout", "5000", AgentConfig{Timeout: 5000}},
		{"unknown key is ignored", AgentConfig{Name: "x"}, "nonsense", "blue", AgentConfig{Name: "x"}},
		{"empty key is ignored", AgentConfig{Name: "x"}, "", "v", AgentConfig{Name: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.start
			applyAgentFrontmatterKey(&got, tt.key, tt.value)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("applyAgentFrontmatterKey mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAgentTimeoutFrom pins the milliseconds-vs-seconds guard: a bundled agent
// once shipped `timeout: 30` and was SIGKILLed 30ms in, every run.
func TestAgentTimeoutFrom(t *testing.T) {
	t.Parallel()

	const current = 7777

	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"plausible value applied", "5000", 5000},
		{"exactly the floor is applied", "1000", minAgentTimeoutMs},
		{"just under the floor is ignored", "999", current},
		{"seconds-looking value is ignored", "30", current},
		{"zero is ignored", "0", current},
		{"negative is ignored", "-1", current},
		{"non-numeric is ignored", "30s", current},
		{"empty is ignored", "", current},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := agentTimeoutFrom(tt.value, "test-agent", current); got != tt.want {
				t.Errorf("agentTimeoutFrom(%q, current=%d) = %d, want %d", tt.value, current, got, tt.want)
			}
		})
	}
}

// TestParseAgentContentTimeoutRoundTrip checks the extracted helpers still
// compose the way the whole-file parser needs them to.
func TestParseAgentContentTimeoutRoundTrip(t *testing.T) {
	t.Parallel()

	content := "---\n" +
		"name: explore\n" +
		"description: look around\n" +
		"role: smol\n" +
		"worktree: true\n" +
		"timeout: 30\n" + // implausible: must be ignored
		"lsp: FULL\n" +
		"tools: read, write\n" +
		"---\n" +
		"Body line one.\n"

	cfg, err := parseAgentContent(content, "/agents/fallback.md")
	if err != nil {
		t.Fatalf("parseAgentContent: %v", err)
	}
	want := AgentConfig{
		Name:        "explore",
		Description: "look around",
		Role:        "smol",
		Worktree:    true,
		LSP:         "full",
		Tools:       []string{"read", "write"},
		Instruction: "Body line one.",
	}
	if diff := cmp.Diff(want, cfg); diff != "" {
		t.Errorf("parseAgentContent mismatch (-want +got):\n%s", diff)
	}
}

func TestACPEventFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   sharedacp.Event
		want Event
	}{
		{
			name: "message becomes text_delta",
			in:   sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "hi", SessionID: "s"},
			want: Event{Type: "text_delta", Content: "hi", SessionID: "s"},
		},
		{
			name: "progress becomes tool_call",
			in:   sharedacp.Event{Type: sharedacp.EventTypeProgress, Content: "step", SessionID: "s"},
			want: Event{Type: "tool_call", Content: "step", SessionID: "s"},
		},
		{
			name: "tool becomes tool_call",
			in:   sharedacp.Event{Type: sharedacp.EventTypeTool, Content: "grep", SessionID: "s"},
			want: Event{Type: "tool_call", Content: "grep", SessionID: "s"},
		},
		{
			name: "stderr keeps its own type",
			in:   sharedacp.Event{Type: sharedacp.EventTypeStderr, Content: "warn", SessionID: "s"},
			want: Event{Type: "stderr", Content: "warn", SessionID: "s"},
		},
		{
			name: "error carries Error, not Content",
			in:   sharedacp.Event{Type: sharedacp.EventTypeError, Error: "boom", Content: "ignored", SessionID: "s"},
			want: Event{Type: "error", Error: "boom", SessionID: "s"},
		},
		{
			name: "unknown type passes through",
			in:   sharedacp.Event{Type: "thinking", Content: "hmm", SessionID: "s"},
			want: Event{Type: "thinking", Content: "hmm", SessionID: "s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, acpEventFor(tt.in)); diff != "" {
				t.Errorf("acpEventFor mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApplyGracefulCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   sharedacp.RunResult
		want sharedacp.RunResult
	}{
		{
			name: "kill error is coerced to success and sentinel stripped",
			in: sharedacp.RunResult{
				Status: sharedacp.StatusError,
				Error:  "signal: killed",
				Result: "all done " + acpCompletionSentinel,
			},
			want: sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "all done"},
		},
		{
			name: "loose sentinel form is stripped too",
			in:   sharedacp.RunResult{Result: "done " + acpCompletionMatcher},
			want: sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "done"},
		},
		{
			name: "other fields are preserved",
			in:   sharedacp.RunResult{SessionID: "s", StopReason: "end_turn", Stderr: "noise", Result: "x"},
			want: sharedacp.RunResult{Status: sharedacp.StatusSuccess, SessionID: "s", StopReason: "end_turn", Stderr: "noise", Result: "x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, applyGracefulCompletion(tt.in)); diff != "" {
				t.Errorf("applyGracefulCompletion mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestACPErrorMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   sharedacp.RunResult
		want string
	}{
		{"error only", sharedacp.RunResult{Error: "boom"}, "boom"},
		{"stderr only", sharedacp.RunResult{Stderr: "trace"}, "stderr: trace"},
		{"both are joined", sharedacp.RunResult{Error: "boom", Stderr: "trace"}, "boom\nstderr: trace"},
		{"neither gets a fallback", sharedacp.RunResult{}, "subprocess failed"},
		{"whitespace-only stderr is not appended", sharedacp.RunResult{Error: "boom", Stderr: "   \n "}, "boom"},
		{"whitespace-only everything gets the fallback", sharedacp.RunResult{Error: "  ", Stderr: " "}, "subprocess failed"},
		{"fields are trimmed", sharedacp.RunResult{Error: "  boom  ", Stderr: "  trace  "}, "boom\nstderr: trace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, acpErrorMessage(tt.in)); diff != "" {
				t.Errorf("acpErrorMessage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestStashBeforeWorktree exercises the stash seam against a throwaway repo
// created by initTestRepo. It never touches the real checkout.
func TestStashBeforeWorktree(t *testing.T) {
	t.Run("clean tree stashes nothing", func(t *testing.T) {
		repo := initTestRepo(t)
		m := NewWorktreeManager(repo)

		msg, stashed, err := m.stashBeforeWorktree("agent-1")
		if err != nil {
			t.Fatalf("stashBeforeWorktree: %v", err)
		}
		if stashed {
			t.Error("stashed = true on a clean tree, want false")
		}
		if !strings.Contains(msg, "agent-1") {
			t.Errorf("stash message %q does not identify the agent", msg)
		}
		if out, _ := m.git("stash", "list"); strings.TrimSpace(out) != "" {
			t.Errorf("stash list = %q, want empty", out)
		}
	})

	t.Run("dirty tree stashes and is recoverable by message", func(t *testing.T) {
		repo := initTestRepo(t)
		m := NewWorktreeManager(repo)
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
			t.Fatal(err)
		}

		msg, stashed, err := m.stashBeforeWorktree("agent-2")
		if err != nil {
			t.Fatalf("stashBeforeWorktree: %v", err)
		}
		if !stashed {
			t.Fatal("stashed = false on a dirty tree, want true")
		}
		// The untracked file must have been swept up (-u) and be restorable.
		if _, statErr := os.Stat(filepath.Join(repo, "dirty.txt")); !os.IsNotExist(statErr) {
			t.Errorf("dirty.txt still present after stash, stat err = %v", statErr)
		}
		out, _ := m.git("stash", "list")
		if !strings.Contains(out, msg) {
			t.Errorf("stash list %q does not contain the unique message %q", out, msg)
		}
		if err := m.popStashByMessage(msg); err != nil {
			t.Fatalf("popStashByMessage: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(repo, "dirty.txt")); statErr != nil {
			t.Errorf("dirty.txt not restored after pop: %v", statErr)
		}
	})

	t.Run("non-repo is a fatal error, not a clean tree", func(t *testing.T) {
		m := NewWorktreeManager(t.TempDir())

		_, stashed, err := m.stashBeforeWorktree("agent-3")
		if err == nil {
			t.Fatal("stashBeforeWorktree() = nil error outside a git repo, want a failure")
		}
		if !strings.Contains(err.Error(), "git stash before worktree") {
			t.Errorf("error = %q, want it to name the stash step", err)
		}
		if stashed {
			t.Error("stashed = true on failure, want false")
		}
	})
}

func TestAddWorktree(t *testing.T) {
	t.Run("creates a new branch when it does not exist", func(t *testing.T) {
		repo := initTestRepo(t)
		m := NewWorktreeManager(repo)
		wtPath := filepath.Join(repo, ".pi-go", "tasks", "fresh")

		if _, err := m.addWorktree("pi-agent-fresh", wtPath, false); err != nil {
			t.Fatalf("addWorktree: %v", err)
		}
		if !m.branchExists("pi-agent-fresh") {
			t.Error("branch was not created")
		}
		if _, err := os.Stat(wtPath); err != nil {
			t.Errorf("worktree dir missing: %v", err)
		}
	})

	t.Run("attaches an existing branch without -b", func(t *testing.T) {
		repo := initTestRepo(t)
		m := NewWorktreeManager(repo)
		cmd := exec.Command("git", "branch", "prebuilt")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v: %s", err, out)
		}
		wtPath := filepath.Join(repo, ".pi-go", "tasks", "attached")

		// The -b form would fail here with "a branch named X already exists".
		if _, err := m.addWorktree("prebuilt", wtPath, true); err != nil {
			t.Fatalf("addWorktree on an existing branch: %v", err)
		}
		if _, err := os.Stat(wtPath); err != nil {
			t.Errorf("worktree dir missing: %v", err)
		}
	})
}

// TestMergeAgentLayer pins the precedence rule DiscoverAgents relies on: a
// later layer replaces a same-named agent IN PLACE, so the merged order is the
// order names were first seen, not the order the winning layer supplied them.
func TestMergeAgentLayer(t *testing.T) {
	t.Parallel()

	agent := func(name, source string) AgentConfig {
		return AgentConfig{Name: name, Source: source}
	}

	tests := []struct {
		name   string
		layers [][]AgentConfig
		want   []AgentConfig
	}{
		{
			name:   "no layers",
			layers: nil,
			want:   nil,
		},
		{
			name:   "single layer passes through",
			layers: [][]AgentConfig{{agent("a", "bundled"), agent("b", "bundled")}},
			want:   []AgentConfig{agent("a", "bundled"), agent("b", "bundled")},
		},
		{
			name: "later layer overrides in place, keeping first-seen order",
			layers: [][]AgentConfig{
				{agent("a", "bundled"), agent("b", "bundled")},
				{agent("b", "user")},
			},
			want: []AgentConfig{agent("a", "bundled"), agent("b", "user")},
		},
		{
			name: "project beats user beats bundled",
			layers: [][]AgentConfig{
				{agent("x", "bundled")},
				{agent("x", "user")},
				{agent("x", "project")},
			},
			want: []AgentConfig{agent("x", "project")},
		},
		{
			name: "new names append after the ones already seen",
			layers: [][]AgentConfig{
				{agent("a", "bundled")},
				{agent("z", "user"), agent("a", "user")},
			},
			want: []AgentConfig{agent("a", "user"), agent("z", "user")},
		},
		{
			name: "a duplicate within one layer takes the last occurrence",
			layers: [][]AgentConfig{
				{agent("a", "first"), agent("a", "second")},
			},
			want: []AgentConfig{agent("a", "second")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var all []AgentConfig
			seen := make(map[string]int)
			for _, layer := range tt.layers {
				all = mergeAgentLayer(all, seen, layer)
			}
			if diff := cmp.Diff(tt.want, all); diff != "" {
				t.Errorf("mergeAgentLayer mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
