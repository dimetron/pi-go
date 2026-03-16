// Package extension provides hooks (before/after tool call), skill loading,
// and MCP tool integration for the pi-go agent.
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"time"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"
)

// HookConfig defines a shell command hook that runs before or after tool calls.
type HookConfig struct {
	// Event is "before_tool" or "after_tool".
	Event string `json:"event"`
	// Command is the shell command to execute.
	Command string `json:"command"`
	// Tools optionally restricts this hook to specific tool names.
	// If empty, the hook fires for all tools.
	Tools []string `json:"tools,omitempty"`
	// Timeout in seconds for hook execution. Default: 10.
	Timeout int `json:"timeout,omitempty"`
}

// matchesTool returns true if the hook should fire for the given tool name.
func (h HookConfig) matchesTool(name string) bool {
	if len(h.Tools) == 0 {
		return true
	}
	return slices.Contains(h.Tools, name)
}

func (h HookConfig) timeout() time.Duration {
	if h.Timeout > 0 {
		return time.Duration(h.Timeout) * time.Second
	}
	return 10 * time.Second
}

// BuildBeforeToolCallbacks converts HookConfigs with event "before_tool" into
// ADK BeforeToolCallback functions.
func BuildBeforeToolCallbacks(hooks []HookConfig) []llmagent.BeforeToolCallback {
	var cbs []llmagent.BeforeToolCallback
	for _, h := range hooks {
		if h.Event != "before_tool" {
			continue
		}
		hook := h // capture
		cbs = append(cbs, func(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
			if !hook.matchesTool(t.Name()) {
				return args, nil
			}
			if err := runHookCommand(ctx, hook, t.Name(), args); err != nil {
				log.Printf("hook %q failed for tool %q: %v", hook.Command, t.Name(), err)
				// Non-fatal: log and continue.
			}
			return args, nil
		})
	}
	return cbs
}

// BuildAfterToolCallbacks converts HookConfigs with event "after_tool" into
// ADK AfterToolCallback functions.
func BuildAfterToolCallbacks(hooks []HookConfig) []llmagent.AfterToolCallback {
	var cbs []llmagent.AfterToolCallback
	for _, h := range hooks {
		if h.Event != "after_tool" {
			continue
		}
		hook := h // capture
		cbs = append(cbs, func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
			if !hook.matchesTool(t.Name()) {
				return result, nil
			}
			if hookErr := runHookCommand(ctx, hook, t.Name(), result); hookErr != nil {
				log.Printf("hook %q failed for tool %q: %v", hook.Command, t.Name(), hookErr)
			}
			return result, nil
		})
	}
	return cbs
}

// runHookCommand executes a hook's shell command with the tool name and data as JSON on stdin.
func runHookCommand(ctx context.Context, hook HookConfig, toolName string, data map[string]any) error {
	hookCtx, cancel := context.WithTimeout(ctx, hook.timeout())
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "sh", "-c", hook.Command)

	// Pass context as JSON on stdin.
	input := map[string]any{
		"tool": toolName,
		"data": data,
	}
	jsonBytes, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshaling hook input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(jsonBytes)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q: %w (stderr: %s)", hook.Command, err, stderr.String())
	}
	return nil
}
