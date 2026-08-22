package subagent

// Tests for the helpers extracted while reducing cyclomatic complexity in
// agents.go, orchestrator.go, spawner.go, spawner_acp.go and worktree.go.
//
// Each helper is exercised at the boundaries its original branch encoded, so a
// change in branch structure shows up as a failing case here rather than as a
// silent behavior change in a long function.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
)

// ---------------------------------------------------------------------------
// agents.go
// ---------------------------------------------------------------------------

func TestApplyAgentFrontmatterKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		check func(t *testing.T, cfg AgentConfig)
	}{
		{"name", "name", "explore", func(t *testing.T, c AgentConfig) {
			if c.Name != "explore" {
				t.Errorf("Name = %q", c.Name)
			}
		}},
		{"description", "description", "does things", func(t *testing.T, c AgentConfig) {
			if c.Description != "does things" {
				t.Errorf("Description = %q", c.Description)
			}
		}},
		{"role", "role", "smol", func(t *testing.T, c AgentConfig) {
			if c.Role != "smol" {
				t.Errorf("Role = %q", c.Role)
			}
		}},
		{"worktree true", "worktree", "TRUE", func(t *testing.T, c AgentConfig) {
			if !c.Worktree {
				t.Error("Worktree = false, want true")
			}
		}},
		{"worktree other", "worktree", "yes", func(t *testing.T, c AgentConfig) {
			if c.Worktree {
				t.Error("Worktree = true, want false")
			}
		}},
		{"timeout accepted", "timeout", "60000", func(t *testing.T, c AgentConfig) {
			if c.Timeout != 60000 {
				t.Errorf("Timeout = %d", c.Timeout)
			}
		}},
		{"timeout too small leaves default", "timeout", "30", func(t *testing.T, c AgentConfig) {
			if c.Timeout != 0 {
				t.Errorf("Timeout = %d, want 0", c.Timeout)
			}
		}},
		{"lsp lowercased", "lsp", "  FULL  ", func(t *testing.T, c AgentConfig) {
			if c.LSP != "full" {
				t.Errorf("LSP = %q", c.LSP)
			}
		}},
		{"tools split", "tools", "read, write ,, edit", func(t *testing.T, c AgentConfig) {
			want := []string{"read", "write", "edit"}
			if len(c.Tools) != len(want) {
				t.Fatalf("Tools = %v", c.Tools)
			}
			for i := range want {
				if c.Tools[i] != want[i] {
					t.Errorf("Tools[%d] = %q, want %q", i, c.Tools[i], want[i])
				}
			}
		}},
		{"unknown key ignored", "unknown-key", "blue", func(t *testing.T, c AgentConfig) {
			if !reflect.DeepEqual(c, AgentConfig{}) {
				t.Errorf("unknown key mutated cfg: %+v", c)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg AgentConfig
			applyAgentFrontmatterKey(&cfg, tt.key, tt.value)
			tt.check(t, cfg)
		})
	}
}

// TestApplyAgentFrontmatterKey_ToolsAccumulate pins the append semantics: two
// `tools:` lines add up rather than the second replacing the first.
func TestApplyAgentFrontmatterKey_ToolsAccumulate(t *testing.T) {
	var cfg AgentConfig
	applyAgentFrontmatterKey(&cfg, "tools", "read")
	applyAgentFrontmatterKey(&cfg, "tools", "write")
	if len(cfg.Tools) != 2 || cfg.Tools[0] != "read" || cfg.Tools[1] != "write" {
		t.Errorf("Tools = %v, want [read write]", cfg.Tools)
	}
}

func TestParseAgentTimeout(t *testing.T) {
	tests := []struct {
		value   string
		wantMs  int
		wantOK  bool
		comment string
	}{
		{"60000", 60000, true, "plausible millisecond value"},
		{"1000", 1000, true, "exactly the minimum is honored"},
		{"999", 0, false, "just under the minimum is a unit mistake"},
		{"30", 0, false, "seconds-looking value ignored"},
		{"0", 0, false, "zero means unset"},
		{"-5", 0, false, "negative ignored"},
		{"abc", 0, false, "non-numeric ignored"},
		{"", 0, false, "empty ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.value+"/"+tt.comment, func(t *testing.T) {
			gotMs, gotOK := parseAgentTimeout("agent", tt.value)
			if gotMs != tt.wantMs || gotOK != tt.wantOK {
				t.Errorf("parseAgentTimeout(%q) = (%d, %v), want (%d, %v)",
					tt.value, gotMs, gotOK, tt.wantMs, tt.wantOK)
			}
		})
	}
}

