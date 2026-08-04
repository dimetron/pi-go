package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
	"github.com/dimetron/pi-go/internal/webserver"
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
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "print"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCliJSONModeNoPromptExitsCleanly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "json"})

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
	root := detectGitRoot(context.Background(), "/")
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

// -----------------------------------------------------------------------
// ParsePairingCode
// -----------------------------------------------------------------------

func TestParsePairingCode_PlainCode(t *testing.T) {
	code, token, err := ParsePairingCode("123456")
	if err != nil {
		t.Fatalf("ParsePairingCode: %v", err)
	}
	if code != "123456" {
		t.Errorf("code = %q, want 123456", code)
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestParsePairingCode_CodeAndToken(t *testing.T) {
	code, token, err := ParsePairingCode("123456:mytoken")
	if err != nil {
		t.Fatalf("ParsePairingCode: %v", err)
	}
	if code != "123456" {
		t.Errorf("code = %q, want 123456", code)
	}
	if token != "mytoken" {
		t.Errorf("token = %q, want mytoken", token)
	}
}

func TestParsePairingCode_WithWhitespace(t *testing.T) {
	code, _, err := ParsePairingCode("  123456  ")
	if err != nil {
		t.Fatalf("ParsePairingCode: %v", err)
	}
	if code != "123456" {
		t.Errorf("code = %q, want 123456", code)
	}
}

// -----------------------------------------------------------------------
// GetServePairingManager
// -----------------------------------------------------------------------

func TestGetServePairingManager(t *testing.T) {
	server := webserver.NewServerV2(webserver.Config{
		PairingTimeout: 5 * time.Minute,
	})
	pm := GetServePairingManager(server)
	if pm == nil {
		t.Error("expected non-nil PairingManager")
	}
}

// -----------------------------------------------------------------------
// formatAge — all branches
// -----------------------------------------------------------------------

func TestFormatAge_JustNow(t *testing.T) {
	result := formatAge(time.Now())
	if result != "just now" {
		t.Errorf("formatAge(now) = %q, want 'just now'", result)
	}
}

func TestFormatAge_Minutes(t *testing.T) {
	result := formatAge(time.Now().Add(-5 * time.Minute))
	if result != "5m ago" {
		t.Errorf("formatAge(-5m) = %q, want '5m ago'", result)
	}
}

func TestFormatAge_Hours(t *testing.T) {
	result := formatAge(time.Now().Add(-3 * time.Hour))
	if result != "3h ago" {
		t.Errorf("formatAge(-3h) = %q, want '3h ago'", result)
	}
}

func TestFormatAge_Days(t *testing.T) {
	result := formatAge(time.Now().Add(-2 * 24 * time.Hour))
	if result != "2d ago" {
		t.Errorf("formatAge(-2d) = %q, want '2d ago'", result)
	}
}

func TestFormatAge_OlderThanWeek(t *testing.T) {
	result := formatAge(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC))
	if result == "" {
		t.Error("expected non-empty formatted date")
	}
	// Should be like "Jan 15"
	if result == "just now" || result == "0d ago" {
		t.Errorf("old date should format as date, got %q", result)
	}
}

// -----------------------------------------------------------------------
// printRecentJSON
// -----------------------------------------------------------------------

func TestPrintRecentJSON_Empty(t *testing.T) {
	_ = captureStdout(t, func() {
		err := printRecentJSON(nil, nil, 20)
		if err != nil {
			t.Fatalf("printRecentJSON: %v", err)
		}
	})
}

func TestPrintRecentJSON_WithData(t *testing.T) {
	obs := []*memory.Observation{
		{Title: "test obs", Type: memory.TypeBugfix, Text: "fixed a bug"},
	}
	sums := []*memory.SessionSummary{
		{Request: "test request", Completed: "did stuff"},
	}
	output := captureStdout(t, func() {
		err := printRecentJSON(obs, sums, 10)
		if err != nil {
			t.Fatalf("printRecentJSON: %v", err)
		}
	})
	if output == "" {
		t.Error("expected JSON output")
	}
}

// -----------------------------------------------------------------------
// findMemoryDB
// -----------------------------------------------------------------------

func TestFindMemoryDB_ProjectSpecific(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	os.WriteFile(dbPath, []byte("test"), 0o644)

	found, err := findMemoryDB(dir)
	if err != nil {
		t.Fatalf("findMemoryDB: %v", err)
	}
	if found != dbPath {
		t.Errorf("found = %q, want %q", found, dbPath)
	}
}

func TestFindMemoryDB_PalaceLegacy(t *testing.T) {
	dir := t.TempDir()
	piDir := filepath.Join(dir, ".pi-go")
	os.MkdirAll(piDir, 0o755)
	dbPath := filepath.Join(piDir, "palace.db")
	os.WriteFile(dbPath, []byte("test"), 0o644)

	found, err := findMemoryDB(dir)
	if err != nil {
		t.Fatalf("findMemoryDB: %v", err)
	}
	if found != dbPath {
		t.Errorf("found = %q, want %q", found, dbPath)
	}
}

func TestFindMemoryDB_NotFound(t *testing.T) {
	// Override HOME so the global fallback doesn't find a real DB.
	t.Setenv("HOME", t.TempDir())
	_, err := findMemoryDB(t.TempDir())
	if err == nil {
		t.Error("expected error when DB not found")
	}
}

func TestFindMemoryDB_EmptyProject(t *testing.T) {
	_, err := findMemoryDB("")
	// With empty project, it tries global path — likely not found in test
	if err == nil {
		t.Log("global memory DB found (unexpected but OK)")
	}
}

// -----------------------------------------------------------------------
// runMemoryMine — project files
// -----------------------------------------------------------------------

// skipWithoutEmbedder skips tests that need a real embedding backend.
//
// These are integration tests wearing unit-test clothing: they call the same
// code paths `pi memory mine` does, which embed for real. With the default
// config that means a local Ollama daemon, and without one they either fail
// outright ("cannot reach daemon at http://localhost:11434") or — worse — fall
// through to downloading a ~100 MB model, which blew past the 10-minute test
// timeout and took the whole package down with a panic rather than a failure.
//
// Probing is cheap: EmbedderAvailability is a loopback request with a 3-second
// deadline. So this skips in CI, where no daemon runs, and still exercises the
// code locally where one does — which is better than a build tag that would
// make these dead weight everywhere.
func skipWithoutEmbedder(t *testing.T) {
	t.Helper()
	cfg := palace.DefaultConfig()
	cfg.ModelPath = defaultPalaceModelPath()
	if err := palace.EmbedderAvailability(cfg); err != nil {
		t.Skipf("no embedding backend available: %v", err)
	}
}

func TestRunMemoryMine_ProjectFiles(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	// (hugot.DownloadModel → gomlx/go-huggingface hub.(*Repo).DownloadFiles).
	if raceEnabled {
		t.Skip("skipping: go-huggingface has race condition")
	}
	skipWithoutEmbedder(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() { println(\"hello world test content for chunk threshold minimum\") }"), 0o644)

	output := captureStdout(t, func() {
		err := runMemoryMine(dir, "testproject", false)
		if err != nil {
			t.Fatalf("runMemoryMine: %v", err)
		}
	})
	if output == "" {
		t.Error("expected mining output")
	}
}

func TestRunMemoryMine_Conversations(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	// (hugot.DownloadModel → gomlx/go-huggingface hub.(*Repo).DownloadFiles).
	if raceEnabled {
		t.Skip("skipping: go-huggingface has race condition")
	}
	skipWithoutEmbedder(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "chat.jsonl"),
		[]byte(`{"role":"user","content":"question"}
{"role":"assistant","content":"answer"}
`), 0o644)

	output := captureStdout(t, func() {
		err := runMemoryMine(dir, "testconv", true)
		if err != nil {
			t.Fatalf("runMemoryMine convos: %v", err)
		}
	})
	if output == "" {
		t.Error("expected mining output")
	}
}

// -----------------------------------------------------------------------
// runMemoryModelStatus
// -----------------------------------------------------------------------

func TestRunMemoryModelStatus_NoModel(t *testing.T) {
	dir := t.TempDir()

	output := captureStdout(t, func() {
		err := runMemoryModelStatus(dir)
		if err != nil {
			t.Fatalf("runMemoryModelStatus: %v", err)
		}
	})
	if output == "" {
		t.Error("expected status output")
	}
}

func TestRunMemoryModelStatus_WithModel(t *testing.T) {
	dir := t.TempDir()
	modelDir := filepath.Join(dir, "sentence-transformers_all-MiniLM-L6-v2")
	os.MkdirAll(modelDir, 0o755)
	os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("fake model data"), 0o644)

	output := captureStdout(t, func() {
		err := runMemoryModelStatus(dir)
		if err != nil {
			t.Fatalf("runMemoryModelStatus: %v", err)
		}
	})
	if output == "" {
		t.Error("expected status output")
	}
}

