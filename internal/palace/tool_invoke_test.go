package palace

import (
	"context"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"
)

// mockToolCtx is a minimal agent.ToolContext backed by a real context.Context so
// that tool handlers (which forward the context to SQLite operations) work. It
// lets tests invoke tools through the tool.Tool interface, exercising the
// functiontool factory closures that direct handler calls never reach.
type mockToolCtx struct {
	context.Context
}

func (mockToolCtx) FunctionCallID() string         { return "" }
func (mockToolCtx) Actions() *session.EventActions { return &session.EventActions{} }
func (mockToolCtx) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
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

var _ agent.ToolContext = mockToolCtx{}

// runnableTool mirrors the adk-internal interface implemented by functiontool
// tools, allowing tests to invoke a tool end-to-end.
type runnableTool interface {
	Run(ctx agent.ToolContext, args any) (map[string]any, error)
}

// TestPalaceToolsRunEndToEnd builds every palace tool and invokes it through the
// tool.Tool.Run interface. This covers the functiontool factory closures (which
// only direct-handler tests would otherwise leave uncovered) and the PalaceTools
// builder loop.
func TestPalaceToolsRunEndToEnd(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	tools, err := PalaceTools(p)
	if err != nil {
		t.Fatalf("PalaceTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("PalaceTools returned no tools")
	}

	byName := make(map[string]int)
	for _, tl := range tools {
		byName[tl.Name()]++
	}

	ctx := mockToolCtx{Context: context.Background()}

	// Each tool is invoked with valid arguments so the handler runs its happy
	// path. Order matters: add-drawer and kg-add seed data the read tools use.
	args := []struct {
		name string
		in   map[string]any
	}{
		{"palace-status", map[string]any{}},
		{"palace-add-drawer", map[string]any{"wing": "proj", "room": "auth", "content": "JWT tokens expire after 1h"}},
		{"palace-kg-add", map[string]any{"subject": "Alice", "predicate": "works_on", "object": "auth"}},
		{"palace-kg-query", map[string]any{"entity": "Alice"}},
		{"palace-kg-timeline", map[string]any{"entity": "Alice"}},
		{"palace-kg-invalidate", map[string]any{"subject": "Alice", "predicate": "works_on", "object": "auth"}},
		{"palace-diary-write", map[string]any{"agent_name": "tester", "entry": "today I tested tools", "topic": "testing"}},
		{"palace-diary-read", map[string]any{"agent_name": "tester"}},
		{"palace-search", map[string]any{"query": "JWT"}},
		{"palace-traverse", map[string]any{"start_room": "auth"}},
	}

	toolByName := make(map[string]int, len(tools))
	for i, tl := range tools {
		toolByName[tl.Name()] = i
	}

	for _, a := range args {
		idx, ok := toolByName[a.name]
		if !ok {
			t.Errorf("tool %q not registered by PalaceTools", a.name)
			continue
		}
		r, ok := tools[idx].(runnableTool)
		if !ok {
			t.Errorf("tool %q does not implement Run", a.name)
			continue
		}
		out, runErr := r.Run(ctx, a.in)
		if runErr != nil {
			t.Errorf("%s Run error: %v", a.name, runErr)
			continue
		}
		if _, ok := out["content"]; !ok {
			t.Errorf("%s output missing content field: %#v", a.name, out)
		}
	}
}

// TestPalaceToolsNilPalace verifies PalaceTools returns nil for a disabled palace.
func TestPalaceToolsNilPalace(t *testing.T) {
	t.Parallel()
	tools, err := PalaceTools(nil)
	if err != nil {
		t.Fatalf("PalaceTools(nil): %v", err)
	}
	if tools != nil {
		t.Errorf("PalaceTools(nil) = %v, want nil", tools)
	}
}