func TestParseAgentToolList(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{"read, write, edit", []string{"read", "write", "edit"}},
		{"  read  ", []string{"read"}},
		{"read,,write", []string{"read", "write"}},
		{",", nil},
		{"", nil},
		{"   ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got := parseAgentToolList(tt.value)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAgentToolList(%q) = %v, want %v", tt.value, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadAgentsWithSource(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("alpha.md", "---\nname: alpha\n---\nbody\n")
	write("beta.md", "---\nname: beta\n---\nbody\n")
	write("ignored.txt", "not markdown")

	agents, err := loadAgentsWithSource(dir, "project")
	if err != nil {
		t.Fatalf("loadAgentsWithSource: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2: %+v", len(agents), agents)
	}
	for _, a := range agents {
		if a.Source != "project" {
			t.Errorf("agent %q Source = %q, want project", a.Name, a.Source)
		}
	}

	// A missing directory is not an error, and stamps nothing.
	missing, err := loadAgentsWithSource(filepath.Join(dir, "nope"), "user")
	if err != nil {
		t.Fatalf("missing dir returned error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing dir returned %d agents", len(missing))
	}
}

func TestMergeAgentsByName(t *testing.T) {
	bundled := []AgentConfig{{Name: "a", Source: "bundled"}, {Name: "b", Source: "bundled"}}
	user := []AgentConfig{{Name: "b", Source: "user"}, {Name: "c", Source: "user"}}
	project := []AgentConfig{{Name: "a", Source: "project"}}

	seen := make(map[string]int)
	var all []AgentConfig
	all = mergeAgentsByName(all, seen, bundled)
	all = mergeAgentsByName(all, seen, user)
	all = mergeAgentsByName(all, seen, project)

	want := []struct{ name, source string }{
		{"a", "project"}, // project overrides bundled, in bundled's slot
		{"b", "user"},    // user overrides bundled, in bundled's slot
		{"c", "user"},    // appended after the bundled entries
	}
	if len(all) != len(want) {
		t.Fatalf("merged = %+v, want %d entries", all, len(want))
	}
	for i, w := range want {
		if all[i].Name != w.name || all[i].Source != w.source {
			t.Errorf("[%d] = %q/%q, want %q/%q", i, all[i].Name, all[i].Source, w.name, w.source)
		}
	}
}

func TestMergeAgentsByName_EmptyInputIsNoOp(t *testing.T) {
	seen := map[string]int{"a": 0}
	all := []AgentConfig{{Name: "a"}}
	got := mergeAgentsByName(all, seen, nil)
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("merging nil changed the slice: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// worktree.go
// ---------------------------------------------------------------------------

func TestClaimExistingWorktree(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)
	wtPath := filepath.Join(repo, ".pi-go", "tasks", "x")

	t.Run("branch attached at the expected path is reused", func(t *testing.T) {
		reuse, err := mgr.claimExistingWorktree("agent-1", "br", wtPath, true, wtPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reuse {
			t.Error("reuse = false, want true")
		}
	})

	t.Run("branch attached elsewhere is refused", func(t *testing.T) {
		reuse, err := mgr.claimExistingWorktree("agent-1", "br", wtPath, true, filepath.Join(repo, "other"))
		if err == nil {
			t.Fatal("expected an error for a branch checked out elsewhere")
		}
		if reuse {
			t.Error("reuse = true on refusal")
		}
		if !strings.Contains(err.Error(), "already checked out") {
			t.Errorf("error = %v, want 'already checked out'", err)
		}
	})

	t.Run("branch exists but is unattached falls through", func(t *testing.T) {
		reuse, err := mgr.claimExistingWorktree("agent-1", "br", wtPath, true, "")
		if err != nil || reuse {
			t.Errorf("got (%v, %v), want (false, nil)", reuse, err)
		}
	})

	t.Run("stale directory is removed", func(t *testing.T) {
		if err := os.MkdirAll(wtPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wtPath, "leftover"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		reuse, err := mgr.claimExistingWorktree("agent-1", "br", wtPath, false, "")
		if err != nil || reuse {
			t.Fatalf("got (%v, %v), want (false, nil)", reuse, err)
		}
		if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
			t.Errorf("stale directory survived: %v", statErr)
		}
	})
}

func TestStashBeforeWorktreeAdd_CleanTree(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	msg, stashed, err := mgr.stashBeforeWorktreeAdd("agent-clean")
	if err != nil {
		t.Fatalf("stashBeforeWorktreeAdd on a clean tree: %v", err)
	}
	if stashed {
		t.Error("stashed = true on a clean tree")
	}
	if msg == "" {
		t.Error("message is empty; the pop would not be able to find its entry")
	}
	if n := stashCount(t, repo); n != 0 {
		t.Errorf("stash list has %d entries, want 0", n)
	}
}

func TestStashBeforeWorktreeAdd_DirtyTree(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, stashed, err := mgr.stashBeforeWorktreeAdd("agent-dirty")
	if err != nil {
		t.Fatalf("stashBeforeWorktreeAdd: %v", err)
	}
	if !stashed {
		t.Fatal("stashed = false, want true for a dirty tree")
	}
	if n := stashCount(t, repo); n != 1 {
		t.Fatalf("stash list has %d entries, want 1", n)
	}
	// The message must be the one findStashByMessage can locate, which is what
	// makes the eventual pop deterministic. It must also yield an object id —
	// that is what the apply addresses the entry by.
	ref, oid, found := mgr.findStashByMessage(msg)
	if !found {
		t.Errorf("stash entry for message %q not found", msg)
	}
	if ref == "" || oid == "" {
		t.Errorf("findStashByMessage returned ref=%q oid=%q, want both non-empty", ref, oid)
	}
}

func TestStashBeforeWorktreeAdd_NotARepo(t *testing.T) {
	mgr := NewWorktreeManager(t.TempDir())

	_, stashed, err := mgr.stashBeforeWorktreeAdd("agent-norepo")
	if err == nil {
		t.Fatal("expected an error outside a git repo")
	}
	if stashed {
		t.Error("stashed = true on failure")
	}
	if !strings.Contains(err.Error(), "git stash before worktree") {
		t.Errorf("error = %v, want it to name the stash step", err)
	}
}

func TestAddWorktree(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	fresh := filepath.Join(repo, ".pi-go", "tasks", "fresh")
	if err := os.MkdirAll(filepath.Dir(fresh), 0o755); err != nil {
		t.Fatal(err)
	}

	// branchExists=false creates the branch with -b.
	if err := mgr.addWorktree(fresh, "wt-new", false); err != nil {
		t.Fatalf("addWorktree(-b): %v", err)
	}
	if !mgr.branchExists("wt-new") {
		t.Error("branch wt-new was not created")
	}

	// A second add on the same branch fails, and the error names the step.
	err := mgr.addWorktree(filepath.Join(repo, ".pi-go", "tasks", "dup"), "wt-new", true)
	if err == nil {
		t.Fatal("expected an error re-adding an attached branch")
	}
	if !strings.Contains(err.Error(), "git worktree add") {
		t.Errorf("error = %v, want it to name the add step", err)
	}
}

func TestAddWorktree_AttachesExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	if out, err := exec.Command("git", "-C", repo, "branch", "detached-br").CombinedOutput(); err != nil {
		t.Fatalf("creating branch: %v: %s", err, out)
	}

	wt := filepath.Join(repo, ".pi-go", "tasks", "attach")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	// branchExists=true must attach without -b, which would fail on an
	// existing branch.
	if err := mgr.addWorktree(wt, "detached-br", true); err != nil {
		t.Fatalf("addWorktree(attach): %v", err)
	}
	if got := mgr.worktreeForBranch("detached-br"); !samePath(got, wt) {
		t.Errorf("branch attached at %q, want %q", got, wt)
	}
}

// TestCreate_RecordsNothingWhenWorktreeAddFails is the safety-critical case:
// the stash push succeeded, then `git worktree add` failed. Create must not
// record the agent, and must not leave the entry orphaned in the stash list.
//
// This test used to be TestCreate_DropsStashWhenWorktreeAddFails and pinned the
// opposite outcome — it asserted that dirty.txt did NOT come back, because the
// failure path ran `git stash drop`, deleting the entry WITHOUT applying it and
// destroying the uncommitted work the stash was protecting. That was left in
// place deliberately (the refactor it shipped with was behavior-preserving) with
// a note telling whoever fixed it to flip the assertions to "dirty.txt is
// restored". That fix has landed, so the assertion below is flipped.
//
// The restore itself is covered in depth by worktree_stash_test.go; what this
// test still uniquely pins is Create's bookkeeping after a failure — no active
// entry, no path recorded.
func TestCreate_RecordsNothingWhenWorktreeAddFails(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Make the tree dirty so the stash push actually creates an entry.
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Force `git worktree add -b` to fail after the stash has already been
	// taken: a branch name ending in ".lock" survives sanitizeWorktreeName but
	// is rejected by git's ref-format rules.
	const requested = "blocked.lock"
	if _, branch := worktreeNames("blocked-1234567890", requested); branch != requested {
		t.Fatalf("sanitized branch = %q, want %q; the test no longer forces an add failure", branch, requested)
	}

	_, err := mgr.Create("blocked-1234567890", requested)
	if err == nil {
		t.Fatal("expected Create to fail when worktree add cannot run")
	}
	if !strings.Contains(err.Error(), "git worktree add") {
		t.Errorf("error = %v, want it to name the add step", err)
	}
	if n := stashCount(t, repo); n != 0 {
		t.Errorf("stash list has %d entries after a failed Create, want 0", n)
	}
	// The stash was applied, not dropped: the user's uncommitted file is back
	// in the working tree. See the doc comment.
	if _, statErr := os.Stat(filepath.Join(repo, "dirty.txt")); statErr != nil {
		t.Errorf("dirty.txt was not restored (stat err = %v); the failed "+
			"worktree add destroyed the user's uncommitted work", statErr)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active() = %d after a failed Create, want 0", mgr.Active())
	}
	if got := mgr.PathFor("blocked-1234567890"); got != "" {
		t.Errorf("PathFor = %q after a failed Create, want empty", got)
	}
}

// TestCreate_StashSurvivesSuccessfulAdd is the other half of the pairing: on
// success the entry stays in the list, tagged with the message MergeBack pops.
func TestCreate_StashSurvivesSuccessfulAdd(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Create("keep-1234567890"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupAll() })

	if n := stashCount(t, repo); n != 1 {
		t.Fatalf("stash list has %d entries, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(repo, "dirty.txt")); !os.IsNotExist(err) {
		t.Errorf("uncommitted file still present after stash: %v", err)
	}
}

// ---------------------------------------------------------------------------
// spawner.go
// ---------------------------------------------------------------------------

func TestChildExitError(t *testing.T) {
	cfg := TimeoutConfig{Absolute: 2 * time.Second, Inactivity: time.Second}
	waitErr := errors.New("exit status 1")

	tests := []struct {
		name        string
		timedOut    bool
		ctxErr      error
		waitErr     error
		stderr      string
		wantNil     bool
		wantSubstrs []string
		wantTimeout bool
	}{
		{
			name: "clean exit", wantNil: true,
		},
		{
			name: "idle timeout wins over everything",
			// Even with a deadline and a wait error present, the idle case is
			// first — a limit kill is also a signal kill.
			timedOut: true, ctxErr: context.DeadlineExceeded, waitErr: waitErr, stderr: "boom",
			wantSubstrs: []string{"produced no output for 1s", timeoutHint},
			wantTimeout: true,
		},
		{
			name:   "absolute deadline",
			ctxErr: context.DeadlineExceeded, waitErr: waitErr, stderr: "boom",
			wantSubstrs: []string{"exceeded its 2s time limit", timeoutHint},
			wantTimeout: true,
		},
		{
			name:    "wait error with stderr",
			waitErr: waitErr, stderr: "panic: nope",
			wantSubstrs: []string{"pi process failed", "exit status 1", "panic: nope"},
		},
		{
			name:        "wait error without stderr",
			waitErr:     waitErr,
			wantSubstrs: []string{"pi process failed", "exit status 1"},
		},
		{
			name: "stderr alone is not a failure", stderr: "just chatter", wantNil: true,
		},
		{
			name: "canceled context that is not a deadline", ctxErr: context.Canceled, wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := childExitError(cfg, tt.timedOut, tt.ctxErr, tt.waitErr, tt.stderr)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("childExitError = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("childExitError = nil, want an error")
			}
			for _, want := range tt.wantSubstrs {
				if !strings.Contains(got.Error(), want) {
					t.Errorf("error %q missing %q", got, want)
				}
			}
			if errors.Is(got, ErrSubagentTimeout) != tt.wantTimeout {
				t.Errorf("errors.Is(err, ErrSubagentTimeout) = %v, want %v",
					errors.Is(got, ErrSubagentTimeout), tt.wantTimeout)
			}
			if !tt.wantTimeout && tt.waitErr != nil && !errors.Is(got, tt.waitErr) {
				t.Errorf("wait error not wrapped in %v", got)
			}
		})
	}
}

func TestEmitChildLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantEvent  Event
		wantResult string
	}{
		{
			name: "non-JSON becomes text",
			line: "plain output",
			// The raw line is emitted but NOT accumulated into the result.
			wantEvent: Event{Type: "text_delta", Content: "plain output"},
		},
		{
			name:       "text_delta accumulates",
			line:       `{"type":"text_delta","delta":"hi"}`,
			wantEvent:  Event{Type: "text_delta", Content: "hi"},
			wantResult: "hi",
		},
		{
			name:      "tool_call carries name and args",
			line:      `{"type":"tool_call","tool_name":"read","tool_input":{"path":"x"}}`,
			wantEvent: Event{Type: "tool_call", Content: "read"},
		},
		{
			name:      "tool_result",
			line:      `{"type":"tool_result","content":"ok"}`,
			wantEvent: Event{Type: "tool_result", Content: "ok"},
		},
		{
			name:      "message_start carries session id",
			line:      `{"type":"message_start","session_id":"s1"}`,
			wantEvent: Event{Type: "message_start", SessionID: "s1"},
		},
		{
			name:      "message_end drops payload",
			line:      `{"type":"message_end","content":"ignored"}`,
			wantEvent: Event{Type: "message_end"},
		},
		{
			name:      "unknown type concatenates delta and content",
			line:      `{"type":"thinking","delta":"a","content":"b"}`,
			wantEvent: Event{Type: "thinking", Content: "ab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Process{events: make(chan Event, 4)}
			var result strings.Builder
			p.emitChildLine(tt.line, &result)

			select {
			case got := <-p.events:
				if got.Type != tt.wantEvent.Type || got.Content != tt.wantEvent.Content ||
					got.SessionID != tt.wantEvent.SessionID {
					t.Errorf("event = %+v, want %+v", got, tt.wantEvent)
				}
			default:
				t.Fatal("no event emitted")
			}
			if result.String() != tt.wantResult {
				t.Errorf("result = %q, want %q", result.String(), tt.wantResult)
			}
		})
	}
}

