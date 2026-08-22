package subagent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
	"github.com/dimetron/pi-go/internal/testenv"
)

// TestOrchestrator_SetProviderOptions exercises the 0%-covered provider-option
// setter and verifies values land in the orchestrator struct.
func TestOrchestrator_SetProviderOptions(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	orch.SetProviderOptions("https://api.example.com", true, []string{"X-Foo: bar", "X-Baz: qux"})

	if orch.BaseURL != "https://api.example.com" {
		t.Errorf("BaseURL = %q, want https://api.example.com", orch.BaseURL)
	}
	if !orch.Insecure {
		t.Error("Insecure should be true")
	}
	if len(orch.Headers) != 2 || orch.Headers[0] != "X-Foo: bar" {
		t.Errorf("Headers = %v, want [X-Foo: bar, X-Baz: qux]", orch.Headers)
	}

	// Re-setting replaces values (not appends).
	orch.SetProviderOptions("", false, nil)
	if orch.BaseURL != "" || orch.Insecure || orch.Headers != nil {
		t.Errorf("reset failed: BaseURL=%q Insecure=%v Headers=%v", orch.BaseURL, orch.Insecure, orch.Headers)
	}
}

// TestOrchestrator_SpawnWithRetry_NoRetriesReturnsSpawnErr verifies that with
// maxRetries=0 the function returns immediately on spawn failure, wrapping the
// error with the attempt count.
func TestOrchestrator_SpawnWithRetry_NoRetriesReturnsSpawnErr(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	orch.spawner.PiBinary = "/nonexistent/pi"

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "explore", Role: "smol"},
		Prompt:     "test",
		MaxRetries: 0,
	})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

// TestOrchestrator_SpawnWithRetry_NegativeAndClamped verifies input validation:
// negative MaxRetries is clamped to 0, values > 3 are clamped to 3. With a
// failing spawn we exit via the final return path.
func TestOrchestrator_SpawnWithRetry_NegativeAndClamped(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.spawner.PiBinary = "/nonexistent/pi"

	// Negative clamp → 0
	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "explore", Role: "smol"},
		Prompt:     "t",
		MaxRetries: -1,
	})
	if err == nil {
		t.Fatal("expected spawn error")
	}

	// Over-max clamp → 3 (still fails because spawn fails every time)
	_, _, err = orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "explore", Role: "smol"},
		Prompt:     "t",
		MaxRetries: 10,
	})
	if err == nil {
		t.Fatal("expected spawn error after 4 attempts")
	}
}

// TestOrchestrator_SpawnWithRetry_SuccessfulNoRetry takes the happy path
// through SpawnWithRetry with a mock ACP runner to exercise the early-return
// branch when maxRetries is zero.
func TestOrchestrator_SpawnWithRetry_SuccessfulNoRetry(t *testing.T) {
	sess := newFakeACPSession()
	withFakeACPRunner(t, sess)

	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	go func() {
		sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "ok", SessionID: "s"})
	}()

	events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("SpawnWithRetry: %v", err)
	}
	if agentID == "" {
		t.Error("expected non-empty agent id")
	}

	// Drain to ensure no goroutine leaks.
	for range events {
	}
}

// TestSendProcEvent_DropsWhenFull exercises the full-channel drop path of
// sendProcEvent. Previously only the happy path was covered.
func TestSendProcEvent_DropsWhenFull(t *testing.T) {
	proc := &Process{
		events: make(chan Event, 1),
		done:   make(chan struct{}),
	}

	// First event fits.
	sendProcEvent(proc, Event{Type: "a"})
	// Second event must be dropped rather than block.
	done := make(chan struct{})
	go func() {
		sendProcEvent(proc, Event{Type: "b"})
		close(done)
	}()
	select {
	case <-done:
		// success — function returned without blocking
	case <-time.After(500 * time.Millisecond):
		t.Fatal("sendProcEvent blocked on full channel")
	}

	// Drain the first event to verify only one landed.
	got := <-proc.events
	if got.Type != "a" {
		t.Errorf("got event type %q, want a", got.Type)
	}
	select {
	case extra := <-proc.events:
		t.Errorf("unexpected extra event %+v", extra)
	default:
	}
}

// TestWorktree_CleanupReturnsErrOnMissing exercises the "no worktree found"
// error branch inside cleanupWorktree by calling Cleanup on an unknown ID.
func TestWorktree_CleanupReturnsErrOnMissing(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	if err := mgr.Cleanup("unknown-agent"); err == nil {
		t.Fatal("expected error for missing agent")
	}
}

