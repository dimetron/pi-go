package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/dimetron/pi-go/internal/config"
)

// markerTool is a do-nothing tool whose only job is to be identifiable by name
// at a known position in the assembled tool list.
func markerTool(t *testing.T, name string) adktool.Tool {
	t.Helper()
	tl, err := functiontool.New(
		functiontool.Config{Name: name, Description: "a marker tool"},
		func(_ adkagent.Context, _ struct{}) (string, error) { return "", nil },
	)
	if err != nil {
		t.Fatalf("building marker tool: %v", err)
	}
	return tl
}

// buildTestRuntime runs the subsystem assembly New extracted into
// buildRuntime, on a bare Agent, with cleanup registered.
func buildTestRuntime(t *testing.T, providerName string, extra ...Option) (*Agent, *runtimeParts) {
	t.Helper()
	isolate(t)

	o := defaultOptions()
	// Match newTestAgent: every optional subsystem off, so the assertions are
	// about wiring rather than about which subsystems happen to be enabled.
	for _, opt := range append([]Option{WithSubagents(false), WithLSP(LSPOff)}, extra...) {
		opt(&o)
	}

	a := &Agent{workDir: t.TempDir()}
	cfg := config.Config{}
	rt, err := a.buildRuntime(t.Context(), o, &cfg, providerName)
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return a, rt
}

func toolNames(ts []adktool.Tool) []string {
	names := make([]string, 0, len(ts))
	for _, tl := range ts {
		names = append(names, tl.Name())
	}
	return names
}

func hasTool(ts []adktool.Tool, name string) bool {
	for _, n := range toolNames(ts) {
		if n == name {
			return true
		}
	}
	return false
}

// TestBuildRuntimeWiresEverySubsystem checks the struct buildRuntime returns
// carries exactly what New still needs afterwards: the sandbox and LSP manager
// for callbacks, the orchestrator for the ACP log path, and the tool list.
// A nil in any of these is a silent miswiring rather than a build failure.
func TestBuildRuntimeWiresEverySubsystem(t *testing.T) {
	_, rt := buildTestRuntime(t, "anthropic")

	if rt.sandbox == nil {
		t.Error("sandbox is nil; buildCallbacks needs it")
	}
	if rt.orch == nil {
		t.Error("orch is nil; onNewSession needs it for the ACP log path")
	}
	if rt.lspMgr == nil {
		t.Error("lspMgr is nil; buildCallbacks needs it")
	}
	if len(rt.tools) == 0 {
		t.Error("tools is empty; the model would have nothing to call")
	}
}

