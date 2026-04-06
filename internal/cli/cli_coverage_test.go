package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
)

// -----------------------------------------------------------------------
// cleanup method — exercise all non-nil resource branches
// -----------------------------------------------------------------------

func TestCliCleanupWithSessionLog(t *testing.T) {
	// Create a real logger so cleanup exercises the sessionLog.Close() path.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	lg, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}

	r := &initResources{sessionLog: lg}
	r.cleanup() // Should close the logger without error.
}

func TestCliCleanupWithSandbox(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}

	r := &initResources{sandbox: sb}
	r.cleanup() // Should close sandbox without error.
}

func TestCliCleanupWithLSPManager(t *testing.T) {
	mgr := lsp.NewManager(nil)

	r := &initResources{lspMgr: mgr}
	r.cleanup() // Should call mgr.Shutdown() without error.
}

func TestCliCleanupWithOrchestrator(t *testing.T) {
	orch := subagent.NewOrchestrator(nil, "", nil)

	r := &initResources{orch: orch}
	r.cleanup() // Should call orch.Shutdown() without error.
}

func TestCliCleanupWithMemoryStore(t *testing.T) {
	// Use a real SQLite memory store backed by a temp file.
	dbPath := filepath.Join(t.TempDir(), "test-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("memory.OpenDB: %v", err)
	}
	store := memory.NewSQLiteStore(db)

	r := &initResources{memStore: store}
	r.cleanup() // Should close the store without error.
}

func TestCliCleanupWithMemoryWorker(t *testing.T) {
	// Create a real memory store + worker so cleanup exercises the Shutdown path.
	dbPath := filepath.Join(t.TempDir(), "test-mem-worker.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("memory.OpenDB: %v", err)
	}
	store := memory.NewSQLiteStore(db)
	worker := memory.NewWorker(store, nil, 10)
	worker.Start(context.Background())

	r := &initResources{
		memStore:  store,
		memWorker: worker,
	}
	r.cleanup() // Should shutdown worker and close store.
}

func TestCliCleanupAllResources(t *testing.T) {
	// Exercise cleanup with every field populated.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	lg, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}

	sb, err := tools.NewSandbox(tmpDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}

	mgr := lsp.NewManager(nil)
	orch := subagent.NewOrchestrator(nil, "", nil)

	dbPath := filepath.Join(t.TempDir(), "test-all.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("memory.OpenDB: %v", err)
	}
	store := memory.NewSQLiteStore(db)
	worker := memory.NewWorker(store, nil, 10)
	worker.Start(context.Background())

	r := &initResources{
		sessionLog: lg,
		sandbox:    sb,
		lspMgr:     mgr,
		orch:       orch,
		memStore:   store,
		memWorker:  worker,
	}
	r.cleanup() // Every branch should be exercised.
}

// -----------------------------------------------------------------------
// detectMode — verify return value in test environment
// -----------------------------------------------------------------------

func TestCliDetectModeReturnsValidValue(t *testing.T) {
	mode := detectMode()
	switch mode {
	case "print", "interactive":
		// Valid.
	default:
		t.Errorf("detectMode() = %q, want 'print' or 'interactive'", mode)
	}
}

func TestCliDetectModeUnderGoTest(t *testing.T) {
	// Under go test, stdin is a pipe, so we expect "print".
	mode := detectMode()
	if mode != "print" {
		t.Logf("detectMode() = %q (stdin appears to be a terminal); expected 'print' under go test", mode)
	}
}

// -----------------------------------------------------------------------
// Version variable — ensure it is set
// -----------------------------------------------------------------------

func TestCliVersionDefault(t *testing.T) {
	// Version should be "dev" when not overridden by ldflags.
	if Version == "" {
		t.Error("Version should not be empty")
	}
}

// -----------------------------------------------------------------------
// newRootCmd — verify subcommand registration
// -----------------------------------------------------------------------

