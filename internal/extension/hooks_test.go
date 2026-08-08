package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"
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
	mu           sync.Mutex
	startedCalls []struct {
		name string
		args map[string]any
	}
	endedCalls []struct {
		callID string
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startedCalls = append(m.startedCalls, struct {
		name string
		args map[string]any
	}{name, args})
	return "call_test", nil
}

func (m *mockToolCallReporter) OnToolEnd(ctx context.Context, callID string, args map[string]any, result any, runErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endedCalls = append(m.endedCalls, struct {
		callID string
		args   map[string]any
		result any
		runErr error
	}{callID, args, result, runErr})
	return nil
}

// mockToolCtx is a minimal agent.Context implementation for testing callback
// correlation. Only FunctionCallID() is meaningful; all other methods return
// zero values. The full v2 method set is implemented so the mock stays
// forward-compatible with future agent.Context surface growth.
type mockToolCtx struct {
	context.Context
	funcCallID string
}

func (c *mockToolCtx) FunctionCallID() string         { return c.funcCallID }
func (c *mockToolCtx) Actions() *session.EventActions { return nil }
func (c *mockToolCtx) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (c *mockToolCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
func (c *mockToolCtx) RequestConfirmation(string, any) error                { return nil }
func (c *mockToolCtx) AgentName() string                                    { return "" }
func (c *mockToolCtx) ReadonlyState() session.ReadonlyState                 { return nil }
func (c *mockToolCtx) State() session.State                                 { return nil }
func (c *mockToolCtx) Artifacts() agent.Artifacts                           { return nil }
func (c *mockToolCtx) InvocationID() string                                 { return "" }
func (c *mockToolCtx) UserContent() *genai.Content                          { return nil }
func (c *mockToolCtx) AppName() string                                      { return "" }
func (c *mockToolCtx) Branch() string                                       { return "" }
func (c *mockToolCtx) SessionID() string                                    { return "" }
func (c *mockToolCtx) UserID() string                                       { return "" }

// InvocationContext surface (v1) and v2 additions.
func (c *mockToolCtx) Agent() agent.Agent          { return nil }
func (c *mockToolCtx) Memory() agent.Memory        { return nil }
func (c *mockToolCtx) Session() session.Session    { return nil }
func (c *mockToolCtx) RunConfig() *agent.RunConfig { return nil }
func (c *mockToolCtx) EndInvocation()              {}
func (c *mockToolCtx) Ended() bool                 { return false }
func (c *mockToolCtx) WithContext(ctx context.Context) agent.InvocationContext {
	c.Context = ctx
	return c
}
func (c *mockToolCtx) IsolationScope() string { return "" }
func (c *mockToolCtx) ResumedInput(string) (any, bool) {
	return nil, false
}
func (c *mockToolCtx) WithICDelta(*agent.InvocationContextDelta) agent.InvocationContext {
	return c
}

// agent.Context-only surface (v2 additions).
func (c *mockToolCtx) Path() string                            { return "" }
func (c *mockToolCtx) RunID() string                           { return "" }
func (c *mockToolCtx) SubScheduler() agent.DynamicSubScheduler { return nil }
func (c *mockToolCtx) WithAgentContext(ctx context.Context) agent.Context {
	c.Context = ctx
	return c
}
func (c *mockToolCtx) WithAgentTimeout(time.Duration) (agent.Context, context.CancelFunc) {
	return c, func() {}
}
func (c *mockToolCtx) WithAgentCancel() (agent.Context, context.CancelFunc) {
	return c, func() {}
}
func (c *mockToolCtx) OutputForAncestors() []string { return nil }
func (c *mockToolCtx) WithDelta(*agent.CommonContextDelta) agent.Context {
	return c
}

var _ agent.Context = (*mockToolCtx)(nil)

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

// TestBuildToolCallCallbacks_CallIDPropagated is the RED test: it asserts that
// OnToolEnd receives the call ID returned by OnToolStart so the ACP peer can
// match the completion update to the correct StartToolCall event.
//
// Currently fails because BuildToolCallCallbacks discards the ID and passes ""
// to OnToolEnd — that silently drops all tool results in the ACP stream.
func TestBuildToolCallCallbacks_CallIDPropagated(t *testing.T) {
	m := &mockToolCallReporter{}
	beforeCBs, afterCBs := BuildToolCallCallbacks(m)

	ctx := &mockToolCtx{Context: context.Background(), funcCallID: "adk-fc-42"}
	tool := mockTool{nameVal: "bash"}
	args := map[string]any{"command": "echo hello"}
	result := map[string]any{"stdout": "hello\n", "stderr": "", "exit_code": 0}

	if _, err := beforeCBs[0](ctx, tool, args); err != nil {
		t.Fatalf("beforeCB: %v", err)
	}
	if _, err := afterCBs[0](ctx, tool, args, result, nil); err != nil {
		t.Fatalf("afterCB: %v", err)
	}

	if len(m.endedCalls) != 1 {
		t.Fatalf("OnToolEnd called %d times, want 1", len(m.endedCalls))
	}
	// The call ID passed to OnToolEnd must be the one returned by OnToolStart,
	// not the empty string that would make it a no-op in the real adapter.
	if got, want := m.endedCalls[0].callID, "call_test"; got != want {
		t.Errorf("OnToolEnd callID = %q, want %q (got empty string means tool results are silently dropped)", got, want)
	}
}

// TestBuildToolCallCallbacks_ConcurrentCallIDCorrelation fires N parallel
// tool invocations and verifies each gets its own call ID in OnToolEnd.
func TestBuildToolCallCallbacks_ConcurrentCallIDCorrelation(t *testing.T) {
	const workers = 10

	// Each OnToolStart call returns a unique ID so we can verify mapping.
	var mu sync.Mutex
	var callSeq int
	reporter := &spyReporter{}
	reporter.startFn = func() string {
		mu.Lock()
		defer mu.Unlock()
		callSeq++
		return fmt.Sprintf("call_%d", callSeq)
	}

	beforeCBs, afterCBs := BuildToolCallCallbacks(reporter)
	tool := mockTool{nameVal: "bash"}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			ctx := &mockToolCtx{Context: context.Background(), funcCallID: fmt.Sprintf("adk-fc-%d", i)}
			args := map[string]any{"command": fmt.Sprintf("echo %d", i)}
			result := map[string]any{"stdout": fmt.Sprintf("%d\n", i)}

			if _, err := beforeCBs[0](ctx, tool, args); err != nil {
				t.Errorf("worker %d beforeCB: %v", i, err)
				return
			}
			if _, err := afterCBs[0](ctx, tool, args, result, nil); err != nil {
				t.Errorf("worker %d afterCB: %v", i, err)
			}
		}()
	}
	wg.Wait()

	reporter.mu.Lock()
	defer reporter.mu.Unlock()

	if len(reporter.endedCallIDs) != workers {
		t.Fatalf("OnToolEnd called %d times, want %d", len(reporter.endedCallIDs), workers)
	}
	for _, id := range reporter.endedCallIDs {
		if id == "" {
			t.Error("OnToolEnd received empty call ID — tool result will be silently dropped")
		}
	}
	// Every started call ID must appear in ended call IDs.
	started := make(map[string]bool, len(reporter.startedCallIDs))
	for _, id := range reporter.startedCallIDs {
		started[id] = true
	}
	for _, id := range reporter.endedCallIDs {
		if !started[id] {
			t.Errorf("ended call ID %q was not returned by any OnToolStart", id)
		}
	}
}