func TestEmitChildLine_ToolArgsPassedThrough(t *testing.T) {
	p := &Process{events: make(chan Event, 1)}
	var result strings.Builder
	p.emitChildLine(`{"type":"tool_call","tool_name":"read","tool_input":{"path":"x"}}`, &result)

	ev := <-p.events
	args, ok := ev.ToolArgs.(map[string]any)
	if !ok {
		t.Fatalf("ToolArgs = %#v, want a map", ev.ToolArgs)
	}
	if args["path"] != "x" {
		t.Errorf("ToolArgs[path] = %v, want x", args["path"])
	}
}

func TestReadChildLines_DrainsUntilClosed(t *testing.T) {
	p := &Process{events: make(chan Event, 8)}
	lines := make(chan string, 4)
	lines <- `{"type":"text_delta","delta":"a"}`
	lines <- "" // blank lines are skipped, not emitted
	lines <- `{"type":"text_delta","delta":"b"}`
	close(lines)

	idle := NewInactivityTimer(10 * time.Second)
	defer idle.Stop()

	result, timedOut := p.readChildLines(lines, idle, func() { t.Error("cancel called on a clean drain") })
	if timedOut {
		t.Error("timedOut = true on a clean drain")
	}
	if result != "ab" {
		t.Errorf("result = %q, want %q", result, "ab")
	}
	if len(p.events) != 2 {
		t.Errorf("emitted %d events, want 2 (blank line must be skipped)", len(p.events))
	}
}