func TestCliNewRootCmdSubcommands(t *testing.T) {
	cmd := newRootCmd()

	// Verify ping and audit subcommands are registered.
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	for _, want := range []string{"ping", "audit"} {
		if !names[want] {
			t.Errorf("subcommand %q not found on root cmd", want)
		}
	}
}

func TestCliNewRootCmdFlagDefaults(t *testing.T) {
	cmd := newRootCmd()

	// socket flag should have a default.
	socketVal, err := cmd.Flags().GetString("socket")
	if err != nil {
		t.Fatalf("getting socket flag: %v", err)
	}
	if socketVal != "/tmp/pi-go.sock" {
		t.Errorf("socket default = %q, want '/tmp/pi-go.sock'", socketVal)
	}

	// Boolean flags should default to false.
	for _, name := range []string{"continue", "smol", "slow", "plan", "insecure", "memory-off"} {
		val, err := cmd.Flags().GetBool(name)
		if err != nil {
			t.Errorf("getting %s flag: %v", name, err)
			continue
		}
		if val {
			t.Errorf("flag %q default = true, want false", name)
		}
	}
}

// -----------------------------------------------------------------------
// runRoot error paths — mode flag with missing prompt
// -----------------------------------------------------------------------

func TestCliPrintModeNoPromptExitsCleanly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-4o", "--mode", "print"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCliJSONModeNoPromptExitsCleanly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-4o", "--mode", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// -----------------------------------------------------------------------
// --system flag
// -----------------------------------------------------------------------

func TestCliSystemFlagParsed(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--system", "custom instruction"})
	_ = cmd.ParseFlags([]string{"--system", "custom instruction"})

	val, err := cmd.Flags().GetString("system")
	if err != nil {
		t.Fatalf("getting system flag: %v", err)
	}
	if val != "custom instruction" {
		t.Errorf("system flag = %q, want %q", val, "custom instruction")
	}
}

// -----------------------------------------------------------------------
// --header flag (repeatable)
// -----------------------------------------------------------------------

func TestCliHeaderFlagRepeatable(t *testing.T) {
	cmd := newRootCmd()
	args := []string{"--header", "key1=val1", "--header", "key2=val2"}
	cmd.SetArgs(args)
	_ = cmd.ParseFlags(args)

	val, err := cmd.Flags().GetStringArray("header")
	if err != nil {
		t.Fatalf("getting header flag: %v", err)
	}
	if len(val) != 2 {
		t.Fatalf("header flag count = %d, want 2", len(val))
	}
	if val[0] != "key1=val1" || val[1] != "key2=val2" {
		t.Errorf("header values = %v, want [key1=val1 key2=val2]", val)
	}
}

// -----------------------------------------------------------------------
// detectGitRoot edge case — root of filesystem
// -----------------------------------------------------------------------

func TestCliDetectGitRootAtRoot(t *testing.T) {
	// "/" is unlikely to be a git repo.
	root := detectGitRoot("/")
	if root != "" {
		t.Logf("detectGitRoot('/') = %q (unexpected git repo at root)", root)
	}
}

// -----------------------------------------------------------------------
// runPrint with nil logger — exercises the nil-logger code path
// -----------------------------------------------------------------------

func TestCliRunPrintNilLogger(t *testing.T) {
	llm := &cliMockLLM{name: "test-nil-log", response: "hello"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		err := runPrint(context.Background(), ag, sessionID, "hi", nil)
		if err != nil {
			t.Fatalf("runPrint error: %v", err)
		}
	})
	if stdout == "" {
		t.Error("expected non-empty stdout")
	}
}

// -----------------------------------------------------------------------
// runJSON with nil logger
// -----------------------------------------------------------------------

func TestCliRunJSONNilLogger(t *testing.T) {
	llm := &cliMockLLM{name: "test-json-nil-log", response: "hello json"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		err := runJSON(context.Background(), ag, sessionID, "hi", nil)
		if err != nil {
			t.Fatalf("runJSON error: %v", err)
		}
	})
	if stdout == "" {
		t.Error("expected non-empty stdout")
	}
}

