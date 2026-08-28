package agent

import (
	"errors"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"
)

// fakeTool is a minimal tool.Tool that also carries the Declaration and Run
// methods the ADK looks for, so a renamed wrapper can be exercised end to end.
type fakeTool struct {
	name   string
	desc   string
	noDecl bool
	noRun  bool

	ranWith any
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return f.desc }
func (f *fakeTool) IsLongRunning() bool { return false }

func (f *fakeTool) Declaration() *genai.FunctionDeclaration {
	if f.noDecl {
		return nil
	}
	return &genai.FunctionDeclaration{Name: f.name, Description: f.desc}
}

// ProcessRequest mirrors what a real MCP tool does, so a test that removes the
// dedupe layer reproduces the exact "duplicate tool" failure rather than a
// wiring artifact of the fake.
func (f *fakeTool) ProcessRequest(_ adkagent.Context, req *model.LLMRequest) error {
	return toolutils.PackTool(req, f)
}

func (f *fakeTool) Run(_ adkagent.Context, args any) (map[string]any, error) {
	if f.noRun {
		return nil, errors.New("unreachable")
	}
	f.ranWith = args
	return map[string]any{"called": f.name}, nil
}

// noRunTool omits Run entirely, standing in for a tool the ADK would not
// dispatch to.
type noRunTool struct{ name string }

func (n *noRunTool) Name() string        { return n.name }
func (n *noRunTool) Description() string { return "" }
func (n *noRunTool) IsLongRunning() bool { return false }

// fakeToolset returns a fixed list of tools, and records how often it was asked.
type fakeToolset struct {
	name  string
	tools []tool.Tool
	err   error
	calls int
}

func (f *fakeToolset) Name() string { return f.name }