func TestReadChildLines_IdleTimeoutCancelsAndDrains(t *testing.T) {
	p := &Process{events: make(chan Event, 8)}
	lines := make(chan string, 2)
	lines <- `{"type":"text_delta","delta":"partial"}`

	idle := NewInactivityTimer(20 * time.Millisecond)
	defer idle.Stop()

	canceled := false
	// Feed one more line after the timer fires, so the post-cancel drain loop
	// has something to consume before the producer closes.
	go func() {
		time.Sleep(60 * time.Millisecond)
		lines <- "late"
		close(lines)
	}()

	result, timedOut := p.readChildLines(lines, idle, func() { canceled = true })
	if !timedOut {
		t.Fatal("timedOut = false, want true")
	}
	if !canceled {
		t.Error("cancel was not called on idle timeout")
	}
	// Text seen before the timeout is kept — the partial answer is not thrown away.
	if result != "partial" {
		t.Errorf("result = %q, want %q", result, "partial")
	}
}

func TestStartLineScanner(t *testing.T) {
	lines := startLineScanner(strings.NewReader("one\ntwo\nthree\n"))

	var got []string
	for l := range lines {
		got = append(got, l)
	}
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStartStderrCollector(t *testing.T) {
	c := startStderrCollector(strings.NewReader("first\nsecond\n"))
	<-c.done
	if got := c.Text(); got != "first\nsecond" {
		t.Errorf("Text() = %q, want %q", got, "first\nsecond")
	}
}

// TestStartStderrCollector_Bounded pins the cap: a chatty child must not turn
// the diagnostic buffer into the new place the memory goes.
func TestStartStderrCollector_Bounded(t *testing.T) {
	line := strings.Repeat("x", 1024) + "\n"
	c := startStderrCollector(strings.NewReader(strings.Repeat(line, 200))) // ~200KB
	<-c.done

	got := len(c.Text())
	if got == 0 {
		t.Fatal("captured nothing")
	}
	// The check is "buffer under the cap before appending", so the last line
	// can push it one line past maxStderrCapture, but no further.
	if got > maxStderrCapture+len(line) {
		t.Errorf("captured %d bytes, want <= %d", got, maxStderrCapture+len(line))
	}
}

func TestBuildCommand(t *testing.T) {
	s := &Spawner{PiBinary: "/bin/echo"}
	dir := t.TempDir()

	cmd := s.buildCommand(t.Context(), SpawnOpts{
		Prompt:  "do it",
		Model:   "m1",
		WorkDir: dir,
		Env:     []string{"EXTRA_ONE=1"},
	})

	if cmd.Path != "/bin/echo" {
		t.Errorf("Path = %q", cmd.Path)
	}
	if cmd.Dir != dir {
		t.Errorf("Dir = %q, want %q", cmd.Dir, dir)
	}
	if cmd.WaitDelay != 3*time.Second {
		t.Errorf("WaitDelay = %v, want 3s", cmd.WaitDelay)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--model m1") || !strings.HasSuffix(joined, "do it") {
		t.Errorf("Args = %v", cmd.Args)
	}
	var found bool
	for _, e := range cmd.Env {
		if e == "EXTRA_ONE=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("opts.Env not merged into cmd.Env: %v", cmd.Env)
	}
}

func TestBuildCommand_NoExtraEnvOrWorkDir(t *testing.T) {
	s := &Spawner{PiBinary: "/bin/echo"}
	cmd := s.buildCommand(t.Context(), SpawnOpts{Prompt: "p"})

	if cmd.Dir != "" {
		t.Errorf("Dir = %q, want empty", cmd.Dir)
	}
	if len(cmd.Env) == 0 {
		t.Error("Env is empty; the filtered base env should still be present")
	}
}

func TestStartChildProcess(t *testing.T) {
	// /bin/echo does not exist on Windows; the runners do carry a POSIX shell.
	sh := testenv.RequireShell(t)
	cmd := exec.Command(sh, "-c", "echo hello")
	stdout, stderr, err := startChildProcess(cmd)
	if err != nil {
		t.Fatalf("startChildProcess: %v", err)
	}
	if stdout == nil || stderr == nil {
		t.Fatal("nil pipe returned on success")
	}
	if _, err := stdout.Read(make([]byte, 1)); err != nil {
		t.Errorf("reading stdout: %v", err)
	}
	_ = cmd.Wait()
}

func TestStartChildProcess_StartFailure(t *testing.T) {
	cmd := exec.Command(filepath.Join(t.TempDir(), "no-such-binary"))
	_, _, err := startChildProcess(cmd)
	if err == nil {
		t.Fatal("expected an error starting a missing binary")
	}
	if !strings.Contains(err.Error(), "starting pi process") {
		t.Errorf("error = %v, want it to name the start step", err)
	}
}

// ---------------------------------------------------------------------------
// spawner_acp.go
// ---------------------------------------------------------------------------

func TestACPProcEvent(t *testing.T) {
	tests := []struct {
		name string
		in   sharedacp.Event
		want Event
	}{
		{
			"message",
			sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "hi", SessionID: "s"},
			Event{Type: "text_delta", Content: "hi", SessionID: "s"},
		},
		{
			"progress",
			sharedacp.Event{Type: sharedacp.EventTypeProgress, Content: "p", SessionID: "s"},
			Event{Type: "tool_call", Content: "p", SessionID: "s"},
		},
		{
			"tool",
			sharedacp.Event{Type: sharedacp.EventTypeTool, Content: "t", SessionID: "s"},
			Event{Type: "tool_call", Content: "t", SessionID: "s"},
		},
		{
			"stderr",
			sharedacp.Event{Type: sharedacp.EventTypeStderr, Content: "e", SessionID: "s"},
			Event{Type: "stderr", Content: "e", SessionID: "s"},
		},
		{
			"error carries Error, not Content",
			sharedacp.Event{Type: sharedacp.EventTypeError, Error: "bad", Content: "dropped", SessionID: "s"},
			Event{Type: "error", Error: "bad", SessionID: "s"},
		},
		{
			"unknown type passes through",
			sharedacp.Event{Type: "custom", Content: "c", SessionID: "s"},
			Event{Type: "custom", Content: "c", SessionID: "s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acpProcEvent(tt.in); got != tt.want {
				t.Errorf("acpProcEvent(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCompleteGracefully(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strict sentinel", "all done " + acpCompletionSentinel, "all done"},
		{"loose sentinel", "all done " + acpCompletionMatcher, "all done"},
		{"both forms", acpCompletionSentinel + " x " + acpCompletionMatcher, "x"},
		{"no sentinel", "  plain  ", "plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := completeGracefully(sharedacp.RunResult{
				Status: sharedacp.StatusError,
				Error:  "signal: killed",
				Result: tt.in,
			})
			if got.Status != sharedacp.StatusSuccess {
				t.Errorf("Status = %v, want success", got.Status)
			}
			if got.Error != "" {
				t.Errorf("Error = %q, want empty", got.Error)
			}
			if got.Result != tt.want {
				t.Errorf("Result = %q, want %q", got.Result, tt.want)
			}
		})
	}
}

