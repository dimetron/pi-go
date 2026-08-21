package cli

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/lsp"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
)

// ---------------------------------------------------------------------------
// trySend
// ---------------------------------------------------------------------------

func TestTrySend(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		capacity int
		prefill  int
		wantLen  int
	}{
		{name: "empty buffer takes the value", capacity: 1, prefill: 0, wantLen: 1},
		{name: "room left takes the value", capacity: 4, prefill: 2, wantLen: 3},
		{name: "full buffer drops the value", capacity: 2, prefill: 2, wantLen: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := make(chan string, tc.capacity)
			for i := range tc.prefill {
				ch <- string(rune('a' + i))
			}

			// A full channel must drop rather than block: this call returning at
			// all is the assertion that matters.
			trySend(ch, "new")

			if got := len(ch); got != tc.wantLen {
				t.Fatalf("len(ch) = %d, want %d", got, tc.wantLen)
			}
		})
	}
}

func TestTrySendDeliversTheValue(t *testing.T) {
	t.Parallel()

	ch := make(chan int, 1)
	trySend(ch, 42)
	if got := <-ch; got != 42 {
		t.Fatalf("received %d, want 42", got)
	}
}

// ---------------------------------------------------------------------------
// initCoreTools
// ---------------------------------------------------------------------------

func TestInitCoreTools(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var res initResources
	defer res.cleanup()

	coreTools, err := initCoreTools(tmp, "", &res)
	if err != nil {
		t.Fatalf("initCoreTools() error = %v", err)
	}
	if len(coreTools) == 0 {
		t.Error("initCoreTools() returned no tools")
	}
	// Both resources have to be recorded for cleanup before anything can fail
	// later in init, otherwise a mid-init error leaks them.
	if res.sandbox == nil {
		t.Error("res.sandbox not recorded")
	}
	if res.bashSup == nil {
		t.Error("res.bashSup not recorded")
	}

	names := make(map[string]bool, len(coreTools))
	for _, tool := range coreTools {
		names[tool.Name()] = true
	}
	for _, want := range []string{"bash", "read"} {
		if !names[want] {
			t.Errorf("core tool %q missing; got %v", want, names)
		}
	}
}

// ---------------------------------------------------------------------------
// discoverSubsystems
// ---------------------------------------------------------------------------