// spyReporter records start/end call IDs for concurrent correlation tests.
type spyReporter struct {
	mu             sync.Mutex
	startFn        func() string
	startedCallIDs []string
	endedCallIDs   []string
}

func (s *spyReporter) OnToolStart(_ context.Context, _ string, _ map[string]any) (string, error) {
	id := s.startFn()
	s.mu.Lock()
	s.startedCallIDs = append(s.startedCallIDs, id)
	s.mu.Unlock()
	return id, nil
}

func (s *spyReporter) OnToolEnd(_ context.Context, callID string, _ map[string]any, _ any, _ error) error {
	s.mu.Lock()
	s.endedCallIDs = append(s.endedCallIDs, callID)
	s.mu.Unlock()
	return nil
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

func TestBuildTracingCallbacks(t *testing.T) {
	// BuildTracingCallbacks creates before/after tool callbacks that emit OTEL spans.
	// We test that the callbacks are created and can be called without panicking.
	// The actual span emission depends on OTEL configuration at runtime.
	before, after := BuildTracingCallbacks()
	if len(before) != 1 {
		t.Fatalf("expected 1 before callback, got %d", len(before))
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 after callback, got %d", len(after))
	}

	// Create a mock tool and context
	mockT := mockTool{nameVal: "read"}
	ctx := &mockToolCtx{Context: context.Background(), funcCallID: "test-call-1"}
	args := map[string]any{"path": "/test"}

	// Call before callback - should not panic
	_, err := before[0](ctx, mockT, args)
	if err != nil {
		t.Fatalf("before callback failed: %v", err)
	}

	// Call after callback with success - should not panic
	result := map[string]any{"content": "test"}
	_, err = after[0](ctx, mockT, args, result, nil)
	if err != nil {
		t.Fatalf("after callback failed: %v", err)
	}

	// Call after callback with error - should not panic
	testErr := fmt.Errorf("tool execution failed")
	_, err = after[0](ctx, mockT, args, result, testErr)
	if err != nil {
		t.Fatalf("after callback with error failed: %v", err)
	}
}

func TestBuildLLMTracingCallbacks(t *testing.T) {
	// BuildLLMTracingCallbacks creates before/after model callbacks that emit OTEL spans.
	// We test that the callbacks are created and can be called without panicking.
	// The actual span emission depends on OTEL configuration at runtime.
	before, after := BuildLLMTracingCallbacks("openai")
	if len(before) != 1 {
		t.Fatalf("expected 1 before callback, got %d", len(before))
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 after callback, got %d", len(after))
	}

	// Create a mock LLM request
	req := &model.LLMRequest{
		Model: "claude-3-5-sonnet",
	}

	// Create a minimal mock context
	callbackCtx := &mockReadonlyContext{Context: context.Background()}

	// Call before callback - should not panic
	_, err := before[0](callbackCtx, req)
	if err != nil {
		t.Fatalf("before callback failed: %v", err)
	}

	// Call after callback with response - should not panic
	resp := &model.LLMResponse{
		ModelVersion: req.Model,
	}
	_, err = after[0](callbackCtx, resp, nil)
	if err != nil {
		t.Fatalf("after callback failed: %v", err)
	}

	// Call after callback with error - should not panic
	testErr := fmt.Errorf("model request failed")
	_, err = after[0](callbackCtx, resp, testErr)
	if err != nil {
		t.Fatalf("after callback with error failed: %v", err)
	}
}

// mockReadonlyContext is a minimal implementation of agent.Context for testing.
// The full v2 method set is implemented so the mock stays forward-compatible
// with future agent.Context surface growth.
type mockReadonlyContext struct {
	context.Context
	invocationID string
}

func (c *mockReadonlyContext) UserContent() *genai.Content          { return nil }
func (c *mockReadonlyContext) InvocationID() string                 { return c.invocationID }
func (c *mockReadonlyContext) AgentName() string                    { return "" }
func (c *mockReadonlyContext) ReadonlyState() session.ReadonlyState { return nil }
func (c *mockReadonlyContext) UserID() string                       { return "" }
func (c *mockReadonlyContext) AppName() string                      { return "" }
func (c *mockReadonlyContext) SessionID() string                    { return "" }
func (c *mockReadonlyContext) Branch() string                       { return "" }
func (c *mockReadonlyContext) Artifacts() agent.Artifacts           { return nil }
func (c *mockReadonlyContext) State() session.State                 { return nil }

// Tool-context surface (was ToolContext in v1.4.0) and shared callback helpers.
func (c *mockReadonlyContext) FunctionCallID() string         { return "" }
func (c *mockReadonlyContext) Actions() *session.EventActions { return nil }
func (c *mockReadonlyContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (c *mockReadonlyContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return nil
}
func (c *mockReadonlyContext) RequestConfirmation(string, any) error { return nil }

// InvocationContext surface.
func (c *mockReadonlyContext) Agent() agent.Agent          { return nil }
func (c *mockReadonlyContext) Memory() agent.Memory        { return nil }
func (c *mockReadonlyContext) Session() session.Session    { return nil }
func (c *mockReadonlyContext) RunConfig() *agent.RunConfig { return nil }
func (c *mockReadonlyContext) EndInvocation()              {}
func (c *mockReadonlyContext) Ended() bool                 { return false }
func (c *mockReadonlyContext) WithContext(ctx context.Context) agent.InvocationContext {
	c.Context = ctx
	return c
}
func (c *mockReadonlyContext) IsolationScope() string { return "" }
func (c *mockReadonlyContext) ResumedInput(string) (any, bool) {
	return nil, false
}
func (c *mockReadonlyContext) WithICDelta(*agent.InvocationContextDelta) agent.InvocationContext {
	return c
}

// agent.Context-only surface (v2 additions).
func (c *mockReadonlyContext) Path() string                            { return "" }
func (c *mockReadonlyContext) RunID() string                           { return "" }
func (c *mockReadonlyContext) SubScheduler() agent.DynamicSubScheduler { return nil }
func (c *mockReadonlyContext) WithAgentContext(ctx context.Context) agent.Context {
	c.Context = ctx
	return c
}
func (c *mockReadonlyContext) WithAgentTimeout(time.Duration) (agent.Context, context.CancelFunc) {
	return c, func() {}
}
func (c *mockReadonlyContext) WithAgentCancel() (agent.Context, context.CancelFunc) {
	return c, func() {}
}
func (c *mockReadonlyContext) OutputForAncestors() []string { return nil }
func (c *mockReadonlyContext) WithDelta(*agent.CommonContextDelta) agent.Context {
	return c
}

var _ agent.Context = (*mockReadonlyContext)(nil)

func TestRunLifecycleHook(t *testing.T) {
	dir := t.TempDir()
	out := dir + "/out.json"
	hook := HookConfig{
		Event:   "turn_complete",
		Command: "cat > " + out,
		Timeout: 5,
	}
	ctx := context.Background()

	err := RunLifecycleHook(ctx, hook, "turn_complete", map[string]any{"error": false})
	if err != nil {
		t.Fatalf("RunLifecycleHook() error = %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading hook output: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal hook output: %v", err)
	}
	if got["event"] != "turn_complete" {
		t.Errorf("event = %v, want turn_complete", got["event"])
	}
	if d, ok := got["data"].(map[string]any); !ok || d["error"] != false {
		t.Errorf("data = %v, want {error:false}", got["data"])
	}
}

func TestRunLifecycleHookTimeout(t *testing.T) {
	hook := HookConfig{
		Event:   "user_input_required",
		Command: "sleep 10",
		Timeout: 1,
	}
	ctx := context.Background()
	if err := RunLifecycleHook(ctx, hook, "user_input_required", nil); err == nil {
		t.Error("expected timeout error")
	}
}
