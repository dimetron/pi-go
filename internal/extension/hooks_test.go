package extension

import (
	"context"
	"fmt"
	"testing"
)

func TestHookConfigMatchesTool(t *testing.T) {
	tests := []struct {
		name     string
		hook     HookConfig
		toolName string
		want     bool
	}{
		{
			name:     "empty tools matches all",
			hook:     HookConfig{Tools: nil},
			toolName: "read",
			want:     true,
		},
		{
			name:     "matching tool",
			hook:     HookConfig{Tools: []string{"read", "write"}},
			toolName: "read",
			want:     true,
		},
		{
			name:     "non-matching tool",
			hook:     HookConfig{Tools: []string{"read", "write"}},
			toolName: "bash",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.hook.matchesTool(tt.toolName)
			if got != tt.want {
				t.Errorf("matchesTool(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestHookConfigTimeout(t *testing.T) {
	h := HookConfig{Timeout: 5}
	if d := h.timeout(); d.Seconds() != 5 {
		t.Errorf("timeout() = %v, want 5s", d)
	}

	h2 := HookConfig{}
	if d := h2.timeout(); d.Seconds() != 10 {
		t.Errorf("default timeout() = %v, want 10s", d)
	}
}

func TestBuildBeforeToolCallbacks(t *testing.T) {
	hooks := []HookConfig{
		{Event: "before_tool", Command: "echo before"},
		{Event: "after_tool", Command: "echo after"},
	}

	before := BuildBeforeToolCallbacks(hooks)
	if len(before) != 1 {
		t.Errorf("expected 1 before callback, got %d", len(before))
	}

	after := BuildAfterToolCallbacks(hooks)
	if len(after) != 1 {
		t.Errorf("expected 1 after callback, got %d", len(after))
	}
}

func TestBuildBeforeToolCallbacksEmpty(t *testing.T) {
	before := BuildBeforeToolCallbacks(nil)
	if len(before) != 0 {
		t.Errorf("expected 0 before callbacks, got %d", len(before))
	}
}

func TestBuildAfterToolCallbacksEmpty(t *testing.T) {
	after := BuildAfterToolCallbacks(nil)
	if len(after) != 0 {
		t.Errorf("expected 0 after callbacks, got %d", len(after))
	}
}

func TestRunHookCommand(t *testing.T) {
	hook := HookConfig{
		Event:   "before_tool",
		Command: "cat",
		Timeout: 5,
	}
	ctx := context.Background()

	// Test successful execution
	err := runHookCommand(ctx, hook, "read", map[string]any{"path": "/test"})
	if err != nil {
		t.Errorf("runHookCommand() error = %v", err)
	}

	// Test with invalid command (non-existent)
	badHook := HookConfig{
		Event:   "before_tool",
		Command: "nonexistent-command-12345",
		Timeout: 1,
	}
	err = runHookCommand(ctx, badHook, "read", nil)
	if err == nil {
		t.Error("expected error for non-existent command")
	}
}

func TestRunHookCommandTimeout(t *testing.T) {
	hook := HookConfig{
		Event:   "before_tool",
		Command: "sleep 10",
		Timeout: 1, // 1 second timeout
	}
	ctx := context.Background()

	err := runHookCommand(ctx, hook, "read", nil)
	// Should timeout
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestBuildBeforeToolCallbacksWithTools(t *testing.T) {
	// Test that filtering by tools works
	hooks := []HookConfig{
		{Event: "before_tool", Tools: []string{"read"}, Command: "echo before"},
		{Event: "before_tool", Tools: []string{"write"}, Command: "echo before_write"},
	}

	before := BuildBeforeToolCallbacks(hooks)
	if len(before) != 2 {
		t.Errorf("expected 2 before callbacks, got %d", len(before))
	}
}

func TestBuildAfterToolCallbacksWithTools(t *testing.T) {
	hooks := []HookConfig{
		{Event: "after_tool", Tools: []string{"read", "write"}, Command: "echo after"},
	}

	after := BuildAfterToolCallbacks(hooks)
	if len(after) != 1 {
		t.Errorf("expected 1 after callback, got %d", len(after))
	}
}

// TestBuildBeforeToolCallbacksMultipleHooks verifies multiple before_tool hooks
// each produce a separate callback.
func TestBuildBeforeToolCallbacksMultipleHooks(t *testing.T) {
	hooks := []HookConfig{
		{Event: "before_tool", Command: "echo a", Tools: []string{"read"}, Timeout: 5},
		{Event: "before_tool", Command: "echo b", Timeout: 5},
		{Event: "after_tool", Command: "echo c", Timeout: 5}, // should be skipped
	}

	cbs := BuildBeforeToolCallbacks(hooks)
	if len(cbs) != 2 {
		t.Errorf("expected 2 before callbacks, got %d", len(cbs))
	}
}

// TestBuildAfterToolCallbacksMultipleHooks verifies multiple after_tool hooks
// each produce a separate callback.
func TestBuildAfterToolCallbacksMultipleHooks(t *testing.T) {
	hooks := []HookConfig{
		{Event: "after_tool", Command: "echo a", Tools: []string{"write"}, Timeout: 5},
		{Event: "after_tool", Command: "echo b", Timeout: 5},
		{Event: "before_tool", Command: "echo c", Timeout: 5}, // should be skipped
	}

	cbs := BuildAfterToolCallbacks(hooks)
	if len(cbs) != 2 {
		t.Errorf("expected 2 after callbacks, got %d", len(cbs))
	}
}

type mockToolCallReporter struct {
	startedCalls []struct {
		name string
		args map[string]any
	}
	endedCalls []struct {
		args   map[string]any
		result any
		runErr error
	}
}

type mockTool struct {
	nameVal string
}

func (m mockTool) Name() string        { return m.nameVal }
func (m mockTool) Description() string { return "mock tool for testing" }
func (m mockTool) IsLongRunning() bool { return false }

func (m *mockToolCallReporter) OnToolStart(ctx context.Context, name string, args map[string]any) (string, error) {
	m.startedCalls = append(m.startedCalls, struct {
		name string
		args map[string]any
	}{name, args})
	return "call_test", nil
}

func (m *mockToolCallReporter) OnToolEnd(ctx context.Context, callID string, args map[string]any, result any, runErr error) error {
	m.endedCalls = append(m.endedCalls, struct {
		args   map[string]any
		result any
		runErr error
	}{args, result, runErr})
	return nil
}

func TestBuildToolCallCallbacks(t *testing.T) {
	m := &mockToolCallReporter{}
	beforeCBs, afterCBs := BuildToolCallCallbacks(m)

	if len(beforeCBs) != 1 {
		t.Fatalf("expected 1 before callback, got %d", len(beforeCBs))
	}
	if len(afterCBs) != 1 {
		t.Fatalf("expected 1 after callback, got %d", len(afterCBs))
	}

	// Simulate a before tool callback.
	args := map[string]any{"path": "/foo/bar"}
	tool := mockTool{}
	tool.nameVal = "read"
	_, err := beforeCBs[0](nil, tool, args)
	if err != nil {
		t.Fatalf("before callback failed: %v", err)
	}

	if len(m.startedCalls) != 1 {
		t.Fatalf("expected 1 started call, got %d", len(m.startedCalls))
	}
	if m.startedCalls[0].name != "read" {
		t.Errorf("tool name = %q, want %q", m.startedCalls[0].name, "read")
	}
	if m.startedCalls[0].args["path"] != "/foo/bar" {
		t.Errorf("tool args[path] = %v, want /foo/bar", m.startedCalls[0].args["path"])
	}

	// Simulate an after tool callback.
	result := map[string]any{"content": "hello world"}
	afterCBs[0](nil, tool, args, result, nil)

	if len(m.endedCalls) != 1 {
		t.Fatalf("expected 1 ended call, got %d", len(m.endedCalls))
	}
	if m.endedCalls[0].result.(map[string]any)["content"] != "hello world" {
		t.Errorf("result content = %v, want hello world", m.endedCalls[0].result.(map[string]any)["content"])
	}
	if m.endedCalls[0].runErr != nil {
		t.Errorf("runErr = %v, want nil", m.endedCalls[0].runErr)
	}
}

func TestBuildToolCallCallbacksAfterError(t *testing.T) {
	m := &mockToolCallReporter{}
	_, afterCBs := BuildToolCallCallbacks(m)

	tool := mockTool{}
	tool.nameVal = "bash"
	result := map[string]any{"stdout": "oops"}
	_, err := afterCBs[0](nil, tool, nil, result, fmt.Errorf("exit 1"))
	if err != nil {
		t.Fatalf("after callback returned error: %v", err)
	}

	if len(m.endedCalls) != 1 {
		t.Fatalf("expected 1 ended call, got %d", len(m.endedCalls))
	}
	if m.endedCalls[0].runErr == nil {
		t.Error("runErr = nil, want the error")
	}
}

// TestParseFrontmatterLine covers the happy path and the "no colon" edge case.
func TestParseFrontmatterLine(t *testing.T) {
	tests := []struct {
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{"name: my-skill", "name", "my-skill", true},
		{"description: Does something", "description", "Does something", true},
		{"tools: read, write, bash", "tools", "read, write, bash", true},
		// Value with a colon in it — SplitN(n=2) keeps the rest intact.
		{"key: val:ue", "key", "val:ue", true},
		// No colon → not a valid frontmatter line.
		{"no colon here", "", "", false},
		// Empty line.
		{"", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			key, value, ok := parseFrontmatterLine(tt.line)
			if ok != tt.wantOK {
				t.Errorf("parseFrontmatterLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
				return
			}
			if ok {
				if key != tt.wantKey {
					t.Errorf("key = %q, want %q", key, tt.wantKey)
				}
				if value != tt.wantValue {
					t.Errorf("value = %q, want %q", value, tt.wantValue)
				}
			}
		})
	}
}