func TestRunMemoryModelStatus_DefaultPath(t *testing.T) {
	// With empty path, uses ~/.pi-go/models/
	output := captureStdout(t, func() {
		_ = runMemoryModelStatus("")
	})
	if output == "" {
		t.Error("expected status output")
	}
}

// -----------------------------------------------------------------------
// runMemoryStatus
// -----------------------------------------------------------------------

func TestRunMemoryStatus_NoDB(t *testing.T) {
	output := captureStdout(t, func() {
		err := runMemoryStatus(filepath.Join(t.TempDir(), "nonexistent.db"))
		if err != nil {
			t.Fatalf("runMemoryStatus: %v", err)
		}
	})
	if output == "" {
		t.Error("expected 'no palace' message")
	}
}

func TestRunMemoryStatus_WithDB(t *testing.T) {
	// AddDrawer embeds its content, so this needs a backend like the mine tests
	// do. This is the test that hung for 10 minutes and panicked the package.
	skipWithoutEmbedder(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	// Create a real palace with some data
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	p.AddDrawer(context.Background(), palace.DrawerInput{
		Wing: "test", Room: "api", Content: "test content",
	})
	p.KGAdd(context.Background(), palace.TripleInput{
		Subject: "Alice", Predicate: "works_on", Object: "api",
	})
	p.Close()

	output := captureStdout(t, func() {
		err := runMemoryStatus(dbPath)
		if err != nil {
			t.Fatalf("runMemoryStatus: %v", err)
		}
	})
	if output == "" {
		t.Error("expected status output")
	}
}

// -----------------------------------------------------------------------
// runMemoryRecent — JSON output
// -----------------------------------------------------------------------

func TestRunMemoryRecent_JSON(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")

	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	output := captureStdout(t, func() {
		err := runMemoryRecent(dir, 10, "", true)
		if err != nil {
			t.Fatalf("runMemoryRecent JSON: %v", err)
		}
	})
	if output == "" {
		t.Error("expected JSON output")
	}
}

// -----------------------------------------------------------------------
// runMemoryRecent — with summaries
// -----------------------------------------------------------------------

func TestRunMemoryRecent_WithSummaries(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")

	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	store := memory.NewSQLiteStore(db)
	ctx := context.Background()

	// Create session
	store.CreateSession(ctx, &memory.Session{
		SessionID: "s1", Project: dir,
		StartedAt: time.Now(), Status: "completed",
	})

	// Insert observations
	store.InsertObservation(ctx, &memory.Observation{
		SessionID: "s1", Project: dir,
		Title: "obs1", Type: memory.TypeFeature, Text: "feature text",
		CreatedAt: time.Now(),
	})

	// Insert summary
	store.UpsertSummary(ctx, &memory.SessionSummary{
		SessionID: "s1", Project: dir,
		Request: "test request", Completed: "did work",
		CreatedAt: time.Now(),
	})

	db.Close()

	output := captureStdout(t, func() {
		err := runMemoryRecent(dir, 10, "", false)
		if err != nil {
			t.Fatalf("runMemoryRecent: %v", err)
		}
	})
	if output == "" {
		t.Error("expected output with observations and summaries")
	}
}

// -----------------------------------------------------------------------
// runMemoryRecent — type filter with limit
// -----------------------------------------------------------------------

func TestRunMemoryRecent_TypeFilterWithLimit(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")

	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	store := memory.NewSQLiteStore(db)
	ctx := context.Background()

	store.CreateSession(ctx, &memory.Session{
		SessionID: "s1", Project: dir,
		StartedAt: time.Now(), Status: "completed",
	})

	// Insert multiple observations of same type
	for i := 0; i < 5; i++ {
		store.InsertObservation(ctx, &memory.Observation{
			SessionID: "s1", Project: dir,
			Title: "bugfix", Type: memory.TypeBugfix, Text: "fix",
			CreatedAt: time.Now().Add(time.Duration(-i) * time.Minute),
		})
	}

	db.Close()

	// Limit to 2
	output := captureStdout(t, func() {
		err := runMemoryRecent(dir, 2, "bugfix", false)
		if err != nil {
			t.Fatalf("runMemoryRecent: %v", err)
		}
	})
	if output == "" {
		t.Error("expected output")
	}
}

// -----------------------------------------------------------------------
// newServeCmd flag registration
// -----------------------------------------------------------------------

func TestNewServeCmd_Flags(t *testing.T) {
	cmd := newServeCmd()
	if cmd.Use != "serve" {
		t.Errorf("Use = %q, want serve", cmd.Use)
	}
	// Check flags exist
	for _, name := range []string{"addr", "project", "pairing-timeout", "model"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not found", name)
		}
	}
}