// TestBuildRuntimeRegistersClosers checks every acquisition still pushes its
// cleanup. Losing one leaks a sandbox, a process tree, or an LSP server.
func TestBuildRuntimeRegistersClosers(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	for _, opt := range []Option{WithSubagents(false), WithLSP(LSPOff)} {
		opt(&o)
	}

	a := &Agent{workDir: t.TempDir()}
	cfg := config.Config{}
	if _, err := a.buildRuntime(t.Context(), o, &cfg, "anthropic"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}

	// sandbox, bash supervisor, orchestrator, memory, palace, LSP.
	if want := 6; len(a.closers) != want {
		t.Errorf("registered %d closers, want %d: one subsystem is not being released", len(a.closers), want)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a.closers != nil {
		t.Error("Close did not clear the closer list")
	}
	// Close is documented as safe to call more than once.
	if err := a.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestBuildRuntimeCoreToolsPresent checks the base tool set survives the move.
func TestBuildRuntimeCoreToolsPresent(t *testing.T) {
	_, rt := buildTestRuntime(t, "anthropic")
	for _, name := range []string{"bash", "read", "write", "edit", "bash_wait", "bash_kill"} {
		if !hasTool(rt.tools, name) {
			t.Errorf("core tool %q is missing; got %v", name, toolNames(rt.tools))
		}
	}
}

// TestBuildRuntimeExtraToolsComeLast pins the append order the original
// comment insisted on: caller-supplied tools are appended, never substituted,
// so the core set is still there in front of them.
func TestBuildRuntimeExtraToolsComeLast(t *testing.T) {
	extra := markerTool(t, "piagent_test_marker")

	_, rt := buildTestRuntime(t, "anthropic", WithTools(extra))

	if len(rt.tools) < 2 {
		t.Fatalf("got %d tools, want the core set plus the extra", len(rt.tools))
	}
	if got := rt.tools[len(rt.tools)-1].Name(); got != "piagent_test_marker" {
		t.Errorf("last tool = %q, want the caller's tool appended last", got)
	}
	if !hasTool(rt.tools, "bash") {
		t.Error("core tools were replaced rather than appended to")
	}
}

// TestBuildRuntimeSubagentToolsAreOptional covers the one conditional block in
// the tool assembly.
func TestBuildRuntimeSubagentToolsAreOptional(t *testing.T) {
	_, off := buildTestRuntime(t, "anthropic", WithSubagents(false))
	_, on := buildTestRuntime(t, "anthropic", WithSubagents(true))

	if len(on.tools) <= len(off.tools) {
		t.Fatalf("subagents on gave %d tools, off gave %d; enabling them must add tools",
			len(on.tools), len(off.tools))
	}
	// Whatever the extra tools are called, none of them may appear when
	// subagents are disabled.
	offNames := map[string]bool{}
	for _, n := range toolNames(off.tools) {
		offNames[n] = true
	}
	added := 0
	for _, n := range toolNames(on.tools) {
		if !offNames[n] {
			added++
		}
	}
	if added == 0 {
		t.Error("enabling subagents added no uniquely named tool")
	}
}

// TestBuildRuntimeGeminiGrounding pins the provider-conditional tool. It is
// appended, so on a non-Gemini provider the list must be otherwise identical.
func TestBuildRuntimeGeminiGrounding(t *testing.T) {
	_, gemini := buildTestRuntime(t, "gemini")
	_, anthropic := buildTestRuntime(t, "anthropic")

	if len(gemini.tools) != len(anthropic.tools)+1 {
		t.Fatalf("gemini gave %d tools, anthropic %d; want exactly one more",
			len(gemini.tools), len(anthropic.tools))
	}
	// The grounding tool is the last thing appended before the caller's tools,
	// and there are none here.
	if gemini.tools[len(gemini.tools)-1].Name() == anthropic.tools[len(anthropic.tools)-1].Name() {
		t.Error("gemini's extra tool is not at the tail where it was appended")
	}
}

// TestBuildRuntimeGroundingDisabledByEnv checks the escape hatch still works
// through the extracted path.
func TestBuildRuntimeGroundingDisabledByEnv(t *testing.T) {
	t.Setenv("PI_NO_GROUNDING", "1")
	_, gemini := buildTestRuntime(t, "gemini")
	_, anthropic := buildTestRuntime(t, "anthropic")

	if len(gemini.tools) != len(anthropic.tools) {
		t.Errorf("grounding disabled: gemini gave %d tools, anthropic %d; want equal",
			len(gemini.tools), len(anthropic.tools))
	}
}

// TestBuildRuntimeMemoryOffLeavesStoreNil checks buildRuntime's one write to
// the Agent it is a method on. New reads a.memStore afterwards to build the
// memory context, so this is a real cross-function dependency.
func TestBuildRuntimeMemoryOffLeavesStoreNil(t *testing.T) {
	a, rt := buildTestRuntime(t, "anthropic")
	if a.memStore != nil {
		t.Error("memStore is set with memory disabled")
	}
	if rt.memWorker != nil {
		t.Error("memWorker is set with memory disabled")
	}
	// No memory means no memory tools.
	if hasTool(rt.tools, "memory_recall") {
		t.Error("memory tools present with memory disabled")
	}
}

// TestBuildRuntimePalaceOffLeavesContextEmpty covers the field New appends to
// the instruction only when non-empty.
func TestBuildRuntimePalaceOffLeavesContextEmpty(t *testing.T) {
	_, rt := buildTestRuntime(t, "anthropic")
	if rt.palaceContext != "" {
		t.Errorf("palaceContext = %q, want empty with palace disabled", rt.palaceContext)
	}
}

// TestNewWiresRuntimeIntoTheAgent checks the seam between New and
// buildRuntime end to end: the tool list the runtime produced is the one the
// agent exposes, and the agent is usable afterwards.
func TestNewWiresRuntimeIntoTheAgent(t *testing.T) {
	marker := markerTool(t, "piagent_new_marker")

	llm := &fakeLLM{name: "claude-test", reply: "ok"}
	ag := newTestAgent(t, llm, WithTools(marker))

	tools := ag.Tools()
	if len(tools) == 0 {
		t.Fatal("agent exposes no tools")
	}
	if got := tools[len(tools)-1].Name(); got != "piagent_new_marker" {
		t.Errorf("last tool = %q, want the caller's tool", got)
	}
	if !hasTool(tools, "bash") {
		t.Error("core tools missing from the assembled agent")
	}

	// The orchestrator reached onNewSession, which is where rt.orch is used.
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sessionID == "" {
		t.Error("NewSession returned an empty id")
	}
}

// TestNewRejectsMalformedConfig covers New's third early return, and pins that
// it fires before any subsystem is acquired — the assembly buildRuntime now
// owns must not have started, so there is nothing to leak.
func TestNewRejectsMalformedConfig(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".pi-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".pi-go", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	ag, err := New(t.Context(),
		WithModel(&fakeLLM{name: "claude-test", reply: "ok"}),
		WithWorkingDir(workDir),
		WithSessionDir(t.TempDir()),
		WithSubagents(false),
		WithLSP(LSPOff),
	)
	if err == nil {
		_ = ag.Close()
		t.Fatal("expected an error for a malformed config")
	}
	if ag != nil {
		t.Error("expected a nil agent alongside the error")
	}
	if !strings.Contains(err.Error(), "loading config") {
		t.Errorf("error = %q, want a config-loading failure", err)
	}
}
