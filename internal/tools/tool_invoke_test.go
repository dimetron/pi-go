package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	adkmemory "google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/lsp"
)

// mockToolCtx is a minimal agent.Context backed by a real context.Context.
// It lets tests invoke tools through the tool.Tool Run interface, exercising the
// functiontool factory closures that direct handler tests never reach. The full
// v2 method set is implemented so the mock stays forward-compatible with future
// agent.Context surface growth.
type mockToolCtx struct {
	context.Context
}

func (mockToolCtx) FunctionCallID() string         { return "" }
func (mockToolCtx) Actions() *session.EventActions { return &session.EventActions{} }
func (mockToolCtx) SearchMemory(context.Context, string) (*adkmemory.SearchResponse, error) {
	return nil, nil
}
func (mockToolCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
func (mockToolCtx) RequestConfirmation(string, any) error                { return nil }
func (mockToolCtx) AgentName() string                                    { return "" }
func (mockToolCtx) ReadonlyState() session.ReadonlyState                 { return nil }
func (mockToolCtx) State() session.State                                 { return nil }
func (mockToolCtx) Artifacts() agent.Artifacts                           { return nil }
func (mockToolCtx) InvocationID() string                                 { return "" }
func (mockToolCtx) UserContent() *genai.Content                          { return nil }
func (mockToolCtx) AppName() string                                      { return "" }
func (mockToolCtx) Branch() string                                       { return "" }
func (mockToolCtx) SessionID() string                                    { return "" }
func (mockToolCtx) UserID() string                                       { return "" }

// InvocationContext surface.
func (mockToolCtx) Agent() agent.Agent          { return nil }
func (mockToolCtx) Memory() agent.Memory        { return nil }
func (mockToolCtx) Session() session.Session    { return nil }
func (mockToolCtx) RunConfig() *agent.RunConfig { return nil }
func (mockToolCtx) EndInvocation()              {}
func (mockToolCtx) Ended() bool                 { return false }
func (mockToolCtx) WithContext(ctx context.Context) agent.InvocationContext {
	return mockToolCtx{Context: ctx}
}
func (mockToolCtx) IsolationScope() string { return "" }
func (mockToolCtx) ResumedInput(string) (any, bool) {
	return nil, false
}
func (mockToolCtx) WithICDelta(*agent.InvocationContextDelta) agent.InvocationContext {
	return mockToolCtx{}
}

// agent.Context-only surface (v2 additions).
func (mockToolCtx) Path() string                            { return "" }
func (mockToolCtx) RunID() string                           { return "" }
func (mockToolCtx) SubScheduler() agent.DynamicSubScheduler { return nil }
func (mockToolCtx) WithAgentContext(ctx context.Context) agent.Context {
	return mockToolCtx{Context: ctx}
}
func (mockToolCtx) WithAgentTimeout(time.Duration) (agent.Context, context.CancelFunc) {
	return mockToolCtx{}, func() {}
}
func (mockToolCtx) WithAgentCancel() (agent.Context, context.CancelFunc) {
	return mockToolCtx{}, func() {}
}
func (mockToolCtx) OutputForAncestors() []string { return nil }
func (mockToolCtx) WithDelta(*agent.CommonContextDelta) agent.Context {
	return mockToolCtx{}
}

var _ agent.Context = mockToolCtx{}

// runnableTool mirrors the (unexported) adk interface implemented by tools.
type runnableTool interface {
	Run(ctx agent.Context, args any) (map[string]any, error)
}

func runTool(t *testing.T, tl any, args map[string]any) map[string]any {
	t.Helper()
	r, ok := tl.(runnableTool)
	if !ok {
		t.Fatalf("tool %T does not implement Run", tl)
	}
	out, err := r.Run(mockToolCtx{Context: context.Background()}, args)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	return out
}

// TestGitToolsRunEndToEnd builds the git tools and invokes them through Run,
// covering the factory closures.
func TestGitToolsRunEndToEnd(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Use an untracked file so we never need a commit (the user's git may force
	// gpg signing, which hangs in test). The factory closures are covered
	// regardless of whether a diff is produced.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diffTool, err := newGitFileDiffTool(sb)
	if err != nil {
		t.Fatalf("newGitFileDiffTool: %v", err)
	}
	runTool(t, diffTool, map[string]any{"file": "a.txt"})

	hunkTool, err := newGitHunkTool(sb)
	if err != nil {
		t.Fatalf("newGitHunkTool: %v", err)
	}
	runTool(t, hunkTool, map[string]any{"file": "a.txt"})

	ovTool, err := newGitOverviewTool(sb)
	if err != nil {
		t.Fatalf("newGitOverviewTool: %v", err)
	}
	runTool(t, ovTool, map[string]any{})
}

// TestTreeToolRunEndToEnd builds the tree tool and invokes it through Run.
func TestTreeToolRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb := testSandbox(t, dir)
	tr, err := newTreeTool(sb)
	if err != nil {
		t.Fatalf("newTreeTool: %v", err)
	}
	out := runTool(t, tr, map[string]any{"path": dir})
	if _, ok := out["tree"]; !ok {
		t.Errorf("tree output missing tree field: %#v", out)
	}
}

// TestMemoryToolsRunEndToEnd builds the memory tools and invokes them through
// Run, covering each factory closure.
func TestMemoryToolsRunEndToEnd(t *testing.T) {
	store := &memMockStore{}
	tools, err := MemoryTools(store)
	if err != nil {
		t.Fatalf("MemoryTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("MemoryTools returned %d tools, want 3", len(tools))
	}

	byName := map[string]map[string]any{
		"mem-search":   {"query": "anything"},
		"mem-timeline": {"anchor": 1},
		"mem-get":      {"ids": []int64{1}},
	}
	for _, tl := range tools {
		args, ok := byName[tl.Name()]
		if !ok {
			t.Errorf("unexpected memory tool %q", tl.Name())
			continue
		}
		runTool(t, tl, args)
	}
}

// TestLSPToolsRunSkipBranch builds every LSP tool with all language servers
// disabled and invokes each through Run. With no server available, every
// handler takes its early "no server" branch, covering the factory closures and
// the skip paths without spawning real language servers.
func TestLSPToolsRunSkipBranch(t *testing.T) {
	disabled := make([]string, 0)
	for name := range lsp.DefaultLanguages() {
		disabled = append(disabled, name)
	}
	mgr := lsp.NewManager(&lsp.ManagerConfig{Disabled: disabled})

	tools, err := LSPTools(mgr)
	if err != nil {
		t.Fatalf("LSPTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("LSPTools returned no tools")
	}

	args := map[string]map[string]any{
		"lsp-diagnostics":      {"file": "nope.unknownext"},
		"lsp-definition":       {"file": "nope.unknownext", "line": 0, "column": 0},
		"lsp-references":       {"file": "nope.unknownext", "line": 0, "column": 0},
		"lsp-hover":            {"file": "nope.unknownext", "line": 0, "column": 0},
		"lsp-symbols":          {"file": "nope.unknownext"},
		"lsp-workspace-symbol": {"query": "Foo"},
		"lsp-code-action":      {"file": "nope.unknownext", "start_line": 0, "start_col": 0, "end_line": 0, "end_col": 0},
	}
	for _, tl := range tools {
		a, ok := args[tl.Name()]
		if !ok {
			t.Errorf("no args for LSP tool %q", tl.Name())
			continue
		}
		runTool(t, tl, a)
	}
}

func TestTruncateAndAbs(t *testing.T) {
	t.Parallel()
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q, want hello", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q, want hello...", got)
	}
	if got := abs(-7); got != 7 {
		t.Errorf("abs(-7) = %d, want 7", got)
	}
	if got := abs(3); got != 3 {
		t.Errorf("abs(3) = %d, want 3", got)
	}
}