func TestACPErrorText(t *testing.T) {
	tests := []struct {
		name   string
		result sharedacp.RunResult
		want   string
	}{
		{"error only", sharedacp.RunResult{Error: " boom "}, "boom"},
		{"stderr only", sharedacp.RunResult{Stderr: " oops "}, "stderr: oops"},
		{"both", sharedacp.RunResult{Error: "boom", Stderr: "oops"}, "boom\nstderr: oops"},
		{"neither", sharedacp.RunResult{}, "subprocess failed"},
		{"whitespace-only stderr is not appended", sharedacp.RunResult{Error: "boom", Stderr: "   "}, "boom"},
		{"whitespace-only everything", sharedacp.RunResult{Error: "  ", Stderr: "  "}, "subprocess failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acpErrorText(tt.result); got != tt.want {
				t.Errorf("acpErrorText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPumpACPEvents_SendsStartOnceAndDetectsSentinel(t *testing.T) {
	sess := newFakeACPSession()
	proc := &Process{events: make(chan Event, 32)}

	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "working", SessionID: "s1"}
	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "done " + acpCompletionSentinel, SessionID: "s1"}
	// Events after the sentinel must not re-trigger completion handling.
	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "trailing", SessionID: "s1"}
	close(sess.events)

	sentStart, graceful := pumpACPEvents(sess, proc)
	if !sentStart {
		t.Error("sentStart = false, want true")
	}
	if !graceful {
		t.Error("gracefulCompletion = false, want true")
	}

	close(proc.events)
	var types []string
	for ev := range proc.events {
		types = append(types, ev.Type)
	}
	want := []string{"message_start", "text_delta", "text_delta", "text_delta"}
	if len(types) != len(want) {
		t.Fatalf("events = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, types[i], want[i])
		}
	}
}