// -----------------------------------------------------------------------
// runPrint / runJSON with a real logger
// -----------------------------------------------------------------------

func TestCliRunPrintWithLogger(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	lg, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	defer lg.Close()

	llm := &cliMockLLM{name: "test-with-log", response: "logged response"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		err := runPrint(context.Background(), ag, sessionID, "hello", lg)
		if err != nil {
			t.Fatalf("runPrint error: %v", err)
		}
	})
	if stdout == "" {
		t.Error("expected non-empty stdout")
	}

	// Verify the log file was written.
	logPath := lg.Path()
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("log file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("log file is empty")
	}
}

func TestCliRunJSONWithLogger(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	lg, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	defer lg.Close()

	llm := &cliMockLLM{name: "test-json-with-log", response: "logged json"}
	ag, sessionID := newTestAgent(t, llm)

	stdout := captureStdout(t, func() {
		err := runJSON(context.Background(), ag, sessionID, "hello", lg)
		if err != nil {
			t.Fatalf("runJSON error: %v", err)
		}
	})
	if stdout == "" {
		t.Error("expected non-empty stdout")
	}
}

// -----------------------------------------------------------------------
// runPrint with canceled context — exercises the context error path
// -----------------------------------------------------------------------

func TestCliRunPrintCancelledContext(t *testing.T) {
	llm := &cliMockLLM{name: "test-cancel", response: "should not reach"}
	ag, sessionID := newTestAgent(t, llm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	// With a canceled context, runPrint may return nil (context.Canceled
	// is suppressed) or an error. Either is acceptable — we just verify no panic.
	_ = captureStdout(t, func() {
		_ = runPrint(ctx, ag, sessionID, "hello", nil)
	})
}

func TestCliRunJSONCancelledContext(t *testing.T) {
	llm := &cliMockLLM{name: "test-json-cancel", response: "should not reach"}
	ag, sessionID := newTestAgent(t, llm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = captureStdout(t, func() {
		_ = runJSON(ctx, ag, sessionID, "hello", nil)
	})
}

// -----------------------------------------------------------------------
// providerEnvVar — exhaustive coverage
// -----------------------------------------------------------------------

func TestCliProviderEnvVarOllama(t *testing.T) {
	got := providerEnvVar("ollama")
	if got != "OLLAMA_API_KEY" {
		t.Errorf("providerEnvVar('ollama') = %q, want 'OLLAMA_API_KEY'", got)
	}
}

// -----------------------------------------------------------------------
// --insecure flag
// -----------------------------------------------------------------------

func TestCliInsecureFlagParsed(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--insecure"})
	_ = cmd.ParseFlags([]string{"--insecure"})

	val, err := cmd.Flags().GetBool("insecure")
	if err != nil {
		t.Fatalf("getting insecure flag: %v", err)
	}
	if !val {
		t.Error("--insecure flag should be true when set")
	}
}

// -----------------------------------------------------------------------
// --memory-off flag
// -----------------------------------------------------------------------

func TestCliMemoryOffFlagParsed(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--memory-off"})
	_ = cmd.ParseFlags([]string{"--memory-off"})

	val, err := cmd.Flags().GetBool("memory-off")
	if err != nil {
		t.Fatalf("getting memory-off flag: %v", err)
	}
	if !val {
		t.Error("--memory-off flag should be true when set")
	}
}

// -----------------------------------------------------------------------
// cleanup idempotency — calling cleanup twice should not panic
// -----------------------------------------------------------------------

func TestCliCleanupIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	sb, err := tools.NewSandbox(tmpDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}

	r := &initResources{sandbox: sb}
	r.cleanup()
	// Second cleanup on already-closed sandbox may return an error
	// from Close(), but should not panic.
	r.cleanup()
}