func TestDiscoverSubsystems(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cases := []struct {
		name        string
		cfg         config.Config
		wantStarted []string
		notStarted  []string
	}{
		{
			name:        "no MCP servers skips the MCP step entirely",
			cfg:         config.Config{},
			wantStarted: []string{"git", "lsp", "skills"},
			notStarted:  []string{"mcp"},
		},
		{
			name: "a configured MCP server reports progress",
			cfg: config.Config{MCP: &config.MCPConfig{
				Servers: []config.MCPServer{{Name: "none", Command: "/nonexistent-mcp-binary"}},
			}},
			wantStarted: []string{"git", "lsp", "skills", "mcp"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			started := map[string]bool{}
			send := initProgress(func(item string, done bool) {
				mu.Lock()
				defer mu.Unlock()
				if !done {
					started[item] = true
				}
			})

			ps := discoverSubsystems(t.Context(), tc.cfg, tmp, send)
			if ps == nil {
				t.Fatal("discoverSubsystems() = nil")
			}
			// discoverSubsystems only returns once every job has finished, so
			// reading the map here needs no further synchronization.
			mu.Lock()
			defer mu.Unlock()
			for _, item := range tc.wantStarted {
				if !started[item] {
					t.Errorf("step %q never reported progress; got %v", item, started)
				}
			}
			for _, item := range tc.notStarted {
				if started[item] {
					t.Errorf("step %q reported progress but should have been skipped", item)
				}
			}
			// The manager is kept even when no language server is installed —
			// the diagnostics plumbing costs nothing idle.
			if ps.lspMgr == nil {
				t.Error("lspMgr is nil; the manager is meant to be kept unconditionally")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// deferredMemoryTools
// ---------------------------------------------------------------------------

func TestDeferredMemoryTools(t *testing.T) {
	t.Parallel()

	t.Run("disabled yields nothing to clean up", func(t *testing.T) {
		t.Parallel()
		memTools, store, recorder := deferredMemoryTools(config.Config{}, "/tmp", false)
		if memTools != nil || store != nil || recorder != nil {
			t.Fatalf("deferredMemoryTools(disabled) = (%v, %v, %v), want all nil", memTools, store, recorder)
		}
	})

	t.Run("enabled yields tools backed by the lazy store", func(t *testing.T) {
		t.Parallel()
		memTools, store, recorder := deferredMemoryTools(config.Config{}, "/tmp", true)
		if len(memTools) == 0 {
			t.Error("no memory tools returned")
		}
		if store == nil {
			t.Fatal("lazy store is nil")
		}
		if recorder == nil {
			t.Fatal("recorder is nil")
		}
		if recorder.project != "/tmp" {
			t.Errorf("recorder.project = %q, want %q", recorder.project, "/tmp")
		}
	})
}

// ---------------------------------------------------------------------------
// deferredInstructionParts
// ---------------------------------------------------------------------------

func TestDeferredInstructionParts(t *testing.T) {
	orig := flagSystem
	t.Cleanup(func() { flagSystem = orig })

	flagSystem = "be terse"
	parts := deferredInstructionParts()
	if parts.Base != "be terse" {
		t.Errorf("--system override: Base = %q, want %q", parts.Base, "be terse")
	}

	flagSystem = ""
	parts = deferredInstructionParts()
	if parts.String() == "" {
		t.Error("default instruction is empty")
	}
}

// ---------------------------------------------------------------------------
// buildDeferredCallbacks
// ---------------------------------------------------------------------------

func TestBuildDeferredCallbacks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	sandbox, err := tools.NewSandbox(tmp, "")
	if err != nil {
		t.Fatalf("NewSandbox() error = %v", err)
	}
	defer func() { _ = sandbox.Close() }()

	base := buildDeferredCallbacks(config.Config{}, "openai", sandbox, nil, nil)
	if base.metrics == nil || base.deduper == nil {
		t.Fatal("compaction metrics and deduper must always be created")
	}
	if len(base.beforeModel) == 0 {
		t.Error("the read-image callback should always be installed")
	}

	cases := []struct {
		name        string
		lspMgr      *lsp.Manager
		memRecorder *deferredMemoryRecorder
		wantExtra   int
	}{
		{name: "no lsp and no memory", wantExtra: 0},
		{name: "lsp adds its diagnostics callback", lspMgr: lsp.NewManager(nil), wantExtra: 1},
		{name: "memory adds its recorder", memRecorder: newDeferredMemoryRecorder(config.Config{}, tmp), wantExtra: 1},
		{
			name:        "both add one each",
			lspMgr:      lsp.NewManager(nil),
			memRecorder: newDeferredMemoryRecorder(config.Config{}, tmp),
			wantExtra:   2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDeferredCallbacks(config.Config{}, "openai", sandbox, tc.lspMgr, tc.memRecorder)
			if want := len(base.afterTool) + tc.wantExtra; len(got.afterTool) != want {
				t.Errorf("len(afterTool) = %d, want %d", len(got.afterTool), want)
			}
			if len(got.beforeTool) != len(base.beforeTool) {
				t.Errorf("len(beforeTool) = %d, want %d — neither option touches the before chain",
					len(got.beforeTool), len(base.beforeTool))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// openSessionService
// ---------------------------------------------------------------------------

func TestOpenSessionService(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	path, svc, err := openSessionService()
	if err != nil {
		t.Fatalf("openSessionService() error = %v", err)
	}
	if svc == nil {
		t.Fatal("session service is nil")
	}
	if want := filepath.Join(tmp, ".pi-go", "sessions"); path != want {
		t.Errorf("sessions path = %q, want %q", path, want)
	}
}

// ---------------------------------------------------------------------------
// resolveDeferredSession
// ---------------------------------------------------------------------------

func TestResolveDeferredSessionResumed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	orig := flagSession
	t.Cleanup(func() { flagSession = orig })
	flagSession = "existing-session"

	svc, err := pisession.NewFileService(filepath.Join(tmp, "sessions"))
	if err != nil {
		t.Fatalf("NewFileService() error = %v", err)
	}

	// The agent is deliberately nil: a resumed session must never reach
	// ag.CreateSession, and a nil pointer is the loudest way to prove it.
	sess, err := resolveDeferredSession(t.Context(), nil, svc, &cliMockLLM{name: "m"}, "openai", "https://api.example")
	if err != nil {
		t.Fatalf("resolveDeferredSession() error = %v", err)
	}
	if sess.id != "existing-session" {
		t.Errorf("id = %q, want %q", sess.id, "existing-session")
	}
	if !sess.resumed {
		t.Error("resumed = false, want true")
	}
	if sess.title != "" {
		t.Errorf("title = %q, want empty — a resumed session keeps its own title", sess.title)
	}
}

// ---------------------------------------------------------------------------
// installAutoCompactHook
// ---------------------------------------------------------------------------

func TestInstallAutoCompactHookNoOpWhenDisabled(t *testing.T) {
	t.Parallel()

	// With no session service and no tracker, buildAutoCompactHook returns nil
	// and the agent must not be touched — passing a nil agent asserts that.
	installAutoCompactHook(nil, autoCompactDeps{})
}

// ---------------------------------------------------------------------------
// initResources.cleanup
// ---------------------------------------------------------------------------

func TestInitResourcesCleanupIsSafeWhenEmpty(t *testing.T) {
	t.Parallel()

	// Every field is optional: init can fail before any of them exist, and the
	// caller defers cleanup unconditionally.
	var res initResources
	res.cleanup()
}

// TestDeferredMemoryToolsNamesAreMemoryScoped guards the append in deferredInit:
// only the memory tool set may ride in on this return value, because the caller
// adds it to the agent's tools unconditionally.
func TestDeferredMemoryToolsNamesAreMemoryScoped(t *testing.T) {
	t.Parallel()

	memTools, _, _ := deferredMemoryTools(config.Config{}, "/tmp", true)
	for _, tool := range memTools {
		if !strings.HasPrefix(tool.Name(), "mem") {
			t.Errorf("tool %q does not look like a memory tool", tool.Name())
		}
	}
}