// -----------------------------------------------------------------------
// palaceConfigFromCLI
// -----------------------------------------------------------------------

func TestPalaceConfigFromCLI_NilPalace(t *testing.T) {
	// With nil Palace config, should use defaults
	cfg := palaceConfigFromCLI(&config.Config{})
	if cfg.DBPath == "" {
		t.Error("expected non-empty default DBPath")
	}
	if cfg.ModelPath == "" {
		t.Error("expected non-empty default ModelPath")
	}
}

func TestPalaceConfigFromCLI_WithConfig(t *testing.T) {
	cfg := palaceConfigFromCLI(&config.Config{
		Palace: &config.PalaceConfig{
			DBPath:    "/custom/palace.db",
			ModelPath: "/custom/model",
		},
	})
	if cfg.DBPath != "/custom/palace.db" {
		t.Errorf("DBPath = %q, want /custom/palace.db", cfg.DBPath)
	}
	if cfg.ModelPath != "/custom/model" {
		t.Errorf("ModelPath = %q, want /custom/model", cfg.ModelPath)
	}
}

// -----------------------------------------------------------------------
// newMemoryMineCmd flag registration
// -----------------------------------------------------------------------

func TestNewMemoryMineCmd_Flags(t *testing.T) {
	cmd := newMemoryMineCmd()
	for _, name := range []string{"wing", "convos"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not found on mine cmd", name)
		}
	}
}

// -----------------------------------------------------------------------
// newMemoryModelCmd structure
// -----------------------------------------------------------------------

func TestNewMemoryModelCmd_Subcommands(t *testing.T) {
	cmd := newMemoryModelCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	if !names["download"] {
		t.Error("missing 'download' subcommand")
	}
	if !names["status"] {
		t.Error("missing 'status' subcommand")
	}
}