func TestPumpACPEvents_NoSessionIDMeansNoStart(t *testing.T) {
	sess := newFakeACPSession()
	proc := &Process{events: make(chan Event, 8)}

	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeStderr, Content: "warn"}
	close(sess.events)

	sentStart, graceful := pumpACPEvents(sess, proc)
	if sentStart {
		t.Error("sentStart = true with no session id")
	}
	if graceful {
		t.Error("gracefulCompletion = true without a sentinel")
	}
	if len(proc.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(proc.events))
	}
	if ev := <-proc.events; ev.Type != "stderr" {
		t.Errorf("event type = %q, want stderr", ev.Type)
	}
}

// TestPumpACPEvents_SentinelSplitAcrossEvents pins the accumulate-then-match
// behavior: the sentinel is detected across chunk boundaries.
func TestPumpACPEvents_SentinelSplitAcrossEvents(t *testing.T) {
	sess := newFakeACPSession()
	proc := &Process{events: make(chan Event, 16)}

	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "<Task ", SessionID: "s"}
	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "Completed>", SessionID: "s"}
	close(sess.events)

	if _, graceful := pumpACPEvents(sess, proc); !graceful {
		t.Error("gracefulCompletion = false for a sentinel split across events")
	}
}

// TestPumpACPEvents_NonMessageEventsDoNotAccumulate proves the accumulator only
// sees message text: a tool event carrying the sentinel must not end the run.
func TestPumpACPEvents_NonMessageEventsDoNotAccumulate(t *testing.T) {
	sess := newFakeACPSession()
	proc := &Process{events: make(chan Event, 8)}

	sess.events <- sharedacp.Event{Type: sharedacp.EventTypeTool, Content: acpCompletionSentinel, SessionID: "s"}
	close(sess.events)

	if _, graceful := pumpACPEvents(sess, proc); graceful {
		t.Error("gracefulCompletion = true for a sentinel in a tool event")
	}
}

