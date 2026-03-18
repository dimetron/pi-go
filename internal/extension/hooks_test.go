package extension

import (
	"context"
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
