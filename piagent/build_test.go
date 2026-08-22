package piagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/tools"
)

func TestResolveWorkDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir := t.TempDir()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty means the process directory", "", cwd},
		{"absolute is kept", dir, dir},
		{"relative is made absolute", ".", cwd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWorkDir(tt.in)
			if err != nil {
				t.Fatalf("resolveWorkDir(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("resolveWorkDir(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveSessionDir(t *testing.T) {
	home := isolate(t)

	got, err := resolveSessionDir("")
	if err != nil {
		t.Fatalf("resolveSessionDir(\"\"): %v", err)
	}
	if want := filepath.Join(home, ".pi-go", "sessions"); got != want {
		t.Errorf("resolveSessionDir(\"\") = %q, want %q", got, want)
	}

	explicit := t.TempDir()
	if got, err := resolveSessionDir(explicit); err != nil || got != explicit {
		t.Errorf("resolveSessionDir(%q) = %q, %v", explicit, got, err)
	}
}

func TestDetectGitRoot(t *testing.T) {
	plain := t.TempDir()
	if got := detectGitRoot(t.Context(), plain); got != "" {
		t.Errorf("detectGitRoot(non-repo) = %q, want empty", got)
	}

	repo := t.TempDir()
	gitInit(t, repo)
	got := detectGitRoot(t.Context(), repo)
	if got == "" {
		t.Fatal("detectGitRoot(repo) = empty, want the repository root")
	}
	// macOS resolves TempDir through /private, so compare the resolved paths.
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if gotResolved, _ := filepath.EvalSymlinks(got); gotResolved != want {
		t.Errorf("detectGitRoot = %q, want %q", got, want)
	}
}

func TestBuildSandbox(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	extra := t.TempDir()

	granted := filepath.Join(extra, "file.txt")
	if err := os.WriteFile(granted, []byte("visible"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	denied := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(denied, []byte("hidden"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sb, err := buildSandbox(workDir, []string{extra})
	if err != nil {
		t.Fatalf("buildSandbox: %v", err)
	}
	defer sb.Close()

	if _, err := sb.ReadFile(granted); err != nil {
		t.Errorf("extra directory not granted: %v", err)
	}
	if _, err := sb.ReadFile(denied); err == nil {
		t.Error("a file outside the sandbox was readable")
	}
}

func TestBuildSandboxRejectsAnUnusableExtraDir(t *testing.T) {
	isolate(t)
	// A regular file where a directory should be: the sandbox creates missing
	// extra directories, so this is the way to make one fail.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := buildSandbox(t.TempDir(), []string{blocked})
	if err == nil {
		t.Fatal("buildSandbox accepted an unusable extra directory")
	}
	if !strings.Contains(err.Error(), "adding sandbox dir") {
		t.Errorf("error = %v, want it to name the failing directory", err)
	}
}

func TestBuildSandboxRejectsAMissingRoot(t *testing.T) {
	isolate(t)
	if _, err := buildSandbox(filepath.Join(t.TempDir(), "absent"), nil); err == nil {
		t.Fatal("buildSandbox accepted a nonexistent root")
	}
}

func TestBuildToolsets(t *testing.T) {
	if got := buildToolsets(config.Config{}); len(got) != 0 {
		t.Errorf("buildToolsets(empty) = %d toolsets, want 0", len(got))
	}

	cfg := config.Config{
		A2A: &config.A2AConfig{Agents: []config.A2AAgentConfig{{Name: "peer", URL: "http://example.invalid"}}},
		MCP: &config.MCPConfig{Servers: []config.MCPServer{
			{Name: "stdio", Command: "mcp-server", Args: []string{"--stdio"}},
			{Name: "http", URL: "http://mcp.invalid", Headers: map[string]string{"X-Key": "v"}},
		}},
	}
	// MCP transports connect lazily, so nothing is spawned here.
	if got := buildToolsets(cfg); len(got) != 3 {
		t.Errorf("buildToolsets(mcp+a2a) = %d toolsets, want 3", len(got))
	}

	// llms.txt sources add the (cached) fetch_docs toolset; building it
	// touches no network and no disk.
	cfg.LLMS = &config.LLMSConfig{Sources: []config.LLMSSource{{Name: "adk", URL: "https://adk.dev/llms.txt"}}}
	got := buildToolsets(cfg)
	if len(got) != 4 {
		t.Fatalf("buildToolsets(mcp+a2a+llms) = %d toolsets, want 4", len(got))
	}
	if _, ok := got[3].(*tools.LLMSToolset); !ok || got[3].Name() != "llms" {
		t.Errorf("last toolset = %T %q, want *tools.LLMSToolset %q", got[3], got[3].Name(), "llms")
	}
}

func TestBuildInstruction(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	rules := "# Project Rule\n\nAlways use tabs.\n"
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte(rules), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	o := defaultOptions()
	o.instruction = "CUSTOM BASE PROMPT"
	o.extraPrompt = "EXTRA TEXT"
	got := buildInstruction(o, workDir)

	for _, want := range []string{"CUSTOM BASE PROMPT", "Always use tabs.", "EXTRA TEXT"} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction is missing %q", want)
		}
	}
}

func TestMemoryConfigResolution(t *testing.T) {
	home := isolate(t)

	empty := config.Config{}
	if got, want := memoryDBPath(empty), filepath.Join(home, ".pi-go", "memory", "claude-mem.db"); got != want {
		t.Errorf("memoryDBPath(empty) = %q, want %q", got, want)
	}
	if memoryTokenBudget(empty) != config.MemoryDefaults().TokenBudget {
		t.Error("memoryTokenBudget(empty) did not fall back to the default")
	}
	if maxPendingObservations(empty) != config.MemoryDefaults().MaxPending {
		t.Error("maxPendingObservations(empty) did not fall back to the default")
	}

	custom := config.Config{Memory: &config.MemoryConfig{
		DBPath:      "/tmp/custom.db",
		TokenBudget: 1234,
		MaxPending:  7,
	}}
	if got := memoryDBPath(custom); got != "/tmp/custom.db" {
		t.Errorf("memoryDBPath(custom) = %q", got)
	}
	if got := memoryTokenBudget(custom); got != 1234 {
		t.Errorf("memoryTokenBudget(custom) = %d, want 1234", got)
	}
	if got := maxPendingObservations(custom); got != 7 {
		t.Errorf("maxPendingObservations(custom) = %d, want 7", got)
	}
}

func TestSetupMemoryDisabled(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	o.memoryEnabled = false

	store, worker, closeFn := setupMemory(t.Context(), o, config.Config{}, nil)
	defer closeFn()
	if store != nil || worker != nil {
		t.Error("setupMemory returned a store or worker while disabled")
	}
	if got := memoryContext(t.Context(), nil, config.Config{}, "/project"); got != "" {
		t.Errorf("memoryContext(nil store) = %q, want empty", got)
	}
}

func TestSetupMemoryDegradesOnAnUnopenableDatabase(t *testing.T) {
	isolate(t)
	// A directory where the database file should be: OpenDB cannot use it.
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	o := defaultOptions()
	cfg := config.Config{Memory: &config.MemoryConfig{DBPath: dbPath}}

	store, worker, closeFn := setupMemory(t.Context(), o, cfg, nil)
	defer closeFn()
	if store != nil || worker != nil {
		t.Error("setupMemory returned a store for an unopenable database")
	}
}

func TestSetupMemoryOpensAndCloses(t *testing.T) {
	isolate(t)
	cfg := config.Config{Memory: &config.MemoryConfig{DBPath: filepath.Join(t.TempDir(), "mem.db")}}
	o := defaultOptions()

	store, worker, closeFn := setupMemory(t.Context(), o, cfg, nil)
	if store == nil || worker == nil {
		closeFn()
		t.Skip("memory store unavailable in this environment")
	}
	// An empty store recalls nothing, which is a legitimate empty context.
	if got := memoryContext(t.Context(), store, cfg, "/project"); got != "" && !strings.HasPrefix(got, "\n\n") {
		t.Errorf("memoryContext = %q, want it prefixed with a blank line", got)
	}
	closeFn()
}

func TestPalacePaths(t *testing.T) {
	home := isolate(t)

	dbPath, modelPath := palacePaths(config.Config{})
	if dbPath != filepath.Join(home, ".pi-go", "palace.db") {
		t.Errorf("default palace db = %q", dbPath)
	}
	if !strings.Contains(modelPath, "MiniLM") {
		t.Errorf("default model path = %q, want the bundled embedder", modelPath)
	}

	cfg := config.Config{Palace: &config.PalaceConfig{DBPath: "/tmp/p.db", ModelPath: "/tmp/m"}}
	if got, gotModel := palacePaths(cfg); got != "/tmp/p.db" || gotModel != "/tmp/m" {
		t.Errorf("palacePaths(custom) = %q, %q", got, gotModel)
	}
}

func TestSetupPalace(t *testing.T) {
	isolate(t)

	t.Run("disabled", func(t *testing.T) {
		o := defaultOptions()
		o.palaceEnabled = false
		palaceTools, context, closeFn := setupPalace(o, config.Config{}, nil)
		defer closeFn()
		if palaceTools != nil || context != "" {
			t.Error("setupPalace produced output while disabled")
		}
	})

	t.Run("no database on disk", func(t *testing.T) {
		cfg := config.Config{Palace: &config.PalaceConfig{DBPath: filepath.Join(t.TempDir(), "absent.db")}}
		palaceTools, context, closeFn := setupPalace(defaultOptions(), cfg, nil)
		defer closeFn()
		if palaceTools != nil || context != "" {
			t.Error("setupPalace produced output with no palace on disk")
		}
	})

	t.Run("unusable database degrades to no palace", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "corrupt.db")
		if err := os.WriteFile(dbPath, []byte("not a database"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg := config.Config{Palace: &config.PalaceConfig{DBPath: dbPath}}
		palaceTools, context, closeFn := setupPalace(defaultOptions(), cfg, nil)
		defer closeFn()
		if palaceTools != nil || context != "" {
			t.Error("setupPalace produced output from an unusable database")
		}
	})
}

// stubPalaceStore answers the two queries Status makes and nothing else.
type stubPalaceStore struct {
	palace.PalaceStore
	drawers int
	err     error
}

func (s stubPalaceStore) CountDrawers(context.Context) (int, error) { return s.drawers, s.err }
func (s stubPalaceStore) ListWings(context.Context) ([]palace.WingSummary, error) {
	return nil, nil
}
func (s stubPalaceStore) KGStats(context.Context) (*palace.KGStats, error) {
	return &palace.KGStats{}, nil
}

func TestPalaceHasContent(t *testing.T) {
	tests := []struct {
		name  string
		store stubPalaceStore
		want  bool
	}{
		{"empty palace is not worth advertising", stubPalaceStore{drawers: 0}, false},
		{"one drawer is enough", stubPalaceStore{drawers: 1}, true},
		{"a failed count counts as empty", stubPalaceStore{err: errTestCount}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := palace.NewWithStore(tt.store, nil)
			if got := palaceHasContent(p); got != tt.want {
				t.Errorf("palaceHasContent = %v, want %v", got, tt.want)
			}
		})
	}
}

var errTestCount = errors.New("count failed")

func TestSetupLSP(t *testing.T) {
	t.Run("off registers nothing", func(t *testing.T) {
		mgr, lspTools, err := setupLSP(LSPOff)
		if err != nil {
			t.Fatalf("setupLSP(off): %v", err)
		}
		defer mgr.Shutdown()
		if lspTools != nil {
			t.Errorf("setupLSP(off) registered %d tools, want none", len(lspTools))
		}
		if mgr == nil {
			t.Error("setupLSP(off) returned no manager; the after-tool hook needs one")
		}
	})

	for _, mode := range []LSPMode{LSPMin, LSPFull, LSPMode("nonsense")} {
		t.Run(string(mode), func(t *testing.T) {
			mgr, lspTools, err := setupLSP(mode)
			if err != nil {
				t.Fatalf("setupLSP(%q): %v", mode, err)
			}
			defer mgr.Shutdown()
			// Whether any tool registers depends on installed servers; the gate
			// is what matters, so only the invariant is asserted.
			if !mgr.AnyAvailable() && len(lspTools) != 0 {
				t.Errorf("setupLSP(%q) registered %d tools with no server available", mode, len(lspTools))
			}
		})
	}
}

func TestCompactorConfig(t *testing.T) {
	defaults := tools.DefaultCompactorConfig()
	if got := compactorConfig(config.Config{}); got != defaults {
		t.Errorf("compactorConfig(empty) = %+v, want the defaults", got)
	}

	off := false
	cfg := config.Config{Compactor: &config.CompactorConfig{
		Enabled:             &off,
		SourceCodeFiltering: "aggressive",
		MaxChars:            100,
		MaxLines:            10,
	}}
	got := compactorConfig(cfg)
	if got.Enabled || got.SourceCodeFiltering != "aggressive" || got.MaxChars != 100 || got.MaxLines != 10 {
		t.Errorf("compactorConfig(custom) = %+v", got)
	}

	// Zero values must not clobber defaults.
	if got := compactorConfig(config.Config{Compactor: &config.CompactorConfig{}}); got.MaxChars != defaults.MaxChars {
		t.Errorf("an unset MaxChars overwrote the default: %+v", got)
	}
}

func TestConvertHooks(t *testing.T) {
	if got := convertHooks(nil); len(got) != 0 {
		t.Errorf("convertHooks(nil) = %d hooks, want 0", len(got))
	}
	in := []config.HookConfig{{
		Event:   "PreToolUse",
		Command: "./check.sh",
		Tools:   []string{"bash"},
		Timeout: 30,
	}}
	got := convertHooks(in)
	if len(got) != 1 {
		t.Fatalf("convertHooks = %d hooks, want 1", len(got))
	}
	if got[0].Event != "PreToolUse" || got[0].Command != "./check.sh" || got[0].Timeout != 30 {
		t.Errorf("convertHooks = %+v", got[0])
	}
	if len(got[0].Tools) != 1 || got[0].Tools[0] != "bash" {
		t.Errorf("tool matcher lost: %+v", got[0].Tools)
	}
}

func TestBuildSubagentsTolerateBrokenDefinitions(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	agentsDir := filepath.Join(workDir, ".pi-go", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "broken.md"), []byte("not frontmatter"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.Config{}
	orch := buildSubagents(t.Context(), &cfg, workDir)
	if orch == nil {
		t.Fatal("buildSubagents returned nil for a directory with a broken definition")
	}
	orch.Shutdown()
}

func TestTimeoutsAreBounded(t *testing.T) {
	// These bound subprocesses and database calls made during construction; a
	// zero would mean "no timeout", which is what they exist to prevent.
	for name, d := range map[string]time.Duration{
		"gitCmdTimeout":         gitCmdTimeout,
		"palaceStatusTimeout":   palaceStatusTimeout,
		"memoryShutdownTimeout": memoryShutdownTimeout,
	} {
		if d <= 0 {
			t.Errorf("%s = %v, want a positive bound", name, d)
		}
	}
}