// ---------------------------------------------------------------------------
// orchestrator.go
// ---------------------------------------------------------------------------

func TestTerminalStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil is completed", nil, "completed"},
		{"timeout sentinel", fmt.Errorf("wrapped: %w", ErrSubagentTimeout), "timeout"},
		// A timeout also arrives as a signal kill; the sentinel must win.
		{"timeout wrapping a signal kill", fmt.Errorf("signal: killed: %w", ErrSubagentTimeout), "timeout"},
		{"signal kill", errors.New("signal: killed"), "killed"},
		{"other failure", errors.New("exit status 2"), "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalStatus(tt.err); got != tt.want {
				t.Errorf("terminalStatus(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestSpawnEnv(t *testing.T) {
	t.Run("no worktree manager leaves env untouched", func(t *testing.T) {
		o := &Orchestrator{}
		in := []string{"A=1"}
		got := o.spawnEnv(in, "/some/dir")
		if len(got) != 1 || got[0] != "A=1" {
			t.Errorf("env = %v, want [A=1]", got)
		}
	})

	t.Run("adds sandbox and worktree roots", func(t *testing.T) {
		o := &Orchestrator{worktree: NewWorktreeManager("/repo")}
		in := []string{"A=1"}
		got := o.spawnEnv(in, "/repo/.pi-go/tasks/x")

		want := []string{"A=1", "PI_SANDBOX_ROOT=/repo", "PI_WORKTREE_ROOT=/repo/.pi-go/tasks/x"}
		if len(got) != len(want) {
			t.Fatalf("env = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
			}
		}
		// The caller's slice must not be mutated — it is copied first.
		if len(in) != 1 {
			t.Errorf("caller env was mutated: %v", in)
		}
	})

	t.Run("empty workDir omits the worktree root", func(t *testing.T) {
		o := &Orchestrator{worktree: NewWorktreeManager("/repo")}
		got := o.spawnEnv(nil, "")
		if len(got) != 1 || got[0] != "PI_SANDBOX_ROOT=/repo" {
			t.Errorf("env = %v, want [PI_SANDBOX_ROOT=/repo]", got)
		}
	})
}

func TestResolveWorkDir(t *testing.T) {
	t.Run("explicit WorkDir wins", func(t *testing.T) {
		o := &Orchestrator{repoRoot: "/repo", worktree: NewWorktreeManager("/repo")}
		got, err := o.resolveWorkDir("a1", SpawnInput{WorkDir: "/explicit"}, true)
		if err != nil || got != "/explicit" {
			t.Errorf("got (%q, %v), want (/explicit, nil)", got, err)
		}
	})

	t.Run("no worktree falls back to repo root", func(t *testing.T) {
		o := &Orchestrator{repoRoot: "/repo"}
		got, err := o.resolveWorkDir("a1", SpawnInput{}, true)
		if err != nil || got != "/repo" {
			t.Errorf("got (%q, %v), want (/repo, nil)", got, err)
		}
	})

	t.Run("worktree not requested falls back to repo root", func(t *testing.T) {
		o := &Orchestrator{repoRoot: "/repo", worktree: NewWorktreeManager("/repo")}
		got, err := o.resolveWorkDir("a1", SpawnInput{}, false)
		if err != nil || got != "/repo" {
			t.Errorf("got (%q, %v), want (/repo, nil)", got, err)
		}
	})

	t.Run("creates the worktree", func(t *testing.T) {
		repo := initTestRepo(t)
		mgr := NewWorktreeManager(repo)
		o := &Orchestrator{repoRoot: repo, worktree: mgr}
		t.Cleanup(func() { _ = mgr.CleanupAll() })

		got, err := o.resolveWorkDir("wd-1234567890", SpawnInput{WorktreeName: "named"}, true)
		if err != nil {
			t.Fatalf("resolveWorkDir: %v", err)
		}
		want := filepath.Join(repo, ".pi-go", "tasks", "named")
		if got != want {
			t.Errorf("workDir = %q, want %q", got, want)
		}
	})

	t.Run("worktree failure is wrapped", func(t *testing.T) {
		// A manager rooted outside a git repo cannot create a worktree.
		o := &Orchestrator{repoRoot: "/repo", worktree: NewWorktreeManager(t.TempDir())}
		_, err := o.resolveWorkDir("wd-1234567890", SpawnInput{}, true)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "creating worktree") {
			t.Errorf("error = %v, want it to name the worktree step", err)
		}
	})
}

func TestTrackAgent(t *testing.T) {
	o := NewOrchestrator(&config.Config{}, "", nil)
	state := &agentState{ID: "a1", Type: "explore", Status: "running"}

	if !o.trackAgent(state) {
		t.Fatal("trackAgent = false on an open orchestrator")
	}
	if len(o.List()) != 1 {
		t.Errorf("List() = %d entries, want 1", len(o.List()))
	}

	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()

	if o.trackAgent(&agentState{ID: "a2", Status: "running"}) {
		t.Error("trackAgent = true after shutdown")
	}
	if len(o.List()) != 1 {
		t.Errorf("List() = %d entries after a rejected track, want 1", len(o.List()))
	}
}