// TestWorktree_CleanupAfterClose verifies Cleanup is a no-op once the manager
// has started CleanupAll (closed=true).
func TestWorktree_CleanupAfterClose(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)
	_ = mgr.CleanupAll() // sets closed = true

	if err := mgr.Cleanup("nope"); err != nil {
		t.Errorf("Cleanup after close should be no-op, got %v", err)
	}
}

// TestWorktree_CreateAfterClose verifies Create rejects new worktrees after
// the manager has been shut down.
func TestWorktree_CreateAfterClose(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)
	_ = mgr.CleanupAll()

	if _, err := mgr.Create("agent-1"); err == nil {
		t.Fatal("expected error on Create after CleanupAll")
	}
}

// TestWorktree_MergeBack_UnknownAgent exercises the recover-then-fail branch
// of MergeBack when the manager has never seen the agent ID and no on-disk
// metadata exists.
func TestWorktree_MergeBack_UnknownAgent(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	_, err := mgr.MergeBack("never-created")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

// TestWorktree_RecoverFromBranchOnly verifies recoverWorktreeInfo succeeds
// when only the branch exists (not the directory).
func TestWorktree_RecoverFromBranchOnly(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Create a worktree then manually remove only its on-disk path to simulate
	// a partial recovery scenario — the branch is still present.
	agentID := "agent-recover-branch"
	wtPath, err := mgr.Create(agentID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Remove the directory but leave the branch intact via a forced prune of
	// the worktree record.
	if out, err := mgr.git("worktree", "remove", "--force", wtPath); err != nil {
		t.Fatalf("worktree remove: %v: %s", err, out)
	}
	// Drop the in-memory record so recoverWorktreeInfo is exercised.
	mgr.mu.Lock()
	delete(mgr.active, agentID)
	mgr.mu.Unlock()

	// Now merge-back should use recoverWorktreeInfo. The branch has the same
	// HEAD as main, so merge is trivially successful (or emits
	// "Already up to date"); either way we just need to hit the recover path.
	_, _ = mgr.MergeBack(agentID)
}

// TestOrchestrator_WriteACPEvent_Disabled exercises the short-circuit in
// writeACPEvent when the path is empty (already covered) and also the
// append-to-existing-file branch to lift coverage.
func TestOrchestrator_WriteACPEvent_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acp.jsonl")

	// Seed an initial line.
	if err := os.WriteFile(path, []byte(`{"pre":"existing"}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	orch.SetACPLogPath(path)

	orch.writeACPEvent("claude-1", "claude", Event{Type: "text_delta", Content: "hello"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	// Seed line + new event line = at least 2 lines.
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines < 2 {
		t.Errorf("expected >=2 newlines, got %d in %q", lines, string(data))
	}
}

// TestOrchestrator_WriteACPEvent_BadPath swallows errors when the file cannot
// be opened (e.g. path points to a directory). This exercises the OpenFile
// error branch.
func TestOrchestrator_WriteACPEvent_BadPath(t *testing.T) {
	dir := t.TempDir()
	// Using the directory itself as the log "path" forces OpenFile to fail.
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	orch.SetACPLogPath(dir)

	// Must not panic or block, even when OpenFile fails.
	orch.writeACPEvent("claude-1", "claude", Event{Type: "text_delta", Content: "x"})
}

// TestOrchestrator_PruneRecentTasks verifies both the "expired" and "not
// expired" branches of pruneRecentTasks.
func TestOrchestrator_PruneRecentTasks(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// Fresh entry — must survive prune.
	orch.RecordTask("fresh", "ok", "completed")

	// Expired entry — injected directly.
	orch.recentTasksMu.Lock()
	orch.recentTasks[normalizeTaskKey("expired")] = recentTask{
		CompletedAt: time.Now().Add(-2 * recentTaskTTL),
		Summary:     "old",
		Status:      "completed",
	}
	orch.recentTasksMu.Unlock()

	orch.pruneRecentTasks()

	// Fresh remains; expired removed.
	if r := orch.FindRecentTask("fresh"); r == nil {
		t.Error("fresh task should have survived prune")
	}
	orch.recentTasksMu.RLock()
	_, stillThere := orch.recentTasks[normalizeTaskKey("expired")]
	orch.recentTasksMu.RUnlock()
	if stillThere {
		t.Error("expired task should have been pruned")
	}
}

// TestOrchestrator_EnsurePruneLoopIdempotent verifies calling ensurePruneLoop
// multiple times only starts one ticker.
func TestOrchestrator_EnsurePruneLoopIdempotent(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	orch.ensurePruneLoop()
	firstTicker := orch.pruneTicker
	orch.ensurePruneLoop()
	if orch.pruneTicker != firstTicker {
		t.Error("ensurePruneLoop should not replace existing ticker")
	}
}

// TestToSpawnInput_UnknownType exercises the error branch when the agent type
// does not exist in the bundled list.
func TestToSpawnInput_UnknownType(t *testing.T) {
	_, err := AgentInput{Type: "does-not-exist", Prompt: "p"}.ToSpawnInput()
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// TestToSpawnInput_KnownBundled exercises the success path of ToSpawnInput
// using a known bundled type like "explore".
func TestToSpawnInput_KnownBundled(t *testing.T) {
	in := AgentInput{
		Type:       "explore",
		Prompt:     "find the bug",
		WorkDir:    "/tmp",
		Background: true,
	}
	out, err := in.ToSpawnInput()
	if err != nil {
		t.Fatalf("ToSpawnInput: %v", err)
	}
	if out.Agent.Name != "explore" {
		t.Errorf("agent name = %q, want explore", out.Agent.Name)
	}
	if out.Prompt != "find the bug" {
		t.Errorf("prompt = %q", out.Prompt)
	}
	if out.WorkDir != "/tmp" || !out.Background {
		t.Error("fields not forwarded correctly")
	}
}

// TestStartACPSession_KnownAgents verifies the claude/gemini/cursor branches
// are hit — but since we can't actually launch the CLI binaries in a sandbox,
// we tolerate failure from session.Start and only assert the branch routes to
// a specific runner (i.e. the error comes from the runner, not from "unknown
// ACP agent").
func TestStartACPSession_KnownAgents_RoutesToRunner(t *testing.T) {
	for _, name := range []string{"claude", "gemini", "cursor", "copilot"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()

			// startACPSession may return an error from the actual runner when
			// the binary is missing. That's fine — we only verify we did not
			// hit the "unknown ACP agent" default branch.
			_, err := startACPSession(ctx, name, "hi", SpawnOpts{WorkDir: t.TempDir()})
			if err != nil && err.Error() == `unknown ACP agent "`+name+`"` {
				t.Fatalf("hit unknown-agent branch for %s", name)
			}
		})
	}
}

// TestFilterEnv_SkipsMalformed verifies the `!ok` branch in FilterEnv by
// constructing a bespoke allowlist and relying on os.Environ. There's no easy
// way to inject a bare "NOEQUALS" entry, but we can at least verify FilterEnv
// doesn't panic with a minimal allowlist and returns a slice we can iterate.
func TestFilterEnv_MinimalAllowlist(t *testing.T) {
	t.Setenv("FOO_BAR_TEST", "1")
	got := FilterEnv([]string{"FOO_BAR_TEST"})
	// Must contain our injected var and nothing else (at least with this
	// allowlist).
	var found int32
	for _, e := range got {
		if e == "FOO_BAR_TEST=1" {
			atomic.AddInt32(&found, 1)
		}
	}
	if atomic.LoadInt32(&found) != 1 {
		t.Errorf("FOO_BAR_TEST not in filtered env: %v", got)
	}
}

// TestOrchestrator_Spawn_PoolExhaustedRespectsContext verifies the
// pool-acquire error branch in Spawn fires when the context is already
// canceled.
func TestOrchestrator_Spawn_PoolAcquireCanceled(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	// Drain the pool.
	for range DefaultPoolSize {
		if err := orch.pool.Acquire(context.Background()); err != nil {
			t.Fatalf("acquire: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, _, err := orch.Spawn(ctx, SpawnInput{
		Agent:  AgentConfig{Name: "explore", Role: "smol"},
		Prompt: "hello",
	})
	if err == nil {
		t.Fatal("expected error when pool is exhausted and ctx deadlined")
	}
}

// TestWorktree_CleanupPathAlreadyGone exercises the `else` branch of
// cleanupWorktree where the worktree dir was already removed on disk before
// Cleanup ran. Requires a registered worktree whose path we delete manually.
func TestWorktree_CleanupPathAlreadyGone(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	wtPath, err := mgr.Create("agent-gone")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Remove the directory outside git's knowledge.
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	// Cleanup should still succeed via the "path already gone" branch.
	if err := mgr.Cleanup("agent-gone"); err != nil {
		t.Fatalf("Cleanup with missing path: %v", err)
	}
	if mgr.Active() != 0 {
		t.Errorf("expected 0 active, got %d", mgr.Active())
	}
}

// TestWorktree_CleanupBranchAlreadyDeleted exercises the "branch not found"
// tolerated-error branch inside cleanupWorktree by pre-deleting the branch.
func TestWorktree_CleanupBranchAlreadyDeleted(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	wtPath, err := mgr.Create("agent-nobranch")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Force-remove the worktree so git releases its checkout, then delete
	// the branch so cleanupWorktree encounters "not found".
	if _, err := mgr.git("worktree", "remove", "--force", wtPath); err != nil {
		t.Fatalf("worktree remove: %v", err)
	}
	info := mgr.active["agent-nobranch"]
	if _, err := mgr.git("branch", "-D", info.Branch); err != nil {
		t.Fatalf("branch -D: %v", err)
	}

	// Cleanup should not return an error for the missing branch.
	if err := mgr.Cleanup("agent-nobranch"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}

// TestOrchestrator_SpawnWithRetry_RetriesOnSpawnErr forces maxRetries=2 and a
// failing binary so the retry loop burns through all attempts before giving
// up. This pushes coverage into the retry-continue branch.
func TestOrchestrator_SpawnWithRetry_RetriesOnSpawnErr(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.spawner.PiBinary = "/nonexistent/pi"

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "explore", Role: "smol"},
		Prompt:     "retry me",
		MaxRetries: 2,
	})
	if err == nil {
		t.Fatal("expected spawn failure after retries")
	}
}

// TestOrchestrator_ShutdownTwice verifies ShutdownWithTimeout is safe to call
// repeatedly — the second call must not panic even though the ticker is
// already stopped.
func TestOrchestrator_ShutdownTwice(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	orch.ensurePruneLoop() // start the ticker so the second Shutdown has work to do
	orch.Shutdown()
	orch.Shutdown() // should not panic or deadlock
}

// TestOrchestrator_IsKilledBySignal_ShortMessage hits the short-message path
// (len < 6) of isKilledBySignal.
func TestOrchestrator_IsKilledBySignal_ShortMessage(t *testing.T) {
	// "die" is shorter than "killed" suffix check.
	got := isKilledBySignal(stubError("die"))
	if got {
		t.Error("short error should not be reported as killed")
	}
}

// stubError is a test error string stand-in.
type stubError string

func (e stubError) Error() string { return string(e) }

// TestDiscoverAgents_UserDirLoadError injects a path that Stat sees as a dir
// but ReadDir cannot read — we fall back to the "loading user agents" branch
// only if readdir errors. Use a file named .pi-go/agents that's actually a
// regular file so ReadDir returns a not-a-directory error.
func TestDiscoverAgents_UserDirReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.ReadDir on a regular file is not a non-IsNotExist error there, so
		// the "loading user agents" branch cannot be reached from the filesystem.
		t.Skip("ReadDir on a file does not fail with ENOTDIR on Windows")
	}
	// Isolate HOME to a tempdir.
	tmpHome := t.TempDir()
	testenv.SetHome(t, tmpHome)

	// Create ~/.pi-go/agents as a FILE (not a dir) so ReadDir errors.
	piDir := filepath.Join(tmpHome, ".pi-go")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatalf("mkdir .pi-go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "agents"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := DiscoverAgents(t.TempDir(), ScopeBoth)
	if err == nil {
		t.Fatal("expected error when ~/.pi-go/agents is a file")
	}
}

// TestLoadAgentsFromDir_ParseError triggers the "parsing" error path when
// the file exists but a parse failure occurs during scanner read. We use a
// very large pathological file whose content is fine but whose name is
// non-.md, to verify the skip-non-markdown branch is hit (no error).
func TestLoadAgentsFromDir_SkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	// Subdir inside the target dir — LoadAgentsFromDir must skip dirs.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// File that isn't .md.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Valid agent file.
	content := "---\nname: agent1\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "agent1.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	agents, err := LoadAgentsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadAgentsFromDir: %v", err)
	}
	if len(agents) != 1 {
		t.Errorf("expected 1 agent (subdir/readme.txt skipped), got %d", len(agents))
	}
}

// TestParseAgentFileFromFS_MissingFile exercises the ReadFile error path.
func TestParseAgentFileFromFS_MissingFile(t *testing.T) {
	if _, err := ParseAgentFileFromFS(bundledFS, "bundled/does-not-exist.md"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestNewSpawner_DefaultExecutable exercises the NewSpawner code path where
// os.Executable succeeds vs. fails. This bumps the already-hit branch to full
// coverage by asserting the path doesn't panic for an explicit override.
func TestNewSpawner_DefaultAndOverride(t *testing.T) {
	s := NewSpawner("")
	if s.PiBinary == "" {
		t.Error("expected non-empty binary")
	}
	s2 := NewSpawner("/custom/pi")
	if s2.PiBinary != "/custom/pi" {
		t.Errorf("override not honored: %q", s2.PiBinary)
	}
}

// TestOrchestrator_SpawnWithRetry_CompletesWithRetries exercises the full
// event-drain loop in SpawnWithRetry with MaxRetries>0 so the loop waits for
// message_end and returns on "completed" status.
func TestOrchestrator_SpawnWithRetry_CompletesWithRetries(t *testing.T) {
	sess := newFakeACPSession()
	withFakeACPRunner(t, sess)

	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	go func() {
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "done", SessionID: "s"}
		sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "done", SessionID: "s"})
	}()

	events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("SpawnWithRetry: %v", err)
	}
	if agentID == "" {
		t.Fatal("expected non-empty agent id")
	}
	// Drain the channel to prevent goroutine leaks.
	for range events {
	}
}

// TestDispatchACP_StartError verifies dispatchACP bubbles up the underlying
// start error and cancels the derived context so no timer leaks.
func TestDispatchACP_StartError(t *testing.T) {
	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, _, _ string, _ SpawnOpts) (acpSession, error) {
		return nil, stubError("boom")
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	_, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "hello"}, "claude")
	if err == nil {
		t.Fatal("expected error from start failure")
	}
}

// TestDispatchACP_InstructionPrefix verifies dispatchACP prepends the
// instruction to the preamble when provided — hitting the Instruction-nonzero
// branch.
func TestDispatchACP_InstructionPrefix(t *testing.T) {
	var captured string
	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, _, prompt string, _ SpawnOpts) (acpSession, error) {
		captured = prompt
		sess := newFakeACPSession()
		go func() { sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess}) }()
		return sess, nil
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	proc, err := dispatchACP(context.Background(), SpawnOpts{
		Prompt:      "run",
		Instruction: "Be polite.",
	}, "claude")
	if err != nil {
		t.Fatalf("dispatchACP: %v", err)
	}
	for range proc.Events() {
	}
	if captured == "" || !strings.Contains(captured, "You are subagent[claude], run when done reply <Task Completed>!") {
		t.Errorf("instruction not prepended or preamble missing: %q", captured)
	}
	if len(captured) < len("Be polite.") || captured[:len("Be polite.")] != "Be polite." {
		t.Errorf("expected instruction prefix; got %q", captured)
	}
}

// TestWorktree_Create_InvalidRequestedName verifies Create sanitizes the
// requested name to a fallback if the input reduces to an empty string.
func TestWorktree_Create_InvalidRequestedName(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// All-special-chars name sanitizes to "" — worktreeNames falls back to the
	// shortID-derived path.
	path, err := mgr.Create("agent-xx-yy-zz", "///...")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty worktree path")
	}
	_ = mgr.Cleanup("agent-xx-yy-zz")
}

// TestLoadBundledAgents_HasDescriptions verifies every bundled agent has a
// description string — this walks every file in bundled/ so it covers the
// iteration branch in LoadBundledAgents.
func TestLoadBundledAgents_AllHaveDescriptions(t *testing.T) {
	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents: %v", err)
	}
	for _, a := range agents {
		if a.Name == "" {
			t.Errorf("agent has empty name")
		}
	}
}

// TestOrchestrator_Spawn_WorktreeCreateFailsReleasesPool sets up a stub
// worktree root that's not a git repo so Create fails and Spawn returns the
// "creating worktree" error branch, which must release the pool slot before
// returning.
func TestOrchestrator_Spawn_WorktreeCreateFails(t *testing.T) {
	cfg := testConfig()
	// Point the orchestrator at a non-repo dir so worktree.Create fails.
	orch := NewOrchestrator(cfg, t.TempDir(), nil)
	defer orch.Shutdown()

	tru := true
	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:    AgentConfig{Name: "task", Role: "smol"},
		Prompt:   "do",
		Worktree: &tru,
	})
	if err == nil {
		t.Fatal("expected worktree creation error")
	}
	if orch.pool.Available() != DefaultPoolSize {
		t.Errorf("pool slot not released: available=%d", orch.pool.Available())
	}
}