func (f *fakeToolset) Tools(adkagent.ReadonlyContext) ([]tool.Tool, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func toolNames(t *testing.T, ts tool.Toolset) []string {
	t.Helper()
	got, err := ts.Tools(nil)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, tl := range got {
		names = append(names, tl.Name())
	}
	return names
}

func TestDedupeToolsets_RenamesCollisionWithBuiltin(t *testing.T) {
	builtins := []tool.Tool{&fakeTool{name: "find"}, &fakeTool{name: "bash"}}
	ts := &fakeToolset{name: "claude-chrome", tools: []tool.Tool{
		&fakeTool{name: "find"},
		&fakeTool{name: "navigate"},
	}}

	out := dedupeToolsets(builtins, []tool.Toolset{ts})
	if len(out) != 1 {
		t.Fatalf("got %d toolsets, want 1", len(out))
	}
	got := toolNames(t, out[0])
	want := []string{"claude-chrome_find", "navigate"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", got, want)
	}
	if out[0].Name() != "claude-chrome" {
		t.Errorf("toolset name = %q, want %q", out[0].Name(), "claude-chrome")
	}
}

func TestDedupeToolsets_RenamesCollisionAcrossToolsets(t *testing.T) {
	first := &fakeToolset{name: "alpha", tools: []tool.Tool{&fakeTool{name: "search"}}}
	second := &fakeToolset{name: "beta", tools: []tool.Tool{&fakeTool{name: "search"}}}

	out := dedupeToolsets(nil, []tool.Toolset{first, second})
	if got := toolNames(t, out[0]); got[0] != "search" {
		t.Errorf("first toolset name = %q, want %q (first claim wins)", got[0], "search")
	}
	if got := toolNames(t, out[1]); got[0] != "beta_search" {
		t.Errorf("second toolset name = %q, want %q", got[0], "beta_search")
	}
}

func TestDedupeToolsets_PrefixedNameAlsoTaken(t *testing.T) {
	// A built-in already occupies both "find" and the natural fallback, so the
	// numeric suffix is the only name left.
	builtins := []tool.Tool{&fakeTool{name: "find"}, &fakeTool{name: "chrome_find"}}
	ts := &fakeToolset{name: "chrome", tools: []tool.Tool{&fakeTool{name: "find"}}}

	if got := toolNames(t, dedupeToolsets(builtins, []tool.Toolset{ts})[0]); got[0] != "chrome_find_2" {
		t.Errorf("name = %q, want %q", got[0], "chrome_find_2")
	}
}

func TestDedupeToolsets_NameIsStableAcrossCalls(t *testing.T) {
	// Tools is called once per request. A name that drifted between turns would
	// orphan any function call the model already emitted against the old one.
	builtins := []tool.Tool{&fakeTool{name: "find"}}
	ts := &fakeToolset{name: "chrome", tools: []tool.Tool{&fakeTool{name: "find"}}}
	wrapped := dedupeToolsets(builtins, []tool.Toolset{ts})[0]

	first := toolNames(t, wrapped)
	second := toolNames(t, wrapped)
	if first[0] != second[0] {
		t.Errorf("name drifted between calls: %q then %q", first[0], second[0])
	}
	if ts.calls != 2 {
		t.Errorf("inner Tools called %d times, want 2", ts.calls)
	}
}

func TestDedupeToolsets_PassesThroughWhenNothingCollides(t *testing.T) {
	inner := &fakeTool{name: "navigate"}
	ts := &fakeToolset{name: "chrome", tools: []tool.Tool{inner}}

	got, err := dedupeToolsets([]tool.Tool{&fakeTool{name: "find"}}, []tool.Toolset{ts})[0].Tools(nil)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if got[0] != tool.Tool(inner) {
		t.Errorf("non-colliding tool was wrapped; want the original instance back")
	}
}

func TestDedupeToolsets_EmptyAndNilInputs(t *testing.T) {
	if got := dedupeToolsets(nil, nil); got != nil {
		t.Errorf("dedupeToolsets(nil, nil) = %v, want nil", got)
	}
	// A nil entry in either slice must not panic, and nil toolsets are dropped.
	out := dedupeToolsets([]tool.Tool{nil}, []tool.Toolset{nil})
	if len(out) != 0 {
		t.Errorf("nil toolset kept: %v", out)
	}
	ts := &fakeToolset{name: "chrome", tools: []tool.Tool{nil, &fakeTool{name: "navigate"}}}
	if got := toolNames(t, dedupeToolsets(nil, []tool.Toolset{ts})[0]); len(got) != 1 || got[0] != "navigate" {
		t.Errorf("names = %v, want [navigate]", got)
	}
}

func TestDedupeToolsets_PropagatesToolsetError(t *testing.T) {
	wantErr := errors.New("boom")
	ts := &fakeToolset{name: "chrome", err: wantErr}
	if _, err := dedupeToolsets(nil, []tool.Toolset{ts})[0].Tools(nil); !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestDedupedToolset_Unwrap(t *testing.T) {
	ts := &fakeToolset{name: "chrome", tools: []tool.Tool{&fakeTool{name: "find"}}}
	wrapped, ok := dedupeToolsets(nil, []tool.Toolset{ts})[0].(*dedupedToolset)
	if !ok {
		t.Fatal("wrapped toolset is not a *dedupedToolset")
	}
	if wrapped.Unwrap() != tool.Toolset(ts) {
		t.Error("Unwrap did not return the inner toolset")
	}
}

func TestRenamedTool_DeclarationAndDelegation(t *testing.T) {
	inner := &fakeTool{name: "find", desc: "find things"}
	r := &renamedTool{Tool: inner, name: "chrome_find"}

	if r.Name() != "chrome_find" {
		t.Errorf("Name = %q, want chrome_find", r.Name())
	}
	if r.Description() != "find things" {
		t.Errorf("Description = %q, want the inner description", r.Description())
	}
	decl := r.Declaration()
	if decl == nil || decl.Name != "chrome_find" {
		t.Fatalf("Declaration = %+v, want name chrome_find", decl)
	}
	if decl.Description != "find things" {
		t.Errorf("Declaration.Description = %q, want the inner description", decl.Description)
	}
	// The inner declaration must not be mutated: the tool still calls its MCP
	// server under the original name.
	if inner.Declaration().Name != "find" {
		t.Errorf("inner declaration renamed to %q", inner.Declaration().Name)
	}

	out, err := r.Run(nil, map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["called"] != "find" {
		t.Errorf("Run delegated to %v, want the inner tool", out["called"])
	}
	if inner.ranWith == nil {
		t.Error("inner tool did not receive the arguments")
	}
}

func TestRenamedTool_ProcessRequestUsesNewName(t *testing.T) {
	r := &renamedTool{Tool: &fakeTool{name: "find"}, name: "chrome_find"}
	req := &model.LLMRequest{}
	if err := r.ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	if _, ok := req.Tools["chrome_find"]; !ok {
		t.Errorf("request tools = %v, want key chrome_find", req.Tools)
	}
	if _, ok := req.Tools["find"]; ok {
		t.Error("request registered the original name too")
	}

	// The built-in packer rejects a second claim on a name, which is the
	// failure this whole layer exists to prevent — so packing "find" after
	// the rename must succeed.
	builtin := &fakeTool{name: "find"}
	if err := toolutils.PackTool(req, builtin); err != nil {
		t.Errorf("packing the built-in after the rename failed: %v", err)
	}
}

func TestRenamedTool_MissingDeclarationAndRun(t *testing.T) {
	if got := (&renamedTool{Tool: &fakeTool{name: "x", noDecl: true}, name: "y"}).Declaration(); got != nil {
		t.Errorf("Declaration = %+v, want nil when the inner tool has none", got)
	}
	if got := (&renamedTool{Tool: &noRunTool{name: "x"}, name: "y"}).Declaration(); got != nil {
		t.Errorf("Declaration = %+v, want nil when the inner tool declares nothing", got)
	}
	_, err := (&renamedTool{Tool: &noRunTool{name: "x"}, name: "y"}).Run(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not callable") {
		t.Errorf("Run err = %v, want a not-callable error", err)
	}
}

func TestSanitizeToolName(t *testing.T) {
	tests := map[string]string{
		"claude-chrome":  "claude-chrome",
		"my server":      "my_server",
		"a//b":           "a_b",
		"_leading_":      "leading",
		"ok.name-1":      "ok.name-1",
		"###":            "",
		"tabs\tand\nnl":  "tabs_and_nl",
		"Mixed Case 123": "Mixed_Case_123",
	}
	for in, want := range tests {
		if got := sanitizeToolName(in); got != want {
			t.Errorf("sanitizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrefixedToolName_UnusableToolsetName(t *testing.T) {
	if got := prefixedToolName("###", "find"); got != "mcp_find" {
		t.Errorf("prefixedToolName = %q, want mcp_find", got)
	}
}

func TestTruncateToolName(t *testing.T) {
	short := strings.Repeat("a", maxToolNameLen)
	if got := truncateToolName(short); got != short {
		t.Errorf("a name at the limit was altered: %q", got)
	}
	long := strings.Repeat("p", 100) + "_toolname"
	got := truncateToolName(long)
	if len(got) > maxToolNameLen {
		t.Errorf("len = %d, want <= %d", len(got), maxToolNameLen)
	}
	if !strings.HasSuffix(got, "_toolname") {
		t.Errorf("got %q, want the tool's own name preserved at the end", got)
	}
}

func TestDedupeToolsets_LongNamesStayWithinLimit(t *testing.T) {
	server := strings.Repeat("s", 80)
	builtins := []tool.Tool{&fakeTool{name: "find"}}
	ts := &fakeToolset{name: server, tools: []tool.Tool{&fakeTool{name: "find"}}}

	got := toolNames(t, dedupeToolsets(builtins, []tool.Toolset{ts})[0])
	if len(got[0]) > maxToolNameLen {
		t.Errorf("renamed to %q (%d chars), want <= %d", got[0], len(got[0]), maxToolNameLen)
	}
	if got[0] == "find" {
		t.Error("collision was not resolved")
	}
}