func TestAbandonSpawn(t *testing.T) {
	t.Run("releases the pool slot without a worktree", func(t *testing.T) {
		o := NewOrchestrator(&config.Config{}, "", nil)
		if err := o.pool.Acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		before := o.pool.Available()
		o.abandonSpawn("a1", false)
		if got := o.pool.Available(); got != before+1 {
			t.Errorf("Available() = %d, want %d", got, before+1)
		}
	})

	t.Run("removes the worktree it created", func(t *testing.T) {
		repo := initTestRepo(t)
		o := NewOrchestrator(&config.Config{}, repo, nil)
		if err := o.pool.Acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := o.worktree.Create("ab-1234567890"); err != nil {
			t.Fatalf("Create: %v", err)
		}

		o.abandonSpawn("ab-1234567890", true)

		if o.worktree.Active() != 0 {
			t.Errorf("Active() = %d after abandonSpawn, want 0", o.worktree.Active())
		}
		if got := o.pool.Available(); got != o.pool.Size() {
			t.Errorf("Available() = %d, want %d", got, o.pool.Size())
		}
	})
}

func TestDispatchSpawn_DefaultRoutesToSpawner(t *testing.T) {
	o := NewOrchestrator(&config.Config{}, "", nil)
	// An empty prompt is rejected by (*Spawner).Spawn and by nothing else, so
	// this error proves the default arm was taken.
	_, err := o.dispatchSpawn(t.Context(), SpawnOpts{}, "explore")
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("err = %v, want 'prompt is required' from the pi spawner", err)
	}
}

func TestDispatchSpawn_ACPRoutesToACPDispatcher(t *testing.T) {
	sess := newFakeACPSession()
	withFakeACPRunner(t, sess)
	o := NewOrchestrator(&config.Config{}, "", nil)

	proc, err := o.dispatchSpawn(t.Context(), SpawnOpts{Prompt: "go"}, "claude")
	if err != nil {
		t.Fatalf("dispatchSpawn: %v", err)
	}
	// An ACP-dispatched Process has no *exec.Cmd; the pi spawner always sets one.
	if proc.cmd != nil {
		t.Error("expected an ACP Process (no exec.Cmd), got a pi child")
	}
	sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "ok"})
	if got, err := proc.Wait(); err != nil || got != "ok" {
		t.Errorf("Wait() = (%q, %v), want (ok, nil)", got, err)
	}
}

func TestForwardAgentEvents_PublishesRunDone(t *testing.T) {
	o := NewOrchestrator(&config.Config{}, "", nil)
	if err := o.pool.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}

	proc := &Process{events: make(chan Event, 4), done: make(chan struct{})}
	proc.events <- Event{Type: "text_delta", Content: "hi"}
	close(proc.events)
	close(proc.done) // Wait returns (result="", err=nil) → status "completed"

	state := &agentState{ID: "a1", Type: "explore", Status: "running", StartedAt: time.Now()}
	if !o.trackAgent(state) {
		t.Fatal("trackAgent failed")
	}

	out := make(chan Event, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.forwardAgentEvents(out, proc, state, false)
	}()

	var got []Event
	for ev := range out {
		got = append(got, ev)
	}
	<-done

	if len(got) != 2 {
		t.Fatalf("events = %+v, want 2", got)
	}
	if got[0].Type != "text_delta" {
		t.Errorf("[0] = %+v, want the forwarded text_delta", got[0])
	}
	if got[1].Type != "run_done" || got[1].Status != "completed" {
		t.Errorf("[1] = %+v, want run_done/completed", got[1])
	}
	if state.FinishedAt.IsZero() {
		t.Error("FinishedAt was not set")
	}
	if got := o.pool.Available(); got != o.pool.Size() {
		t.Errorf("pool slot not released: Available() = %d, want %d", got, o.pool.Size())
	}
}

// TestForwardAgentEvents_KeepsNonRunningStatus proves the status is only
// derived when the agent is still marked running — a canceled agent keeps the
// status the canceller set.
func TestForwardAgentEvents_KeepsNonRunningStatus(t *testing.T) {
	o := NewOrchestrator(&config.Config{}, "", nil)
	if err := o.pool.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}

	proc := &Process{events: make(chan Event, 1), done: make(chan struct{})}
	close(proc.events)
	proc.err = errors.New("signal: killed")
	close(proc.done)

	state := &agentState{ID: "a1", Type: "explore", Status: "canceled", StartedAt: time.Now()}
	if !o.trackAgent(state) {
		t.Fatal("trackAgent failed")
	}

	out := make(chan Event, 4)
	go o.forwardAgentEvents(out, proc, state, false)

	var last Event
	for ev := range out {
		last = ev
	}
	if last.Type != "run_done" || last.Status != "canceled" {
		t.Errorf("run_done status = %q, want canceled", last.Status)
	}
	if !state.FinishedAt.IsZero() {
		t.Error("FinishedAt was set for an already-terminal agent")
	}
}
